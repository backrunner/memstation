#!/usr/bin/env python3
"""Summarize every preallocated external-memory case, including failures."""

import argparse
import collections
import hashlib
import json
import math
from pathlib import Path
import re

PENDING = {"not executed", "attempt interrupted before completion"}


def successful(answer):
    return bool(answer["correct"] and answer["judged"] and not answer.get("error") and not answer.get("memory_write_error"))


def percentile(values):
    values = sorted(value for value in values if value > 0)
    return values[math.ceil(len(values) * 0.95) - 1] if values else None


def source_json(entry, lines=False):
    raw = Path(entry["path"]).read_bytes()
    if hashlib.sha256(raw).hexdigest() != entry["sha256"]:
        raise ValueError("Input provenance mismatch")
    return [json.loads(line) for line in raw.splitlines() if line.strip()] if lines else json.loads(raw)


def expected_jobs(plan):
    selected = set(plan["longmemeval"]["question_ids"]) if plan.get("longmemeval") else set()
    references = {row["question_id"]: row for row in source_json(plan["longmemeval"]) if row["question_id"] in selected} if selected else {}
    if len(references) != len(selected):
        raise ValueError("Missing or repeated LongMemEval selection")
    expected = {("longmemeval_s", key, mode): 1 for key in selected for mode in ["memory", "none"]}
    for entry in plan["arena"]:
        for task in source_json(entry, lines=True):
            for mode in ["memory", "none"]:
                key = (entry["name"], f"{entry['name']}-{task['id']}", mode)
                if key in expected:
                    raise ValueError("Duplicate arena task")
                expected[key] = len(task["questions"])
    return expected, references


def official_sessions(jobs, references):
    rows = [job for job in jobs if job["dataset"] == "longmemeval_s" and job["mode"] == "memory" and not job["id"].endswith("_abs")]
    measured = 0
    counts = collections.Counter()
    admitted = 0.0
    admitted_all = 0
    for job in rows:
        answer = job["answers"][0]
        if not answer.get("search"):
            continue
        reference = references[job["id"]]
        gold = set(map(str, reference["answer_session_ids"]))
        if not gold:
            raise ValueError("Answerable question has no evidence sessions")

        def session_id(document_id):
            match = re.search(r"-(\d+)$", document_id)
            if not match:
                raise ValueError("Session record has no source occurrence")
            index = int(match[1]) - 1
            if index < 0 or index >= len(reference["haystack_session_ids"]):
                raise ValueError("Session occurrence outside original history")
            return str(reference["haystack_session_ids"][index])

        ranked = [session_id(item["document"]["id"]) for item in answer["search"]["results"]]
        measured += 1
        for k in [1, 3, 5, 10]:
            recalled = set(ranked[:k])
            counts[f"recall_any_at_{k}"] += bool(gold & recalled)
            counts[f"recall_all_at_{k}"] += gold <= recalled
        admitted_ids = {session_id(value) for value in answer["source_ids"] or []}
        admitted += len(gold & admitted_ids) / len(gold)
        admitted_all += gold <= admitted_ids
    denominator = len(rows)
    return {
        "expected_answerable": denominator,
        "measured": measured,
        **({key: value / denominator for key, value in counts.items()} if denominator else {}),
        "admitted_session_recall": admitted / denominator if denominator else None,
        "admitted_recall_all": admitted_all / denominator if denominator else None,
        "ndcg_note": "Repository-standard nDCG is separate from upstream ndcg_any's discount variant.",
    }


