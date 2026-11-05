"""Pinned local encoder service. No conversation storage or semantic fact writes."""
from __future__ import annotations

import hashlib
import hmac
import json
import math
import os
import re
import select
import socket
import threading
import platform
import sys
import time
from collections import deque
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

LABELS = ["person", "organization", "location", "project", "date", "time", "preference", "task", "event"]
ACTIONS = ["requires semantic interpretation", "direct source mentions only"]
UIE_ACTION_SCHEMA = "memory_action [" + ", ".join(ACTIONS) + "]"
MAX_BODY = 1 << 20
# Lets a caller tell this running process from the code on disk it should be running.
CODE_SHA256 = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()


def normalize_spans(text: str, values: dict) -> list[dict]:
    spans = []
    for label, items in values.items():
        if label not in LABELS:
            continue
        for item in items:
            start, end = item.get("start"), item.get("end")
            confidence = item.get("confidence", item.get("probability"))
            value = item.get("text")
            # GLiNER appends sentence punctuation to its input, so an entity at the
            # end of unpunctuated text reports characters beyond the source.
            if (type(end) is int and type(value) is str and end == len(text) + 1 and
                    value.endswith(".")):
                end, value = end - 1, value[:-1]
            if (type(start) is not int or type(end) is not int or
                    not 0 <= start < end <= len(text) or
                    text[start:end] != value or
                    type(confidence) not in (float, int) or
                    not math.isfinite(confidence) or not 0 <= confidence <= 1):
                raise ValueError("invalid model source span")
            # Sentence punctuation is not part of a name; "Sam." and "Sam" are one key.
            trimmed = value.rstrip(".,;:!?")
            if trimmed:
                end, value = start + len(trimmed), trimmed
            spans.append({"start": start, "end": end, "text": value,
                          "label": label, "confidence": confidence})
    if len(spans) > 64:
        raise ValueError("model span budget exceeded")
    return spans


def classification(result: dict) -> float:
    label, confidence = result.get("label"), result.get("confidence")
    if (label not in ACTIONS or type(confidence) not in (int, float) or
            not math.isfinite(confidence) or not 0 <= confidence <= 1):
        raise ValueError("invalid model classification")
    return confidence if label == ACTIONS[0] else 1 - confidence


class Encoder:
    def __init__(self, backend: str, model: str, revision: str, splitter: str):
        self.backend, self.model, self.revision, self.splitter = backend, model, revision, splitter
        if backend == "gliner":
            if not re.fullmatch(r"[0-9a-f]{40}", revision):
                raise ValueError("GLiNER requires an immutable Hugging Face revision")
            from huggingface_hub import snapshot_download
            from gliner2 import AutoExtractor
            path = snapshot_download(repo_id=model, revision=revision)
            self.encoder = AutoExtractor.from_pretrained(path, word_splitter=splitter,
                                                         map_location=os.getenv("MEMSTATION_EXTRACTOR_DEVICE", "cpu"))
        elif backend == "uie":
            if sys.platform != "linux" or platform.machine() != "x86_64":
                raise RuntimeError("UIE requires Linux x86_64; a current dependency has no other platform wheel")
            # Local UIE weights must have a reviewed, complete file manifest.
            path = Path(os.environ["MEMSTATION_EXTRACTOR_MODEL_PATH"]).resolve()
            manifest_bytes = (path / "memstation-manifest.json").read_bytes()
            if revision != "sha256:" + hashlib.sha256(manifest_bytes).hexdigest():
                raise ValueError("UIE manifest revision mismatch")
            manifest = json.loads(manifest_bytes)
            if not manifest or not isinstance(manifest, dict):
                raise ValueError("empty UIE manifest")
            actual = {str(file.relative_to(path)) for file in path.rglob("*")
                      if file.is_file() and file.name != "memstation-manifest.json"}
            if actual != set(manifest):
                raise ValueError("UIE manifest must cover every artifact")
            for name, digest in manifest.items():
                target = (path / name).resolve()
                if (path not in target.parents or not isinstance(digest, str) or
                        not re.fullmatch(r"[0-9a-f]{64}", digest) or
                        hashlib.sha256(target.read_bytes()).hexdigest() != digest):
                    raise ValueError("UIE artifact hash mismatch")
            from paddlenlp import Taskflow
            self.encoder = Taskflow("information_extraction", model=model,
                                    task_path=str(path), schema_lang="en",
                                    schema=LABELS + [UIE_ACTION_SCHEMA])
        else:
            raise ValueError("unsupported extractor backend")

    def extract(self, text: str, purpose: str) -> dict:
        if self.backend == "gliner":
            entities = self.encoder.extract_entities(text, LABELS, include_spans=True,
                                                       include_confidence=True)
            spans = normalize_spans(text, entities["entities"])
            score = 0.0
            if purpose == "source":
                actions = self.encoder.classify_text(text, {"memory_action": ACTIONS},
                                                     include_confidence=True)
                score = classification(actions["memory_action"])
        else:
            self.encoder.set_schema(LABELS + ([UIE_ACTION_SCHEMA] if purpose == "source" else []))
            result = self.encoder(text)[0]
            spans = normalize_spans(text, {key: value for key, value in result.items() if key in LABELS})
            score = 0.0
            if purpose == "source":
                action = result.get(UIE_ACTION_SCHEMA, [])
                if len(action) != 1:
                    raise ValueError("missing UIE classification")
                score = classification({"label": action[0]["text"], "confidence": action[0]["probability"]})
        # supported describes this configured encoder path, not a language-quality claim.
        return {"spans": spans, "needs_semantic": score, "supported": True}


