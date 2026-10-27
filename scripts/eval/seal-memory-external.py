#!/usr/bin/env python3
"""Seal an external evaluation journal after its supervising process has exited."""

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, type=Path)
    parser.add_argument("--process-exit-code", required=True, type=int)
    args = parser.parse_args()
    run = args.run.resolve()
    target = run / "sealed.json"
    if target.exists():
        raise ValueError("Journal is already sealed")
    spec = importlib.util.spec_from_file_location("external_summary", Path(__file__).with_name("summarize-memory-external.py"))
    summary = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(summary)
    manifest = (run / "manifest.json").read_bytes()
    expected, _ = summary.expected_jobs(json.loads(manifest)["plan"])
    paths = [path for pattern in ["*-memory.json", "*-none.json"] for path in run.glob(pattern)]
    actual = {}
    files = {}
    for path in paths:
        raw = path.read_bytes()
        job = json.loads(raw)
        key = (job["dataset"], job["id"], job["mode"])
        if key in actual:
            raise ValueError("Duplicate case in journal")
        actual[key] = len(job["answers"])
        files[path.name] = hashlib.sha256(raw).hexdigest()
    if actual != expected:
        raise ValueError("Journal is missing expected cases or has changed denominators")
    if args.process_exit_code == 0:
        completion = json.loads((run / "completion.json").read_text())
        if completion["cancelled"] or completion["jobs"] != len(expected):
            raise ValueError("Successful process did not complete the declared matrix")
    seal = {
        "protocol": "memstation.external.memory.v1",
        "manifest_sha256": hashlib.sha256(manifest).hexdigest(),
        "files": files,
        "process_exit_code": args.process_exit_code,
        "process_finished_at": datetime.now(timezone.utc).isoformat(),
    }
    with target.open("x") as stream:
        json.dump(seal, stream, indent=2)
        stream.write("\n")
    target.chmod(0o600)
    print(f"Sealed {len(files)} complete case journals: {target}")


if __name__ == "__main__":
    main()
