import datetime as dt
import importlib.util
import random
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location("extend", Path(__file__).with_name("extend-daily-chat.py"))
EXTEND = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(EXTEND)


def sample():
    return {"id": "s", "scene": "assistant_1on1", "owner_speaker": "A", "language": "en", "sessions": [
        {"id": "session_1", "at": "2026-03-01T20:00:00+08:00", "turns": [{"id": "D1:1", "role": "user", "speaker": "A", "text": "x"}]},
        {"id": "session_2", "at": "2026-03-20T20:00:00+08:00", "turns": [{"id": "D2:1", "role": "user", "speaker": "A", "text": "y"}, {"id": "D2:2", "role": "assistant", "speaker": "Assistant", "text": "z"}]},
        {"id": "session_3", "at": "2026-03-22T09:00:00+08:00", "turns": [{"id": "D3:1", "role": "user", "speaker": "A", "text": "w"}]}],
        "questions": [{"id": "q1", "evidence": ["D2:1", "D3:1"]}]}


class ExtendTests(unittest.TestCase):
    def test_timestamps_fall_strictly_between_sessions_on_free_days(self):
        s = sample()
        plan = EXTEND.plan_timestamps(s, 10, random.Random(1))
        days = {dt.datetime.fromisoformat(x["at"]).date() for x in s["sessions"]}
        self.assertEqual(plan, sorted(plan, key=dt.datetime.fromisoformat))
        self.assertTrue(plan)
        for at in plan:
            t = dt.datetime.fromisoformat(at)
            self.assertNotIn(t.date(), days)
            self.assertTrue(dt.datetime.fromisoformat(s["sessions"][0]["at"]) < t < dt.datetime.fromisoformat(s["sessions"][-1]["at"]))
            self.assertEqual(t.utcoffset(), dt.timedelta(hours=8))
        self.assertEqual(plan, EXTEND.plan_timestamps(s, 10, random.Random(1)))

    def test_assemble_renumbers_chronologically_and_remaps_evidence(self):
        added = [{"at": "2026-03-10T21:00:00+08:00", "turns": [{"role": "user", "speaker": "A", "text": "new"}]}]
        out = EXTEND.assemble(sample(), added)
        self.assertEqual([s["id"] for s in out["sessions"]], ["session_1", "session_2", "session_3", "session_4"])
        self.assertEqual(out["sessions"][1]["turns"][0]["text"], "new")
        self.assertEqual(out["questions"][0]["evidence"], ["D3:1", "D4:1"])

    def test_generated_sessions_are_validated(self):
        s = sample()
        ok = [{"at": "t", "turns": [{"role": "user", "speaker": "A", "text": "hi"}, {"role": "assistant", "speaker": "Assistant", "text": "hey"}]}]
        self.assertEqual(len(EXTEND.validate_sessions(s, ok, ["t"])), 1)
        for bad in ([{"at": "t", "turns": [{"role": "assistant", "speaker": "Assistant", "text": "a"}, {"role": "user", "speaker": "A", "text": "b"}]}],
                    [{"at": "t", "turns": [{"role": "user", "speaker": "B", "text": "a"}, {"role": "user", "speaker": "A", "text": "b"}]}],
                    [{"at": "u", "turns": ok[0]["turns"]}],
                    [{"at": "t", "turns": [{"role": "user", "speaker": "A", "text": "[Speaker: A] x"}, {"role": "user", "speaker": "A", "text": "b"}]}]):
            with self.assertRaises(ValueError):
                EXTEND.validate_sessions(s, bad, ["t"])


if __name__ == "__main__":
    unittest.main()
