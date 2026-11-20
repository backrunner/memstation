#!/usr/bin/env python3
"""Lengthen a daily-chat dataset with model-written distractor sessions.

python3 -I scripts/eval/extend-daily-chat.py --input v1.json --output v2.json --work DIR \
    --endpoint http://127.0.0.1:PORT/ROLE/v1 --model MODEL [--api-key-env VAR]

Distractor sessions are ordinary chat between the original sessions: the same people,
places and kinds of activity, with new unrelated small events. A separate checker call
sees every question with its reference answer and rejects any added session that would
change, contradict, extend or newly answer one; rejected sessions are rewritten once and
dropped if still rejected. Questions, references and asked_at times are unchanged except
for the explicitly listed reference revisions. Sessions and turns are renumbered in
chronological order so identifiers carry no hint of which turns are original.

Every model reply is checkpointed under --work; rerunning resumes and never repeats a
completed call. The script never writes a partial output file.
"""
import argparse
import concurrent.futures
import datetime as dt
import hashlib
import json
import os
import pathlib
import random
import re
import urllib.request

SCHEMA = "memstation-daily-chat-v1"
MAX_TURN_CHARS = 400

GENERATE = """You write synthetic, fictional chat history for a memory benchmark. Return only JSON.

Write {count} NEW chat sessions for the conversation described below, one at each timestamp in "timestamps", in that order. They sit between the existing sessions of the same history, so they must be consistent with everything that happened before and after them.

Purpose: these sessions are distractors. A memory system will later be asked the listed questions, and its retrieval must find the original evidence among many ordinary sessions. Make retrieval genuinely hard but keep every reference answer exactly true:
- Reuse the same people, nicknames, places and kinds of activity (meals, trips, sports, work, family, pets, health, plans) in NEW small events: other outings with different companions, other dates, other details, recurring hobbies, everyday complaints and chit-chat.
- Use natural relative time words ("yesterday", "last night", "this weekend", "上周", "昨日", "어제") consistent with each session's own timestamp.
- NEVER create, repeat, contradict, extend or make ambiguous anything a question asks about: no second occurrence of an asked event with the same people, no new value for an asked attribute (residence, schedule, diet, job, names, counts, dates), no extra instance that would change an asked count, no information that answers an unanswerable question, no new person sharing an asked person's name or nickname.
- Do not mention the questions, answers, tests or memory. No placeholders.

Format: {{"sessions":[{{"at":"<timestamp exactly as given>","turns":[{{"role":"user"|"assistant","speaker":"<name>","text":"<message>"}}]}}]}}
Scene rules: {scene_rules}
Language: {language_rule}
Each session has {min_turns}-{max_turns} turns; each message is at most 200 characters and sounds like real casual chat.

Conversation:
{context}"""

CHECK = """You audit synthetic distractor chat sessions for a memory benchmark. Return only JSON.

Below are questions with reference answers that must remain exactly correct for the WHOLE history, and a list of ADDED sessions (labelled X1, X2, ...). Flag an added session if any of its content would: change or contradict a reference answer; add another valid answer, item or occurrence (for example a second time the same people did the asked activity, another visit that changes a count, a new value for a person's residence, schedule, job, diet or name); answer a question whose reference says the information is not recorded; or make a question ambiguous (another person with the same name or nickname, another event matching the question's description). Ordinary unrelated content is fine. Judge strictly but only on real conflicts.

Return {{"conflicts":[{{"session":"X<n>","question":"<question id>","reason":"<short reason>"}}]}} with an empty list when there are none.

Questions:
{questions}

Added sessions:
{sessions}"""