def validate_request(payload: dict, encoder: Encoder, purpose: str) -> list[str]:
    if purpose not in ("source", "query"):
        raise ValueError("invalid extraction purpose")
    if (set(payload) != {"version", "model", "revision", "splitter", "texts"} or
            payload["version"] != 1 or payload["model"] != encoder.model or
            payload["revision"] != encoder.revision or payload["splitter"] != encoder.splitter):
        raise ValueError("extractor identity mismatch")
    texts = payload["texts"]
    if not isinstance(texts, list) or len(texts) > 256 or any(type(text) is not str or not 0 < len(text) <= 512 for text in texts):
        raise ValueError("invalid source windows")
    return texts


def extract_request(payload: dict, encoder: Encoder, purpose: str) -> dict:
    texts = validate_request(payload, encoder, purpose)
    observations = [encoder.extract(text, purpose) for text in texts]
    return {"version": 1, "model": encoder.model, "revision": encoder.revision,
            "splitter": encoder.splitter, "observations": observations}


class InferenceGate:
    """One model call at a time; a bounded FIFO with caller-specific waits."""
    def __init__(self, max_waiters: int = 8):
        self.condition = threading.Condition()
        self.waiters = deque()
        self.active = False
        self.max_waiters = max_waiters

    def acquire(self, timeout: float, priority: bool = False) -> bool:
        with self.condition:
            if not self.active and not self.waiters:
                self.active = True
                return True
            if timeout <= 0 or len(self.waiters) >= self.max_waiters:
                return False
            ticket = object()
            # Online queries go ahead of queued background source batches.
            if priority:
                self.waiters.appendleft(ticket)
            else:
                self.waiters.append(ticket)
            self.condition.notify_all()
            deadline = time.monotonic() + timeout
            while self.active or self.waiters[0] is not ticket:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    self.waiters.remove(ticket)
                    self.condition.notify_all()
                    return False
                self.condition.wait(remaining)
            self.waiters.popleft()
            self.active = True
            return True

    def release(self):
        with self.condition:
            self.active = False
            self.condition.notify_all()


def handler(encoder: Encoder, token: str, *, source_wait_seconds: float = 10,
            max_waiters: int = 8):
    gate = InferenceGate(max_waiters)

    class Handler(BaseHTTPRequestHandler):
        inference_gate = gate

        def log_message(self, *_args):
            pass  # Never log input, credentials, model responses or request URLs.

        def reply(self, status: int, payload: dict):
            raw = json.dumps(payload, ensure_ascii=False, allow_nan=False).encode()
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.send_header("Cache-Control", "no-store")
                if status == 503 and payload.get("error") == "busy":
                    self.send_header("Retry-After", "1")
                self.end_headers()
                self.wfile.write(raw)
            except (BrokenPipeError, ConnectionResetError):
                # Cancellation during/after inference is not another inference
                # failure and must not trigger a second write to the closed peer.
                return

        def do_GET(self):
            self.reply(200 if self.path == "/health/ready" else 404,
                       {"ready": self.path == "/health/ready", "code_sha256": CODE_SHA256})

        def do_POST(self):
            self.connection.settimeout(65)
            if self.path not in ("/extract", "/entities"):
                self.reply(404, {"error": "not_found"})
                return
            if not hmac.compare_digest(self.headers.get("Authorization", ""), "Bearer " + token):
                self.reply(401, {"error": "unauthorized"})
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if length <= 0 or length > MAX_BODY:
                    raise ValueError("body budget exceeded")
                payload = json.loads(self.rfile.read(length))
                purpose = "query" if self.path == "/entities" else "source"
                # Invalid requests cannot occupy a scarce inference/queue slot.
                validate_request(payload, encoder, purpose)
                wait_seconds = source_wait_seconds
                if purpose == "query":
                    timeout_ms = self.headers.get("X-Memstation-Timeout-Ms", "")
                    if not re.fullmatch(r"[0-9]{1,4}", timeout_ms) or not 0 < int(timeout_ms) <= 5000:
                        raise ValueError("bounded query timeout required")
                    # Reserve half the caller budget for inference and posting
                    # lookup. Never retry; source jobs keep their longer wait.
                    wait_seconds = int(timeout_ms) / 2000
                if not gate.acquire(wait_seconds, priority=purpose == "query"):
                    self.reply(503, {"error": "busy"})
                    return
                try:
                    # A caller may cancel while queued. Do not spend
                    # inference on a connection already closed before admission.
                    readable, _, _ = select.select([self.connection], [], [], 0)
                    if readable and not self.connection.recv(1, socket.MSG_PEEK):
                        return
                    result = extract_request(payload, encoder, purpose)
                finally:
                    gate.release()
                self.reply(200, result)
            except (ValueError, KeyError, TypeError):
                self.reply(400, {"error": "invalid_extraction"})
            except Exception:
                self.reply(503, {"error": "inference_failed"})

    return Handler


def main():
    token = os.environ["MEMSTATION_EXTRACTOR_TOKEN"]
    if not token or "\r" in token or "\n" in token:
        raise ValueError("extractor token required")
    encoder = Encoder(os.environ["MEMSTATION_EXTRACTOR_BACKEND"],
                      os.environ["MEMSTATION_EXTRACTOR_MODEL"],
                      os.environ["MEMSTATION_EXTRACTOR_REVISION"],
                      os.environ["MEMSTATION_EXTRACTOR_SPLITTER"])
    server = ThreadingHTTPServer((os.getenv("MEMSTATION_EXTRACTOR_HOST", "127.0.0.1"),
                                  int(os.getenv("MEMSTATION_EXTRACTOR_PORT", "8092"))), handler(encoder, token))
    server.serve_forever()


if __name__ == "__main__":
    main()
