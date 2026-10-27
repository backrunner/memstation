#!/usr/bin/env python3
"""Summarize the fixed diagnostic denominator, workers, latency and failures."""
import argparse
import collections
import hashlib
import json
import math
from pathlib import Path

PENDING={"not executed","attempt interrupted before completion"}

def successful(row):
    a=row["answer"]
    return bool(a["judged"] and a["correct"] and not a.get("error") and not a.get("memory_write_error"))

def percentile(values):
    values=sorted(x for x in values if x>0)
    return values[math.ceil(len(values)*0.95)-1] if values else None

def expected_cases(plan):
    cases={}
    for kind in ["locomo","longmemeval"]:
        for row in plan["selection"][kind]:
            group="locomo-"+row["sample_id"] if kind=="locomo" else "longmemeval-"+row["id"]
            dataset="locomo" if kind=="locomo" else "longmemeval_s"
            for mode in plan["modes"]:
                key=hashlib.sha256(f"{group}\0{row['id']}\0{mode}".encode()).hexdigest()+".json"
                cases[key]=(dataset,group,row["id"],mode)
    for row in plan["selection"]["arena"]:
        for i in range(row["steps"]):
            question=f"{row['id']}-step-{i+1}"
            for mode in plan["modes"]:
                key=hashlib.sha256(f"{row['id']}\0{question}\0{mode}".encode()).hexdigest()+".json"
                cases[key]=(row["dataset"],row["id"],question,mode)
    if len(cases)!=plan["expected_answers"]:
        raise ValueError("Selection changed the expected denominator")
    return cases