def sha(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def offset(at):
    return dt.datetime.fromisoformat(at).utcoffset()


def plan_timestamps(sample, total, rng):
    """Distribute timestamps across gaps between original sessions, proportional to gap
    length, on calendar days that have no original session, at plausible local hours."""
    starts = [dt.datetime.fromisoformat(s["at"]) for s in sample["sessions"]]
    gaps = [(a, b) for a, b in zip(starts, starts[1:])]
    taken = {s.date() for s in starts}
    lengths = [max((b - a).days - 1, 0) for a, b in gaps]
    weights = sum(lengths)
    plan = []
    for (a, b), days in zip(gaps, lengths):
        if days <= 0:
            continue
        want = max(1, round(total * days / weights))
        candidates = [a + dt.timedelta(days=d) for d in range(1, days + 1)]
        candidates = [c for c in candidates if c.date() not in taken and c.date() < b.date()]
        chosen = sorted(rng.sample(candidates, min(want, len(candidates))))
        for day in chosen:
            hour, minute = rng.choice([8, 12, 13, 19, 20, 21, 22, 23]), rng.choice([0, 5, 10, 20, 30, 40, 45, 50])
            at = day.replace(hour=hour, minute=minute, second=0, microsecond=0)
            plan.append(at.isoformat())
            taken.add(day.date())
    return sorted(plan, key=dt.datetime.fromisoformat)


def render_context(sample, questions=True):
    lines = [f"Scene: {sample['scene']}; owner: {sample.get('owner_speaker') or 'none (group chat)'}"]
    for s in sample["sessions"]:
        lines.append(f"\n[{s['at']}]")
        for t in s["turns"]:
            lines.append(f"{t['speaker']}: {t['text']}")
    if questions:
        lines.append("\nQuestions that must keep their reference answers (asked after the whole history):")
        for q in sample["questions"]:
            lines.append(f"- {q['query']} -> {q['answer']}")
    return "\n".join(lines)


def speakers(sample):
    return sorted({t["speaker"] for s in sample["sessions"] for t in s["turns"]})


def scene_rules(sample):
    if sample["scene"] == "assistant_1on1":
        return (f'one-on-one chat: role "user" with speaker "{sample["owner_speaker"]}" and role "assistant" with '
                'speaker "Assistant", starting with the user and mostly alternating. The assistant may suggest things; '
                'the user decides.')
    return ('group chat: every turn has role "user"; speakers only from ' + json.dumps(speakers(sample), ensure_ascii=False)
            + '; not everyone speaks in every session.')


def language_rule(sample):
    if sample["language"] == "mixed":
        return "mix " + " and ".join(sample["source_languages"]) + " the way these speakers already do (code-switching)."
    return {"zh": "Simplified Chinese", "en": "English", "ja": "Japanese", "ko": "Korean"}[sample["language"]] + " only."


def call(endpoint, model, key, system, payload, max_tokens, temperature):
    body = json.dumps({"model": model, "messages": [{"role": "system", "content": system}, {"role": "user", "content": payload}],
                       "max_tokens": max_tokens, "temperature": temperature, "reasoning_effort": "low", "stream": False,
                       "response_format": {"type": "json_object"}}).encode()
    request = urllib.request.Request(endpoint.rstrip("/") + "/chat/completions", data=body,
                                     headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"})
    with urllib.request.urlopen(request, timeout=900) as response:
        reply = json.loads(response.read())
    choice = reply["choices"][0]
    if choice.get("finish_reason") not in (None, "stop"):
        raise ValueError("incomplete model reply: " + str(choice.get("finish_reason")))
    text = choice["message"]["content"].strip()
    text = re.sub(r"^```(?:json)?\s*|\s*```$", "", text)
    return json.loads(text), reply.get("usage", {})


def checkpointed(work, name, produce):
    path = work / (name + ".json")
    if path.exists():
        return json.loads(path.read_text())
    result = produce()
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(result, ensure_ascii=False, indent=1))
    tmp.replace(path)
    return result


def validate_sessions(sample, sessions, timestamps):
    if [s.get("at") for s in sessions] != timestamps:
        raise ValueError("timestamps not returned exactly")
    allowed = set(speakers(sample))
    for s in sessions:
        turns = s.get("turns") or []
        if not 2 <= len(turns) <= 12:
            raise ValueError("session turn count out of range")
        for t in turns:
            text = (t.get("text") or "").strip()
            if not text or len(text) > MAX_TURN_CHARS or "[Conversation" in text or "[Speaker" in text:
                raise ValueError("invalid turn text")
            if sample["scene"] == "assistant_1on1":
                if (t.get("role"), t.get("speaker")) not in (("user", sample["owner_speaker"]), ("assistant", "Assistant")):
                    raise ValueError("invalid 1:1 role/speaker")
            elif t.get("role") != "user" or t.get("speaker") not in allowed:
                raise ValueError("invalid group speaker")
        if sample["scene"] == "assistant_1on1" and turns[0]["role"] != "user":
            raise ValueError("1:1 session must start with the user")
    return [{"at": s["at"], "turns": [{"role": t["role"], "speaker": t["speaker"], "text": t["text"].strip()} for t in s["turns"]]} for s in sessions]


def generate(args, sample, chunk, timestamps, note=""):
    context = render_context(sample)
    if note:
        context += "\n\nA previous draft of these sessions was rejected for conflicting with the questions: " + note
    system = GENERATE.format(count=len(timestamps), scene_rules=scene_rules(sample), language_rule=language_rule(sample),
                             min_turns=3, max_turns=8, context=context)
    payload = json.dumps({"timestamps": timestamps}, ensure_ascii=False)
    last = None
    for attempt in range(2):  # one schema retry on the same input; both attempts are checkpointed
        def produce():
            try:
                reply, usage = call(args.endpoint, args.model, args.key, system, payload, 16384, 0.8)
                return {"reply": reply, "usage": usage}
            except Exception as exc:  # recorded, never silently retried beyond this loop
                return {"error": str(exc)[:500]}
        record = checkpointed(args.work, f"gen-{sample['id']}-{chunk}-a{attempt}", produce)
        if "error" in record:
            last = record["error"]
            continue
        try:
            return validate_sessions(sample, record["reply"].get("sessions", []), timestamps)
        except (ValueError, KeyError, TypeError) as exc:
            last = str(exc)
    raise RuntimeError(f"{sample['id']} {chunk}: {last}")


def check(args, sample, added, label):
    questions = "\n".join(f"{q['id']} (asked {q['asked_at']}): {q['query']} -> reference: {q['answer']}" for q in sample["questions"])
    blocks = []
    for i, s in enumerate(added, 1):
        blocks.append(f"X{i} [{s['at']}]\n" + "\n".join(f"{t['speaker']}: {t['text']}" for t in s["turns"]))
    system = CHECK.format(questions=questions, sessions="\n\n".join(blocks))

    def produce():
        reply, usage = call(args.endpoint, args.model, args.key, system, "Audit the added sessions.", 32768, 0)
        return {"reply": reply, "usage": usage}
    record = checkpointed(args.work, f"check-{sample['id']}-{label}", produce)
    flagged = {}
    for c in record["reply"].get("conflicts", []):
        match = re.fullmatch(r"X(\d+)", str(c.get("session", "")).strip())
        if match and 1 <= int(match.group(1)) <= len(added):
            flagged.setdefault(int(match.group(1)) - 1, []).append(f"{c.get('question')}: {c.get('reason')}")
    return flagged


def extend_sample(args, sample, rng):
    timestamps = plan_timestamps(sample, args.sessions_per_sample, rng)
    chunks = [timestamps[i:i + args.chunk] for i in range(0, len(timestamps), args.chunk)]
    added = []
    for n, chunk in enumerate(chunks):
        added += generate(args, sample, f"c{n}", chunk)
    flagged = check(args, sample, added, "first")
    log = {"planned": len(timestamps), "flagged_first": len(flagged), "rewritten": 0, "dropped": 0, "conflicts": {}}
    for index, reasons in sorted(flagged.items()):
        log["conflicts"][added[index]["at"]] = reasons
        try:
            rewritten = generate(args, sample, f"r{index}", [added[index]["at"]], note="; ".join(reasons))
        except RuntimeError:
            added[index] = None
            log["dropped"] += 1
            continue
        if check(args, sample, rewritten, f"r{index}"):
            added[index] = None
            log["dropped"] += 1
        else:
            added[index] = rewritten[0]
            log["rewritten"] += 1
    return [s for s in added if s], log


def assemble(sample, added):
    sessions = [dict(s, origin="original") for s in sample["sessions"]] + [dict(s, origin="added") for s in added]
    sessions.sort(key=lambda s: dt.datetime.fromisoformat(s["at"]))
    mapping, out = {}, []
    for n, s in enumerate(sessions, 1):
        turns = []
        for k, t in enumerate(s["turns"], 1):
            new = f"D{n}:{k}"
            if s["origin"] == "original":
                mapping[t["id"]] = new
            turns.append({"id": new, "role": t["role"], "speaker": t["speaker"], "text": t["text"]})
        out.append({"id": f"session_{n}", "at": s["at"], "turns": turns})
    result = {k: v for k, v in sample.items() if k not in ("sessions", "questions")}
    result["sessions"] = out
    result["questions"] = [dict(q, evidence=[mapping[e] for e in q["evidence"]]) for q in sample["questions"]]
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--work", required=True)
    parser.add_argument("--revisions", help="JSON list of {sample, question, field, value, reason} applied before generation")
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--api-key-env", default="ANSWERER_API_KEY")
    parser.add_argument("--sessions-per-sample", type=int, default=36)
    parser.add_argument("--chunk", type=int, default=3)
    parser.add_argument("--seed", type=int, default=20261120)
    parser.add_argument("--workers", type=int, default=4)
    args = parser.parse_args()
    args.key = os.environ.get(args.api_key_env, "")
    args.work = pathlib.Path(args.work)
    args.work.mkdir(parents=True, exist_ok=True)
    source = json.loads(pathlib.Path(args.input).read_text())
    if source.get("schema") != SCHEMA:
        raise SystemExit("unsupported schema")
    revisions = json.loads(pathlib.Path(args.revisions).read_text()) if args.revisions else []
    for r in revisions:
        sample = next(s for s in source["samples"] if s["id"] == r["sample"])
        question = next(q for q in sample["questions"] if q["id"] == r["question"])
        if r["field"] not in ("query", "answer"):
            raise SystemExit("only query/answer revisions are allowed")
        question[r["field"]] = r["value"]
    results = {}
    with concurrent.futures.ThreadPoolExecutor(args.workers) as pool:
        jobs = {pool.submit(extend_sample, args, s, random.Random(f"{args.seed}:{s['id']}")): s for s in source["samples"]}
        for job in concurrent.futures.as_completed(jobs):
            results[jobs[job]["id"]] = job.result()
    samples = [assemble(s, results[s["id"]][0]) for s in source["samples"]]
    report = {s["id"]: results[s["id"]][1] for s in source["samples"]}
    out = dict(source, samples=samples, name=source["name"].replace("-v1", "-v2"),
               description=source["description"] + " Version 2 inserts model-written distractor sessions between the original "
               "sessions (same people, places and activity types; audited against every question) and renumbers sessions and turns.",
               provenance={"source_sha256": sha(args.input), "generator_model": args.model, "seed": args.seed,
                           "sessions_per_sample": args.sessions_per_sample, "reference_revisions": revisions, "audit": report})
    tmp = pathlib.Path(args.output + ".tmp")
    tmp.write_text(json.dumps(out, ensure_ascii=False, indent=1))
    tmp.replace(args.output)
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