def summarize(jobs):
    grouped = collections.defaultdict(list)
    failures = []
    for job in jobs:
        grouped[(job["dataset"], job["mode"])].append(job)
    output = {}
    for (dataset, mode), group in sorted(grouped.items()):
        answers = [answer for job in group for answer in job["answers"]]
        current = [answer for job in group if not job.get("reused_baseline") for answer in job["answers"]]
        completed = [answer for answer in answers if answer.get("error", "") not in PENDING]
        current_completed = [answer for answer in current if answer.get("error", "") not in PENDING]
        errors = [answer for answer in completed if answer.get("error") or answer.get("memory_write_error")]
        result = {
            "expected_tasks": len(group), "expected_answers": len(answers),
            "completed_cases": len(completed), "judged": sum(answer["judged"] for answer in answers),
            "unexecuted_cases": sum(answer.get("error", "").startswith("not executed") for answer in answers),
            "interrupted_cases": sum(answer.get("error", "") == "attempt interrupted before completion" or answer.get("error", "").startswith("execution interrupted before checkpoint") for answer in answers),
            "correct": sum(successful(answer) for answer in answers), "execution_errors": len(errors),
            "accuracy_over_expected": sum(successful(answer) for answer in answers) / len(answers),
            "reused_baseline_answers": len(answers) - len(current),
            "reader_input_tokens": sum(answer["reader_usage"]["input_tokens"] for answer in current),
            "reader_output_tokens": sum(answer["reader_usage"]["output_tokens"] for answer in current),
            "judge_input_tokens": sum(answer["judge_usage"]["input_tokens"] for answer in current),
            "judge_output_tokens": sum(answer["judge_usage"]["output_tokens"] for answer in current),
            "reader_p95_millis": percentile([answer["read_millis"] for answer in current_completed]),
            "search_p95_millis_including_queue": percentile([answer["search_millis"] for answer in completed]),
            "omitted_context_records": sum(answer["omitted_context_records"] for answer in answers),
            "rerank_statuses": dict(collections.Counter(answer["rerank"]["status"] for answer in completed if answer["rerank"]["status"])),
        }
        if dataset.startswith("formal_reasoning_"):
            result.update(
                final_subtask_success=sum(successful(job["answers"][-1]) for job in group),
                all_subtasks_success=sum(all(successful(answer) for answer in job["answers"]) for job in group),
                macro_progress=sum(sum(successful(answer) for answer in job["answers"]) / len(job["answers"]) for job in group) / len(group),
                per_step=[{
                    "step": i + 1,
                    "expected": sum(len(job["answers"]) > i for job in group),
                    "correct": sum(successful(job["answers"][i]) for job in group if len(job["answers"]) > i),
                } for i in range(max(len(job["answers"]) for job in group))],
            )
        else:
            types = collections.defaultdict(list)
            for job in group:
                types["abstention" if job["id"].endswith("_abs") else job["question_type"]].extend(job["answers"])
            result["by_type"] = {key: {
                "expected": len(values), "correct": sum(successful(value) for value in values),
                "judged": sum(value["judged"] for value in values),
            } for key, values in types.items()}
        output.setdefault(dataset, {})[mode] = result
        for job in group:
            for answer in job["answers"]:
                if not successful(answer):
                    failures.append({"dataset": dataset, "mode": mode, "id": job["id"], "step": answer["index"], "question": answer["question"], "reference": answer["reference"], "answer": answer["answer"], "error": answer.get("error"), "raw_judge": answer["raw_judge"], "context_sha256": answer["context_sha256"]})
    return output, failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, type=Path)
    args = parser.parse_args()
    run = args.run.resolve()
    manifest = json.loads((run / "manifest.json").read_text())
    expected, references = expected_jobs(manifest["plan"])
    jobs = [json.loads(path.read_text()) for pattern in ["*-memory.json", "*-none.json"] for path in run.glob(pattern)]
    actual = {(job["dataset"], job["id"], job["mode"]): len(job["answers"]) for job in jobs}
    if actual != expected or len(actual) != len(jobs):
        raise ValueError("Expected case files missing, duplicated or changed; refusing a reduced denominator")
    datasets, failures = summarize(jobs)
    if "longmemeval_s" in datasets:
        datasets["longmemeval_s"]["memory"]["official_session_retrieval"] = official_sessions(jobs, references)
    paired = {}
    lookup = {(job["dataset"], job["id"], job["mode"]): job for job in jobs}
    for job in jobs:
        if job["mode"] != "memory":
            continue
        other = lookup[(job["dataset"], job["id"], "none")]
        counts = paired.setdefault(job["dataset"], collections.Counter())
        for memory, none in zip(job["answers"], other["answers"]):
            if memory.get("error", "") in PENDING or none.get("error", "") in PENDING:
                continue
            counts["completed_pairs"] += 1
            counts["memory_gains"] += successful(memory) and not successful(none)
            counts["memory_regressions"] += successful(none) and not successful(memory)
            if memory["context_sha256"] and memory["context_sha256"] == none["context_sha256"]:
                counts["identical_context_pairs"] += 1
                counts["identical_context_grade_disagreements"] += successful(memory) != successful(none)
            elif job["dataset"].startswith("formal_reasoning_") and memory["context"]:
                counts["with_history_pairs"] += 1
                counts["with_history_gains"] += successful(memory) and not successful(none)
                counts["with_history_regressions"] += successful(none) and not successful(memory)
    completion_path = run / "completion.json"
    completed = completion_path.exists() and not json.loads(completion_path.read_text()).get("cancelled", True)
    report = {"scope": manifest["scope"], "expected_answers": sum(expected.values()), "datasets": datasets, "paired": {key: dict(value) for key, value in paired.items()}, "pipeline_completed": completed, "interpretation": "Reference agreement, raw-memory retrieval and configured agent only; not worker ingestion, independent factual calibration or published baseline comparability."}
    out = run / "reports"
    out.mkdir(mode=0o700, exist_ok=True)
    for name, value in [("summary.json", report), ("failures.json", failures)]:
        path = out / name
        path.write_text(json.dumps(value, indent=2) + "\n")
        path.chmod(0o600)
    for dataset, modes in datasets.items():
        for mode, result in modes.items():
            print(f"{dataset} {mode}: {result['correct']}/{result['expected_answers']}, judged {result['judged']}, errors {result['execution_errors']}")
    print(f"Reports: {out}")


if __name__ == "__main__":
    main()
