import importlib.util
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("external_summary", Path(__file__).with_name("summarize-memory-external.py"))
SUMMARY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUMMARY)


def answer(correct, error=""):
    return {
        "correct": correct, "judged": not error, "error": error,
        "reader_usage": {"input_tokens": 1, "output_tokens": 1},
        "judge_usage": {"input_tokens": 1, "output_tokens": 1},
        "read_millis": 1, "search_millis": 0, "omitted_context_records": 0,
        "rerank": {"status": ""}, "index": 0, "question": "synthetic",
        "reference": "synthetic", "answer": "synthetic", "raw_judge": "",
        "context_sha256": "synthetic",
    }


class ExternalSummaryTests(unittest.TestCase):
    def test_reused_model_control_does_not_spend_current_tokens(self):
        job={"dataset":"formal_reasoning_math","id":"old","mode":"none","answers":[answer(False,"reader: budget exhausted")],"reused_baseline":{"result":{"sha256":"pinned"}}}
        output, failures=SUMMARY.summarize([job])
        result=output["formal_reasoning_math"]["none"]
        self.assertEqual(result["reused_baseline_answers"],1)
        self.assertEqual(result["reader_input_tokens"],0)
        self.assertEqual(result["judge_input_tokens"],0)
        self.assertIsNone(result["reader_p95_millis"])
        self.assertEqual(result["execution_errors"],1)
        self.assertEqual(len(failures),1)

    def test_prepare_requires_explicit_control_policy_before_downloading(self):
        with tempfile.TemporaryDirectory() as temporary:
            out=Path(temporary)/"new"
            script=Path(__file__).with_name("prepare-memory-external.py")
            result=subprocess.run([sys.executable,str(script),"--tracks","memoryarena","--output",str(out)],capture_output=True,text=True)
            self.assertNotEqual(result.returncode,0)
            self.assertIn("--arena-baseline-run",result.stderr)
            self.assertFalse(out.exists())

    def test_seal_keeps_all_cases_and_refuses_resealing(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            data = root / "source.json"
            data.write_text(json.dumps([{"question_id": "fixture"}]))
            plan = {"longmemeval": {"path": str(data), "sha256": hashlib.sha256(data.read_bytes()).hexdigest(), "question_ids": ["fixture"]}, "arena": []}
            (root / "manifest.json").write_text(json.dumps({"plan": plan}))
            for mode in ["memory", "none"]:
                (root / f"fixture-{mode}.json").write_text(json.dumps({"dataset": "longmemeval_s", "id": "fixture", "mode": mode, "answers": [answer(False, "not executed")]}))
            script = Path(__file__).with_name("seal-memory-external.py")
            args = [sys.executable, str(script), "--run", str(root), "--process-exit-code", "2"]
            result = subprocess.run(args, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            seal = json.loads((root / "sealed.json").read_text())
            self.assertEqual(len(seal["files"]), 2)
            self.assertEqual(seal["process_exit_code"], 2)
            self.assertEqual(subprocess.run(args, capture_output=True).returncode, 1)
            (root / "fixture-none.json").unlink()
            (root / "sealed.json").unlink()
            self.assertNotEqual(subprocess.run(args, capture_output=True).returncode, 0)

    def test_arena_only_plan_retains_both_complete_conditions(self):
        with tempfile.TemporaryDirectory() as temporary:
            data = Path(temporary) / "arena.jsonl"
            data.write_text(json.dumps({"id": 3, "questions": ["a", "b"]}) + "\n")
            plan = {"arena": [{"name": "formal_reasoning_math", "path": str(data), "sha256": hashlib.sha256(data.read_bytes()).hexdigest()}]}
            expected, references = SUMMARY.expected_jobs(plan)
            self.assertEqual(references, {})
            self.assertEqual(sum(expected.values()), 4)
            self.assertEqual({key[-1] for key in expected}, {"memory", "none"})

    def test_macro_micro_final_and_error_denominators_differ(self):
        jobs = [
            {"dataset": "formal_reasoning_math", "id": "one", "mode": "memory", "answers": [answer(True), answer(True)]},
            {"dataset": "formal_reasoning_math", "id": "two", "mode": "memory", "answers": [answer(True), answer(False), answer(False, "reader: budget exhausted")]},
        ]
        output, failures = SUMMARY.summarize(jobs)
        result = output["formal_reasoning_math"]["memory"]
        self.assertEqual(result["expected_answers"], 5)
        self.assertEqual(result["judged"], 4)
        self.assertEqual(result["execution_errors"], 1)
        self.assertEqual(result["accuracy_over_expected"], 3 / 5)
        self.assertAlmostEqual(result["macro_progress"], (1 + 1 / 3) / 2)
        self.assertEqual(result["final_subtask_success"], 1)
        self.assertEqual(result["all_subtasks_success"], 1)
        self.assertEqual(len(failures), 2)

    def test_official_session_recall_deduplicates_source_identity(self):
        references = {"q": {"answer_session_ids": ["A", "B"], "haystack_session_ids": ["A", "A", "B"]}}
        row = {"search": {"results": [{"document": {"id": f"record-{i:03d}"}} for i in [1, 2, 3]]}, "source_ids": ["record-002", "record-003"]}
        jobs = [{"dataset": "longmemeval_s", "mode": "memory", "id": "q", "answers": [row]}]
        metrics = SUMMARY.official_sessions(jobs, references)
        self.assertEqual(metrics["recall_any_at_1"], 1)
        self.assertEqual(metrics["recall_all_at_1"], 0)
        self.assertEqual(metrics["recall_all_at_3"], 1)
        self.assertEqual(metrics["admitted_session_recall"], 1)
        self.assertEqual(metrics["admitted_recall_all"], 1)
        row["source_ids"] = ["record-000"]
        with self.assertRaises(ValueError):
            SUMMARY.official_sessions(jobs, references)


if __name__ == "__main__":
    unittest.main()
