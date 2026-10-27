#!/usr/bin/env python3
"""Freeze a failure-enriched diagnostic cohort; never a representative benchmark."""
import argparse
import hashlib
import json
from pathlib import Path


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load(path):
    return json.loads(path.read_text())


def stable(values, seed):
    return sorted(values, key=lambda value: hashlib.sha256(f"{seed}:{value['id']}".encode()).hexdigest())


def select_arena(jobs, seed):
    selected = []
    for domain in ["formal_reasoning_math", "formal_reasoning_phys"]:
        rows = [job for job in jobs if job["dataset"] == domain]
        for kind, predicate in [
            ("execution_failure", lambda a: any(x.get("error") for x in a)),
            ("wrong_answer", lambda a: not any(x.get("error") for x in a) and any(not x["correct"] for x in a)),
            ("success_control", lambda a: all(x["judged"] and x["correct"] and not x.get("error") for x in a)),
        ]:
            candidates = [row for row in rows if predicate(row["answers"])]
            if not candidates:
                raise ValueError(f"No {kind} chain for {domain}")
            minimum = min(len(row["answers"]) for row in candidates)
            row = stable([row for row in candidates if len(row["answers"]) == minimum], seed)[0]
            selected.append({"dataset": domain, "id": row["id"], "task_id": int(row["id"].rsplit("-", 1)[1]), "steps": len(row["answers"]), "prior_status": kind})
    return selected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--locomo-results", type=Path, action="append", required=True)
    parser.add_argument("--locomo-dataset", type=Path, required=True)
    parser.add_argument("--longmem-run", type=Path, required=True)
    parser.add_argument("--arena-run", type=Path, required=True)
    parser.add_argument("--seed", default="memstation-failure-cohort-v1")
    args = parser.parse_args()
    out = args.output.resolve()
    if out.exists():
        raise ValueError("Use a fresh output directory")
    sources = [{"path": str(path.resolve()), "sha256": digest(path)} for path in args.locomo_results]
    locomo = []
    for path in args.locomo_results:
        for row in load(path)["results"]:
            answer = row["answer"]
            locomo.append({"id": answer["question_id"], "sample_id": answer["sample_id"], "category": answer["category"], "prior_status": "execution_failure" if answer.get("error") else "success_control" if answer["correct"] else "wrong_answer"})
    if len({row["id"] for row in locomo}) != len(locomo):
        raise ValueError("Repeated LOCOMO case")
    selected = []
    for category in range(1, 5):
        failed = [row for row in locomo if row["category"] == category and row["prior_status"] != "success_control"]
        selected.extend(stable(failed, args.seed)[:1])
    chosen = {row["id"] for row in selected}
    selected.extend(stable([row for row in locomo if row["prior_status"] != "success_control" and row["id"] not in chosen], args.seed)[:8-len(selected)])
    selected.extend(stable([row for row in locomo if row["prior_status"] == "success_control"], args.seed)[:2])
    if len(selected) != 10:
        raise ValueError("Need eight LOCOMO failures across four categories and two controls")
    longmem = [load(path) for path in args.longmem_run.glob("*-memory.json") if load(path).get("dataset") == "longmemeval_s"]
    long_selected = [{"id": job["id"], "prior_status": "execution_failure" if job["answers"][0].get("error") else "wrong_answer"} for job in longmem if not job["answers"][0]["correct"]]
    for abstention in [True, False]:
        values = [{"id": row["id"], "prior_status": "success_control"} for row in longmem if row["answers"][0]["correct"] and row["id"].endswith("_abs") == abstention]
        long_selected.extend(stable(values, args.seed)[:1])
    arena_jobs = [load(path) for path in args.arena_run.glob("*-memory.json")]
    arena = select_arena(arena_jobs, args.seed)
    manifest = load(args.longmem_run / "manifest.json")
    arena_manifest = load(args.arena_run / "manifest.json")
    for path in [args.longmem_run / "manifest.json", args.arena_run / "manifest.json"]:
        sources.append({"path": str(path.resolve()), "sha256": digest(path)})
    plan = {
        "version": 1,
        "scope": "Failure-enriched development regression; all five prior LongMemEval failures, controls, and six complete shortest Arena chains; not representative or held out",
        "seed": args.seed,
        "locomo": {"path": str(args.locomo_dataset.resolve()), "sha256": digest(args.locomo_dataset)},
        "longmemeval": manifest["plan"]["longmemeval"],
        "arena": arena_manifest["plan"]["arena"],
        "selection": {"locomo": sorted(selected, key=lambda row: row["id"]), "longmemeval": sorted(long_selected, key=lambda row: row["id"]), "arena": arena},
        "selection_sources": sources,
        "arena_baseline_manifest": {"path": str((args.arena_run / "manifest.json").resolve()), "sha256": digest(args.arena_run / "manifest.json")},
        "arena_baselines": {row["id"]: {"path": str((args.arena_run / (row["id"] + "-none.json")).resolve()), "sha256": digest(args.arena_run / (row["id"] + "-none.json"))} for row in arena},
        "arena_baseline_policy": "Reuse historical complete low model-only controls, including failures; no reader/judge calls; original budget retained, not same-budget causal comparison",
        "modes": ["none", "raw", "derived", "combined"],
        "reader_output_tokens": 16384, "max_output_tokens": 65536,
        "context_bytes": 128000, "top_n": 10, "rerank_candidates": 50,
        "reader_thinking": {"locomo": "high", "longmemeval_s": "high", "formal_reasoning_math": "low", "formal_reasoning_phys": "low"},
        "worker_thinking": "low", "concurrency": 4,
        "source_policy": "all original histories; chronological own past complete solutions only; no references, grades, query-directed extraction or future background",
    }
    plan["expected_questions"] = len(selected) + len(long_selected) + sum(row["steps"] for row in arena)
    plan["expected_answers"] = plan["expected_questions"] * len(plan["modes"])
    out.mkdir(parents=True, mode=0o700)
    path = out / "plan.json"
    path.write_text(json.dumps(plan, indent=2) + "\n")
    path.chmod(0o600)
    print(f"Frozen {plan['expected_questions']} questions / {plan['expected_answers']} answers: {path}")


if __name__ == "__main__":
    main()
