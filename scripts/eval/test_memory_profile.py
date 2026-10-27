import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("memory_profile", Path(__file__).with_name("summarize-memory-profile.py"))
PROFILE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROFILE)


def row(variant, correct, error=""):
    return {"dataset":"locomo", "question_id":"q", "variant":variant, "mode":"combined", "qa":True,
        "answer":{"correct":correct, "judged":not bool(error), "error":error, "search_millis":1, "read_millis":1, "rerank":{}, "context":"same", "context_sha256":"same"}}


class ProfileTests(unittest.TestCase):
    def test_execution_failure_stays_in_quality_denominator(self):
        metrics, pairs = PROFILE.summarize([row("4b-50", True), row("4b-20", False, "reader: exhausted budget")])
        failed = metrics["locomo"]["4b-20"]["combined"]
        self.assertEqual(failed["expected_qa"], 1)
        self.assertEqual(failed["accuracy"], 0)
        self.assertEqual(failed["execution_errors"], 1)
        self.assertEqual(pairs["locomo"]["4b-20:combined"]["regressions"], 1)

    def test_arena_does_not_enter_static_profile(self):
        plan = {"selection":{"locomo":[{"id":"q", "sample_id":"c"}], "longmemeval":[], "arena":[{"id":"arena", "steps":10}]}}
        profile = {"variants":[{"name":"4b-50"}], "modes":["raw","derived","combined"]}
        cases = PROFILE.expected_cases(plan, profile)
        self.assertEqual(len(cases), 3)
        self.assertTrue(all(value[0] == "locomo" for value in cases.values()))


if __name__ == "__main__":
    unittest.main()
