import unittest
import concurrent.futures
import hashlib
import http.client
import json
import threading
import time
import select
import server as server_module
from server import (Encoder, InferenceGate, normalize_spans, ThreadingHTTPServer, UIE_ACTION_SCHEMA,
                    classification, extract_request, handler, normalize_spans)


class FakeEncoder:
    model, revision, splitter = "model", "pinned", "char"

    def extract(self, text, purpose):
        return {"spans": [], "needs_semantic": 0.9, "supported": True}


class ProtocolTests(unittest.TestCase):
    def test_entity_at_end_of_unpunctuated_text_stays_grounded(self):
        text = "Alice met Bob in Paris"
        spans = normalize_spans(text, {"location": [{"text": "Paris.", "start": 17, "end": 23, "confidence": .9}]})
        self.assertEqual([(s["start"], s["end"], s["text"]) for s in spans], [(17, 22, "Paris")])
        self.assertEqual(text[17:22], "Paris")

    def test_sentence_punctuation_is_not_part_of_a_name(self):
        spans = normalize_spans("With Sam.", {"person": [{"text": "Sam.", "start": 5, "end": 9, "confidence": .9}]})
        self.assertEqual(spans[0]["text"], "Sam")
        self.assertEqual(spans[0]["end"], 8)

    def test_span_beyond_the_source_is_still_rejected(self):
        with self.assertRaises(ValueError):
            normalize_spans("Paris", {"location": [{"text": "Paris and", "start": 0, "end": 9, "confidence": .9}]})

    def test_query_goes_ahead_of_queued_source_batches(self):
        gate = InferenceGate(8)
        self.assertTrue(gate.acquire(0))
        order = []

        def wait(name, priority):
            if gate.acquire(2, priority=priority):
                order.append(name)
                gate.release()

        source = threading.Thread(target=wait, args=("source", False))
        source.start()
        with gate.condition:
            self.assertTrue(gate.condition.wait_for(lambda: len(gate.waiters) == 1, 1))
        query = threading.Thread(target=wait, args=("query", True))
        query.start()
        with gate.condition:
            self.assertTrue(gate.condition.wait_for(lambda: len(gate.waiters) == 2, 1))
        gate.release()
        source.join(3)
        query.join(3)
        self.assertEqual(order, ["query", "source"])

    def test_query_waits_through_brief_contention_without_extra_inference(self):
        calls = []

        class RecordingEncoder(FakeEncoder):
            def extract(self, text, purpose):
                calls.append((text, purpose))
                return super().extract(text, purpose)

        server = ThreadingHTTPServer(("127.0.0.1", 0), handler(RecordingEncoder(), "token"))
        gate = server.RequestHandlerClass.inference_gate
        self.assertTrue(gate.acquire(0))
        serving = threading.Thread(target=server.serve_forever, daemon=True)
        serving.start()

        def query(timeout_ms="200"):
            conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=2)
            try:
                conn.request("POST", "/entities", json.dumps(dict(version=1, model="model",
                             revision="pinned", splitter="char", texts=["person"])),
                             {"Authorization": "Bearer token", "X-Memstation-Timeout-Ms": timeout_ms})
                response = conn.getresponse()
                response.read()
                return response.status
            finally:
                conn.close()

        try:
            for timeout_ms in ["", "-1", "0", "5001", "nan"]:
                self.assertEqual(query(timeout_ms), 400)
            self.assertEqual(calls, [])
            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                pending = pool.submit(query)
                with gate.condition:
                    self.assertTrue(gate.condition.wait_for(lambda: len(gate.waiters) == 1, 1))
                gate.release()
                self.assertEqual(pending.result(), 200)
            self.assertEqual(calls, [("person", "query")])
            # Long contention remains bounded and must not run abandoned work.
            self.assertTrue(gate.acquire(0))
            start = time.monotonic()
            self.assertEqual(query("20"), 503)
            self.assertLess(time.monotonic() - start, .5)
            self.assertEqual(calls, [("person", "query")])
            with gate.condition:
                self.assertEqual(len(gate.waiters), 0)
            gate.release()
        finally:
            gate.release()
            server.shutdown()
            server.server_close()
            serving.join()

    def test_source_waits_but_queries_and_overflow_fail_without_inference(self):
        entered, release = threading.Event(), threading.Event()
        calls = []

        class BlockingEncoder(FakeEncoder):
            def extract(self, text, purpose):
                calls.append(text)
                if text == "first":
                    entered.set()
                    if not release.wait(3):
                        raise RuntimeError("test did not release encoder")
                return super().extract(text, purpose)

        server = ThreadingHTTPServer(("127.0.0.1", 0),
                                    handler(BlockingEncoder(), "test-token",
                                            source_wait_seconds=2, max_waiters=1))
        serving = threading.Thread(target=server.serve_forever, daemon=True)
        serving.start()

        def post(text, path="/extract", revision="pinned"):
            conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=4)
            try:
                body = json.dumps(dict(version=1, model="model", revision=revision,
                                       splitter="char", texts=[text]))
                conn.request("POST", path, body, {"Authorization": "Bearer test-token", "X-Memstation-Timeout-Ms": "200"})
                response = conn.getresponse()
                result = response.status, response.getheader("Retry-After")
                response.read()
                return result
            finally:
                conn.close()

        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
                first = pool.submit(post, "first")
                self.assertTrue(entered.wait(2))
                # The second source remains queued while an interactive query
                # fails quickly. Releasing the first executes each source once.
                second = pool.submit(post, "second")
                gate = server.RequestHandlerClass.inference_gate
                with gate.condition:
                    self.assertTrue(gate.condition.wait_for(lambda: len(gate.waiters) == 1, 1))
                start = time.monotonic()
                self.assertEqual(post("query", "/entities"), (503, "1"))
                self.assertLess(time.monotonic() - start, .5)
                self.assertEqual(post("overflow"), (503, "1"))
                self.assertEqual(post("invalid", revision="wrong"), (400, None))
                self.assertFalse(second.done())
                release.set()
                self.assertEqual(first.result(), (200, None))
                self.assertEqual(second.result(), (200, None))
                self.assertEqual(calls, ["first", "second"])
        finally:
            release.set()
            server.shutdown()
            server.server_close()
            serving.join()

    def test_disconnected_queued_requests_do_not_run_inference(self):
        for path in ["/extract", "/entities"]:
            with self.subTest(path=path):
                self.check_disconnected_queued_request(path)

    def check_disconnected_queued_request(self, path):
        calls = []
        connections = []

        class RecordingEncoder(FakeEncoder):
            def extract(self, text, purpose):
                calls.append(text)
                return super().extract(text, purpose)

        class ObservedHandler(handler(RecordingEncoder(), "token")):
            def setup(self):
                super().setup()
                connections.append(self.connection)

        server = ThreadingHTTPServer(("127.0.0.1", 0), ObservedHandler)
        gate = server.RequestHandlerClass.inference_gate
        self.assertTrue(gate.acquire(0))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=2)
        try:
            conn.request("POST", path, json.dumps(dict(version=1, model="model",
                         revision="pinned", splitter="char", texts=["abandoned"])),
                         {"Authorization": "Bearer token", "X-Memstation-Timeout-Ms": "200"})
            with gate.condition:
                self.assertTrue(gate.condition.wait_for(lambda: len(gate.waiters) == 1, 1))
            conn.close()
            # Observe the peer's FIN before admission; closing the client and
            # releasing the gate concurrently only races network delivery.
            self.assertTrue(select.select(connections, [], [], .05)[0])
            gate.release()
            with gate.condition:
                self.assertTrue(gate.condition.wait_for(lambda: not gate.active and not gate.waiters, 1))
            self.assertEqual(calls, [])
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            thread.join()

    def test_queue_timeout_does_not_leak_capacity(self):
        gate = InferenceGate(1)
        self.assertTrue(gate.acquire(0))
        self.assertFalse(gate.acquire(.01))
        self.assertEqual(len(gate.waiters), 0)
        gate.release()
        self.assertTrue(gate.acquire(0))
        gate.release()

    def test_fifo_prevents_new_queries_from_overtaking_waiting_sources(self):
        gate = InferenceGate(2)
        self.assertTrue(gate.acquire(0))
        admitted = []

        def source(name):
            self.assertTrue(gate.acquire(2))
            admitted.append(name)
            gate.release()

        threads = []
        for name in ["first", "second"]:
            thread = threading.Thread(target=source, args=(name,))
            threads.append(thread)
            thread.start()
            deadline = time.monotonic() + 1
            while len(gate.waiters) < len(threads) and time.monotonic() < deadline:
                time.sleep(.001)
            self.assertEqual(len(gate.waiters), len(threads))
        self.assertFalse(gate.acquire(0))
        gate.release()
        for thread in threads:
            thread.join()
        self.assertEqual(admitted, ["first", "second"])

    def test_identity_and_budget(self):
        body = dict(version=1, model="model", revision="pinned", splitter="char", texts=["中文"])
        self.assertEqual(extract_request(body, FakeEncoder(), "source")["observations"][0]["needs_semantic"], 0.9)
        for changes in ({"revision": "moving"}, {"extra": 1}, {"texts": ["a" * 513]}):
            with self.assertRaises(ValueError):
                extract_request(body | changes, FakeEncoder(), "source")

    def test_readiness_reports_the_code_this_process_loaded(self):
        server = ThreadingHTTPServer(("127.0.0.1", 0), handler(FakeEncoder(), "token"))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=2)
            conn.request("GET", "/health/ready")
            body = json.loads(conn.getresponse().read())
            with open(server_module.__file__, "rb") as source:
                self.assertEqual(body["code_sha256"], hashlib.sha256(source.read()).hexdigest())
        finally:
            server.shutdown()
            server.server_close()

    def test_repeated_unicode_span(self):
        text = "😀小林和小林"
        entity = dict(start=4, end=6, text="小林", confidence=0.9)
        self.assertEqual(normalize_spans(text, {"person": [entity]})[0]["start"], 4)
        for changes in ({"start": 0}, {"end": 99}, {"confidence": float("nan")}):
            with self.assertRaises(ValueError):
                normalize_spans(text, {"person": [entity | changes]})

    def test_uie_enumerated_classification_output(self):
        encoder = Encoder.__new__(Encoder)
        encoder.backend = "uie"
        class FakeUIE:
            def set_schema(self, schema):
                self.schema = schema
            def __call__(self, text):
                return [{UIE_ACTION_SCHEMA: [{"text": "requires semantic interpretation", "probability": .8}]}]
        encoder.encoder = FakeUIE()
        self.assertEqual(encoder.extract("a plan", "source")["needs_semantic"], .8)

    def test_query_extraction_skips_routing_classifier(self):
        class FakeGLiNER:
            def extract_entities(self, text, labels, **kwargs):
                return {"entities": {"project": [{"start": 0, "end": 10, "text": "MemStation", "confidence": .9}]}}
            def classify_text(self, *args, **kwargs):
                raise AssertionError("query must not run semantic-routing classifier")
        encoder = Encoder.__new__(Encoder)
        encoder.backend = "gliner"
        encoder.encoder = FakeGLiNER()
        result = encoder.extract("MemStation", "query")
        self.assertEqual(result["spans"][0]["text"], "MemStation")
        self.assertEqual(result["needs_semantic"], 0)

    def test_classifier_is_not_entity_presence(self):
        self.assertEqual(classification({"label": "requires semantic interpretation", "confidence": 0.9}), 0.9)
        with self.assertRaises(ValueError):
            classification({"label": "invented label", "confidence": 0.9})


if __name__ == "__main__":
    unittest.main()
