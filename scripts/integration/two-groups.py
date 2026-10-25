#!/usr/bin/env python3
"""Runtime-credential example for two preconfigured, disposable workspaces.

Requires a running serve, worker and scheduler. It never calls the AI gateway.
The sample writes synthetic messages; use test workspaces, not existing groups.
"""
import argparse
import datetime
import json
import os
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.dry_run:
        print(json.dumps({"workspaces": 2, "calls_gateway": False,
                          "checks": ["capabilities", "observe", "duplicate", "finalize",
                                     "continuous progress", "scoped recall", "foreign ID hidden"]}))
        return
    base = os.environ["MEMSTATION_URL"].rstrip("/")
    run = "sample-" + str(uuid.uuid4())
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()

    def call(group, method, path, body=None, key=""):
        request = urllib.request.Request(base + path, method=method,
            data=None if body is None else json.dumps(body, ensure_ascii=False).encode(),
            headers={"Authorization": "Bearer " + group["key"], "Content-Type": "application/json",
                     "X-MemStation-Workspace": group["workspace"], "Idempotency-Key": key})
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, {}

    groups = []
    for suffix, city in [("A", "上海"), ("B", "北京")]:
        group = {k: os.environ["MEMSTATION_" + env + "_" + suffix]
                 for k, env in [("workspace", "WORKSPACE"), ("key", "KEY"),
                                ("integration", "INTEGRATION"), ("space", "SOURCE_SPACE")]}
        code, probe = call(group, "GET", "/api/v1/capabilities")
        assert code == 200 and probe["status"] == "ready" and probe["capabilities"]["independent_ingestion"], "Independent ingestion unavailable"
        batch = {"workspace_id": group["workspace"], "integration_id": group["integration"],
                 "external_space_id": group["space"], "thread_id": run, "events": [{
                 "event_id": run, "type": "message.created", "source_message_id": run,
                 "source_revision": 1, "occurred_at": now,
                 "speaker": {"kind": "human", "external_id": "sample-member", "display_name": "同名"},
                 "parts": [{"type": "text", "text": "我在" + city + "工作。"}]}]}
        code, receipt = call(group, "POST", "/api/v1/events/batch", batch, run)
        assert code == 202, "Event rejected"
        code, duplicate = call(group, "POST", "/api/v1/events/batch", batch, run)
        assert code == 202 and duplicate["events"][0]["status"] == "duplicate", "Retry duplicated an event"
        group["receipt"] = receipt
        window = receipt["events"][0]["conversation_id"]
        code, current = call(group, "GET", "/api/v1/conversations/" + window)
        assert code == 200, "Window unavailable"
        code, _ = call(group, "POST", "/api/v1/conversations/" + window + "/finalize", {"expected_revision": current["revision"]})
        assert code == 202, "Finalization rejected"
        groups.append(group)
    assert groups[0]["workspace"] != groups[1]["workspace"], "Two different workspaces required"
    deadline = time.monotonic() + 120
    pending = groups.copy()
    while pending and time.monotonic() < deadline:
        for group in pending.copy():
            code, progress = call(group, "GET", "/api/v1/ingestions/" + group["receipt"]["id"])
            assert code == 200 and not progress["dead_letter"], "Processing unavailable or in DLQ"
            if progress["status"] == "complete":
                pending.remove(group)
        if pending:
            time.sleep(1)
    assert not pending, "Processing deadline exceeded; inspect receipt/job status"
    for i, group in enumerate(groups):
        foreign = groups[1-i]["receipt"]["events"][0]["message_id"]
        code, _ = call(group, "GET", "/api/v1/sources/" + foreign)
        assert code == 404, "Foreign source must be invisible (source_read capability is required for this check)"
        code, result = call(group, "POST", "/api/v1/memory/search", {"query": "同名的人在哪里工作？", "top_n": 5})
        assert code == 200 and result["memories"], "No indexed memory returned"
        assert all(record["workspace_id"] == group["workspace"] for record in result["memories"]), "Foreign recall result"
    print(json.dumps({"status": "passed", "run": run, "workspaces": 2,
                      "mimic_joint_acceptance": False, "model_quality_claim": False}))


if __name__ == "__main__":
    main()