def summarize(rows):
    groups=collections.defaultdict(list)
    for row in rows:groups[(row["dataset"],row["mode"])].append(row)
    result={}
    for (dataset,mode),group in sorted(groups.items()):
        completed=[r for r in group if r["answer"].get("error","") not in PENDING]
        errors=collections.Counter(r["answer"]["error"] for r in completed if r["answer"].get("error"))
        entry={"expected":len(group),"completed":len(completed),"judged":sum(r["answer"]["judged"] for r in group),"correct":sum(successful(r) for r in group),"qa_correct_before_memory_write":sum(r["answer"]["correct"] and r["answer"]["judged"] and not r["answer"].get("error") for r in group),"accuracy":sum(successful(r) for r in group)/len(group),"execution_errors":sum(errors.values()),"memory_write_errors":sum(bool(r["answer"].get("memory_write_error")) for r in group),"error_types":dict(errors),"reader_p95_millis":percentile([r["answer"]["read_millis"] for r in completed]),"search_p95_millis_including_queue":percentile([r["answer"]["search_millis"] for r in completed]),"rerank_input_bytes_p95":percentile([r["answer"]["rerank"].get("input_bytes",0) for r in completed]),"evidence_p95_millis":percentile([r.get("evidence_millis",0) for r in completed]),"omitted_records":sum(r["answer"]["omitted_context_records"] for r in group)}
        entry["reused_baseline_answers"] = sum(bool(r.get("reused_baseline")) for r in group)
        current = [r for r in group if not r.get("reused_baseline")]
        entry["reader_p95_millis"] = percentile([r["answer"]["read_millis"] for r in completed if not r.get("reused_baseline")])
        entry["historical_reader_p95_millis"] = percentile([r["answer"]["read_millis"] for r in completed if r.get("reused_baseline")])
        for prefix in ["reader","judge"]:
            entry[prefix+"_tokens"]={key:sum(r["answer"][prefix+"_usage"][key] for r in current) for key in ["input_tokens","output_tokens","total_tokens"]}
        timed=[r["search_timings"] for r in completed if r.get("search_timings")]
        entry["phase_p95_millis"]={key:percentile([v[key] for v in timed]) for key in timed[0]} if timed else {}
        recalls=[r["source_recall"] for r in group if r.get("source_recall")]
        entry["source_provenance_recall"]={"questions":len(recalls),"retrieved_all":sum(v["retrieved_recall"]==1 for v in recalls),"admitted_all":sum(v["admitted_recall"]==1 for v in recalls),"mean_retrieved_recall":sum(v["retrieved_recall"] for v in recalls)/len(recalls) if recalls else None,"mean_admitted_recall":sum(v["admitted_recall"] for v in recalls)/len(recalls) if recalls else None,"interpretation":"Annotated turn/session provenance overlap, not semantic completeness of a paraphrase."}
        status=collections.defaultdict(list)
        for row in group:status[row["prior_status"]].append(row)
        entry["by_prior_status"]={key:{"expected":len(values),"correct":sum(successful(v) for v in values)} for key,values in status.items()}
        if dataset.startswith("formal_reasoning_"):
            chains=collections.defaultdict(list)
            for row in group:chains[row["group"]].append(row)
            final=[max(values,key=lambda r:r["answer"]["index"]) for values in chains.values()]
            entry["tasks"]=len(chains);entry["final_subtask_success"]=sum(successful(r) for r in final);entry["all_subtasks_success"]=sum(all(successful(r) for r in values) for values in chains.values());entry["macro_progress"]=sum(sum(successful(r) for r in values)/len(values) for values in chains.values())/len(chains)
        result.setdefault(dataset,{})[mode]=entry
    return result

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--run",type=Path,required=True);args=parser.parse_args();root=args.run.resolve()
    manifest=json.loads((root/"manifest.json").read_text());plan=manifest["plan"];expected=expected_cases(plan)
    paths={path.name:path for path in (root/"answers").glob("*.json")}
    if set(paths)!=set(expected):raise ValueError("Missing or unexpected answer files; refusing a reduced denominator")
    rows=[]
    for name,key in expected.items():
        row=json.loads(paths[name].read_text())
        if (row["dataset"],row["group"],row["question_id"],row["mode"])!=key:raise ValueError("Answer identity mismatch")
        a=row["answer"]
        if a.get("context_sha256") and a["context_sha256"]!=hashlib.sha256(a["context"].encode()).hexdigest():raise ValueError("Answer context digest mismatch")
        rows.append(row)
    metrics=summarize(rows);lookup={(r["dataset"],r["question_id"],r["mode"]):r for r in rows};paired={}
    for row in rows:
        if row["mode"] not in ["derived","combined"]:continue
        for baseline in ["none","raw"]:
            other=lookup[(row["dataset"],row["question_id"],baseline)]
            if row["answer"].get("error","") in PENDING or other["answer"].get("error","") in PENDING:continue
            counts=paired.setdefault(row["dataset"],{}).setdefault(row["mode"]+"_vs_"+baseline,collections.Counter())
            counts["pairs"]+=1;counts["gains"]+=successful(row) and not successful(other);counts["regressions"]+=successful(other) and not successful(row)
            if row["answer"]["context_sha256"]==other["answer"]["context_sha256"]:counts["identical_context_grade_disagreements"]+=successful(row)!=successful(other)
    worker=collections.Counter();stages=[]
    for path in (root/"worker").glob("*.json"):
        value=json.loads(path.read_text())
        if "attempts" in value:
            for a in value["attempts"]:worker[a["type"]+ (".failed_attempt" if a.get("error") else ".completed_attempt")]+=1
        elif "session_id" in value:
            stages.append({"session_id":value["session_id"],"conversation_id":value["conversation_id"],"error":value.get("error","")})
    failures=[r for r in rows if r["answer"].get("error","") not in PENDING and not successful(r)]
    complete=(root/"completion.json").exists() and not json.loads((root/"completion.json").read_text()).get("cancelled",True) and all(v["completed"]==v["expected"] for modes in metrics.values() for v in modes.values())
    report={"scope":plan["scope"],"expected_answers":plan["expected_answers"],"pipeline_completed":complete,"datasets":metrics,"paired":paired,"worker_attempts":dict(worker),"worker_sessions":len(stages),"worker_stage_failures":[v for v in stages if v["error"]],"baseline_policy": "Arena none is a historical model-only control; reused usage is excluded from current reader/judge token totals. Token/prompt differences prevent same-budget causal attribution.","interpretation":"Fixed failure-enriched development set, same-family judge; original/derived corpus and real worker processing; not held-out/full benchmark/SOTA. Concurrent direct-channel times overlap. Search latency includes reranker queue; content-only embedding cache may be warm."}
    out=root/"reports";out.mkdir(mode=0o700,exist_ok=True)
    for name,value in [("summary.json",report),("failures.json",failures)]:
        path=out/name;path.write_text(json.dumps(value,indent=2)+"\n");path.chmod(0o600)
    for dataset,modes in metrics.items():
        for mode,v in modes.items():print(f"{dataset} {mode}: {v['correct']}/{v['expected']}, completed={v['completed']}, errors={v['execution_errors']}, search_p95_ms={v['search_p95_millis_including_queue']}")
    print("Report:",out)

if __name__=="__main__":main()
