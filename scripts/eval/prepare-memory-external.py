#!/usr/bin/env python3
"""Pin and prepare external evaluation inputs; contains no inference logic."""

import argparse
import collections
import hashlib
import json
from pathlib import Path
import urllib.request

LONGMEM_REVISION = "98d7416c24c778c2fee6e6f3006e7a073259d48f"
LONGMEM_SHA256 = "d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442"
ARENA_REVISION = "da1a37c8b19280e18627ca01cf368195a5e1d92e"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def download(url, path):
    if path.exists():
        raise ValueError(f"Refusing to overwrite {path}")
    temporary = path.with_suffix(path.suffix + ".download")
    with urllib.request.urlopen(url, timeout=180) as response, temporary.open("xb") as out:
        while chunk := response.read(1024 * 1024):
            out.write(chunk)
    temporary.chmod(0o600)
    temporary.rename(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--tracks", choices=["all", "longmemeval", "memoryarena"], default="all")
    parser.add_argument("--longmemeval-path", type=Path)
    parser.add_argument("--per-group", type=int, default=6)
    parser.add_argument("--seed", default="memstation-external-2026-10-27")
    parser.add_argument("--concurrency", type=int, default=4)
    control = parser.add_mutually_exclusive_group()
    control.add_argument("--arena-baseline-run", type=Path, help="Reuse an existing complete low model-only control run")
    control.add_argument("--create-arena-baseline", action="store_true", help="Explicitly create a new model-only control for a separate model experiment; spends generation/judge tokens")
    args = parser.parse_args()
    if args.tracks != "longmemeval" and not args.arena_baseline_run and not args.create_arena_baseline:
        parser.error("MemoryArena requires --arena-baseline-run; new controls require explicit --create-arena-baseline")
    if args.arena_baseline_run and not (args.arena_baseline_run / "completion.json").is_file():
        parser.error("Arena baseline must be a completed run")
    if not 1 <= args.per_group <= 30 or not 1 <= args.concurrency <= 8:
        parser.error("per-group must be 1..30 and concurrency 1..8")
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False, mode=0o700)
    selected = []
    longmem = None
    if args.tracks != "memoryarena":
        longmem = args.longmemeval_path.resolve() if args.longmemeval_path else out / "longmemeval_s_cleaned.json"
        if not args.longmemeval_path:
            download(
                f"https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/{LONGMEM_REVISION}/longmemeval_s_cleaned.json",
                longmem,
            )
        if digest(longmem) != LONGMEM_SHA256:
            raise ValueError("LongMemEval file differs from the pinned cleaned S release")
        groups = collections.defaultdict(list)
        for row in json.loads(longmem.read_text()):
            group = "abstention" if row["question_id"].endswith("_abs") else row["question_type"]
            groups[group].append(row["question_id"])
        expected_groups = {
            "abstention", "single-session-user", "single-session-assistant",
            "single-session-preference", "knowledge-update", "temporal-reasoning", "multi-session",
        }
        if set(groups) != expected_groups:
            raise ValueError("Unexpected LongMemEval capability groups")
        for name in sorted(groups):
            ids = sorted(groups[name], key=lambda value: hashlib.sha256(f"{args.seed}:{value}".encode()).hexdigest())
            if len(ids) < args.per_group:
                raise ValueError(f"Insufficient questions in {name}")
            selected.extend(ids[:args.per_group])
    arena = []
    for name in (["formal_reasoning_math", "formal_reasoning_phys"] if args.tracks != "longmemeval" else []):
        path = out / f"{name}.jsonl"
        download(f"https://huggingface.co/datasets/ZexueHe/memoryarena/resolve/{ARENA_REVISION}/{name}/data.jsonl", path)
        arena.append({"name": name, "path": str(path), "sha256": digest(path), "revision": ARENA_REVISION})
    plan = {
        "scope": f"{args.tracks}: stratified LongMemEval-S when selected; complete MemoryArena math/physics when selected; paired raw-memory and no-memory controls",
        "longmemeval": {"path": str(longmem), "sha256": LONGMEM_SHA256, "revision": LONGMEM_REVISION, "question_ids": selected} if longmem else None,
        "arena": arena, "top_n": 10, "context_bytes": 64000,
        "longmem_max_output_tokens": 4096, "arena_max_output_tokens": 8192,
        "concurrency": args.concurrency, "rerank_timeout_seconds": 180,
        "source_policy": "original_full_sessions_and_own_past_question_solution_only",
        "arena_agent": "single_call_no_external_tools",
        "judge_signal_in_memory": False, "reader_policy": "interpretation",
    }
    if arena:
        plan["create_arena_baseline"] = args.create_arena_baseline
        if args.arena_baseline_run:
            root = args.arena_baseline_run.resolve()
            plan["arena_baseline_manifest"] = {"path": str(root / "manifest.json"), "sha256": digest(root / "manifest.json")}
            plan["arena_baselines"] = {load_id: {"path": str(path), "sha256": digest(path)} for path in sorted(root.glob("*-none.json")) for load_id in [json.loads(path.read_text())["id"]]}
            plan["arena_baseline_policy"] = "Historical complete low model-only controls retained verbatim; no reader/judge calls; original budgets retained; not a same-budget causal ablation"
    (out / "plan.json").write_text(json.dumps(plan, indent=2) + "\n")
    (out / "selection.json").write_text(json.dumps({"seed": args.seed, "per_group": args.per_group, "ids": selected}, indent=2) + "\n")
    print(f"Prepared {len(selected)} LongMemEval questions and {len(arena)} complete formal-reasoning tracks: {out / 'plan.json'}")


if __name__ == "__main__":
    main()
