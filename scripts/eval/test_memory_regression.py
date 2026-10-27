import importlib.util
from pathlib import Path
import unittest

SPEC=importlib.util.spec_from_file_location("regression_summary",Path(__file__).with_name("summarize-memory-regression.py"))
SUMMARY=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUMMARY)


def row(group,index,correct,error="",write_error=""):
    return {"dataset":"formal_reasoning_math","mode":"combined","group":group,"prior_status":"wrong_answer","answer":{"index":index,"correct":correct,"judged":not bool(error),"error":error,"memory_write_error":write_error,"read_millis":1,"search_millis":1,"omitted_context_records":0,"rerank":{},"reader_usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"judge_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}


class RegressionSummaryTests(unittest.TestCase):
    def test_complete_chain_denominators_are_fixed(self):
        plan={"selection":{"locomo":[{"id":"c.1","sample_id":"c"}],"longmemeval":[{"id":"q"}],"arena":[{"id":"formal_reasoning_math-2","dataset":"formal_reasoning_math","steps":2}]},"modes":["none","raw","derived","combined"],"expected_answers":16}
        expected=SUMMARY.expected_cases(plan)
        self.assertEqual(len(expected),16)
        self.assertEqual(sum(key[0]=="formal_reasoning_math" for key in expected.values()),8)
        plan["expected_answers"]=12
        with self.assertRaises(ValueError):SUMMARY.expected_cases(plan)

    def test_macro_progress_and_final_success_are_not_micro_accuracy(self):
        rows=[row("a",i,True) for i in range(3)]+[row("b",0,False,"reader: budget exhausted")]
        result=SUMMARY.summarize(rows)["formal_reasoning_math"]["combined"]
        self.assertEqual(result["accuracy"],.75)
        self.assertEqual(result["macro_progress"],.5)
        self.assertEqual(result["final_subtask_success"],1)
        self.assertEqual(result["execution_errors"],1)
        self.assertEqual(result["expected"],4)

    def test_reused_baseline_usage_is_not_current_spending(self):
        reused=row("a",0,False,"reader: output budget exhausted")
        reused["mode"]="none"
        reused["reused_baseline"]={"result":{"sha256":"pinned"}}
        result=SUMMARY.summarize([reused])["formal_reasoning_math"]["none"]
        self.assertEqual(result["reused_baseline_answers"],1)
        self.assertEqual(result["reader_tokens"]["total_tokens"],0)
        self.assertEqual(result["judge_tokens"]["total_tokens"],0)
        self.assertEqual(result["execution_errors"],1)
        self.assertEqual(result["expected"],1)

    def test_write_failure_is_distinct_from_answer_correctness(self):
        result=SUMMARY.summarize([row("a",0,True,write_error="failed commit"),row("b",0,False,"not executed")])["formal_reasoning_math"]["combined"]
        self.assertEqual(result["correct"],0)
        self.assertEqual(result["qa_correct_before_memory_write"],1)
        self.assertEqual(result["memory_write_errors"],1)
        self.assertEqual(result["completed"],1)
        self.assertEqual(result["expected"],2)


if __name__=="__main__":unittest.main()
