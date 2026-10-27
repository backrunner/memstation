#!/usr/bin/env python3
"""Summarize the preregistered static-cohort retrieval ablation without dropping failures."""
import argparse
import collections
import hashlib
import json
import math
from pathlib import Path

PENDING = {"not executed", "attempt interrupted before completion"}


def p95(values):
    values = sorted(value for value in values if value > 0)
    return values[math.ceil(len(values) * .95) - 1] if values else None


def correct(row):
    answer = row["answer"]
    return bool(row["qa"] and answer["judged"] and answer["correct"] and not answer.get("error"))


def summarize(rows):
    buckets = collections.defaultdict(list)
    for row in rows:
        buckets[(row["dataset"], row["variant"], row["mode"])].append(row)
    metrics = {}
    for (dataset, variant, mode), values in sorted(buckets.items()):
        attempted = [row for row in values if row["answer"].get("error") not in PENDING]
        qa = [row for row in values if row["qa"]]
        times = [row["retrieval"]["timings"] for row in attempted if row.get("retrieval", {}).get("timings")]
        entry = {
            "expected_searches": len(values), "completed": len(attempted),
            "expected_qa": len(qa), "correct": sum(correct(row) for row in qa),
            "accuracy": sum(correct(row) for row in qa) / len(qa) if qa else None,
            "execution_errors": sum(bool(row["answer"].get("error")) for row in attempted),
            "search_p95_ms": p95([row["answer"]["search_millis"] for row in attempted]),
            "phase_p95_ms": {key: p95([value[key] for value in times]) for key in times[0]} if times else {},
            "rerank_input_bytes_p95": p95([row["answer"]["rerank"].get("input_bytes", 0) for row in attempted]),
            "reader_p95_ms": p95([row["answer"]["read_millis"] for row in attempted]),
            "context_bytes_p95": p95([len(row["answer"]["context"].encode()) for row in attempted]),
        }
        coverage = [row["source_recall"] for row in values if row.get("source_recall")]
        entry["source_provenance"] = {
            "questions": len(coverage),
            "retrieved_all": sum(value["retrieved_recall"] == 1 for value in coverage),
            "admitted_all": sum(value["admitted_recall"] == 1 for value in coverage),
        }
        metrics.setdefault(dataset, {}).setdefault(variant, {})[mode] = entry
    lookup = {(row["dataset"], row["question_id"], row["variant"], row["mode"]): row for row in rows}
    pairs = {}
    for row in rows:
        if row["variant"] == "4b-50" or not row["qa"]:
            continue
        base = lookup[(row["dataset"], row["question_id"], "4b-50", row["mode"])]
        if any(value["answer"].get("error") in PENDING for value in [row, base]):
            continue
        counts = pairs.setdefault(row["dataset"], {}).setdefault(row["variant"] + ":" + row["mode"], collections.Counter())
        counts["pairs"] += 1
        counts["gains"] += correct(row) and not correct(base)
        counts["regressions"] += correct(base) and not correct(row)
        if row["answer"]["context_sha256"] == base["answer"]["context_sha256"]:
            counts["identical_context_grade_disagreements"] += correct(row) != correct(base)
    return metrics, pairs


def expected_cases(parent, profile):
    cases = {}
    for kind in ["locomo", "longmemeval"]:
        for selected in parent["selection"][kind]:
            group = "locomo-" + selected["sample_id"] if kind == "locomo" else "longmemeval-" + selected["id"]
            dataset = "locomo" if kind == "locomo" else "longmemeval_s"
            for variant in profile["variants"]:
                for mode in profile["modes"]:
                    identity = (group, selected["id"], variant["name"], mode)
                    name = hashlib.sha256("\0".join(identity).encode()).hexdigest() + ".json"
                    if name in cases:
                        raise ValueError("Duplicate static profile case")
                    cases[name] = (dataset, selected["id"], variant["name"], mode)
    return cases


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--parent", type=Path, required=True)
    args = parser.parse_args()
    raw_parent = (args.parent / "manifest.json").read_bytes()
    parent = json.loads(raw_parent)
    manifest = json.loads((args.profile / "manifest.json").read_bytes())
    if hashlib.sha256(raw_parent).hexdigest() != manifest["parent_manifest_sha256"]:
        raise ValueError("Profile refers to another parent corpus")
    expected = expected_cases(parent["plan"], manifest["profile_plan"])
    files = {path.name: path for path in args.profile.glob("*.json") if len(path.stem) == 64}
    if set(files) != set(expected) or len(expected) != manifest["expected_searches"]:
        raise ValueError("Missing or unexpected files; refusing a reduced profile denominator")
    rows = []
    for name, identity in expected.items():
        row = json.loads(files[name].read_bytes())
        if tuple(row[key] for key in ["dataset", "question_id", "variant", "mode"]) != identity:
            raise ValueError("Profile identity mismatch")
        if row["qa"] != (row["mode"] in manifest["profile_plan"]["qa_modes"]):
            raise ValueError("Changed QA denominator")
        answer = row["answer"]
        if answer.get("context_sha256") and hashlib.sha256(answer["context"].encode()).hexdigest() != answer["context_sha256"]:
            raise ValueError("Profile context digest mismatch")
        rows.append(row)
    if sum(row["qa"] for row in rows) != manifest["expected_qa"]:
        raise ValueError("Changed QA denominator")
    metrics, pairs = summarize(rows)
    complete = (args.profile / "completion.json").exists() and all(row["answer"].get("error") not in PENDING for row in rows)
    report = {"completed": complete, "expected_searches": len(rows), "expected_qa": manifest["expected_qa"], "datasets": metrics, "paired_vs_new_4b50_baseline": pairs,
        "interpretation": "Every static development question, fixed worker corpus, SELECT-only role, warm content cache, sequential model requests. Smaller candidate limits also change first-stage prefetch. New baseline QA exposes inference/judge variation; provenance overlap does not prove semantic completeness. Not a full benchmark, held-out result, cold latency or production SLO."}
    out = args.profile / "reports"
    out.mkdir(mode=0o700, exist_ok=True)
    for name, value in [("summary.json", report), ("failures.json", [row for row in rows if row["qa"] and row["answer"].get("error") not in PENDING and not correct(row)])]:
        path = out / name
        path.write_text(json.dumps(value, indent=2) + "\n")
        path.chmod(0o600)
    print("Profile searches:", len(rows), "QA:", manifest["expected_qa"], "completed:", complete)
    print("Report:", out)


if __name__ == "__main__":
    main()
