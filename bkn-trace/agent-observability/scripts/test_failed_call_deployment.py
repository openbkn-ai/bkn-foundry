#!/usr/bin/env python3
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

"""Black-box #2072 acceptance against an explicitly supplied isolated deployment.

Requires the business-provenance route assembly, a functioning artifact writer,
Kafka consumer and projection worker. The Vega test dependency must fail the
inventory query. This script creates five synthetic conversations/interactions.
"""
import argparse
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def request(url, headers, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data, headers)
    with urllib.request.urlopen(req, timeout=30) as response:
        raw = response.read().decode()
        if response.headers.get_content_type() == "text/event-stream":
            raw = next(line[5:].strip() for line in raw.splitlines()
                       if line.startswith("data:"))
        return json.loads(raw), response.headers


class MCP:
    def __init__(self, url, token):
        self.url = url
        self.headers = {"Authorization": "Bearer " + token,
                        "Content-Type": "application/json",
                        "Accept": "application/json, text/event-stream"}
        self.sequence = 0
        self.rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {},
                               "clientInfo": {"name": "issue2072-acceptance", "version": "1"}})

    def rpc(self, method, params):
        self.sequence += 1
        data, headers = request(self.url, self.headers,
                                {"jsonrpc": "2.0", "id": self.sequence,
                                 "method": method, "params": params})
        if session := headers.get("Mcp-Session-Id"):
            self.headers["Mcp-Session-Id"] = session
        if "error" in data:
            raise RuntimeError("MCP protocol error: " + str(data["error"]))
        return data["result"]

    def call(self, name, arguments):
        return self.rpc("tools/call", {"name": name, "arguments": arguments})


def poll(check, timeout=25):
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        try:
            return check()
        except (AssertionError, urllib.error.HTTPError) as error:
            last_error = error
            time.sleep(0.5)
    raise RuntimeError("deployment did not converge: " + str(last_error)) from last_error


def completed_interaction(core, headers, conversation, interaction):
    page, _ = request(core + "/business-provenance/interactions?conversation_id=" +
                      conversation, headers)
    entry = next((row for row in page["entries"] if row["interaction_id"] == interaction), None)
    assert entry is not None, "interaction not listed yet"
    assert entry.get("current_record_integrity", {}).get("status") == "complete"
    assert not entry.get("record_integrity_check_failed", False)
    return entry


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mcp-url", required=True)
    parser.add_argument("--retrieval-url", required=True, help="Retrieval public base URL")
    parser.add_argument("--core-url", required=True, help="Core public base URL")
    parser.add_argument("--opensearch-url", required=True)
    parser.add_argument("--projection-index", required=True)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    token = os.environ["BKN_TRACE_TEST_DEPLOYMENT_TOKEN"]
    mcp = MCP(args.mcp_url, token)
    headers = {"Authorization": "Bearer " + token}
    core = args.core_url.rstrip("/") + "/api/agent-observability/v1"
    cases = [
        ("validation failure", "query_object_instance", {}, "input_validation", None),
        ("empty SQL", "run_sql", {"sql": ""}, "input_validation", None),
        ("missing SQL resource", "run_sql", {"sql": "SELECT 1"}, "input_validation", None),
        ("SQL backend failure", "run_sql", {"sql": "SELECT * FROM {{.inventory}}"},
         "vega_query", "resource:inventory"),
        ("SQL policy without target", "run_sql", {"sql": "DELETE FROM inventory"},
         "sql_guard", None),
    ]
    report = []
    for name, tool, business_input, stage, target in cases:
        started = mcp.call("bkn_start_interaction", {
            "question": "#2072 isolated acceptance: " + name,
            "agent_name": "issue2072-acceptance", "conversation_mode": "new"})
        assert not started.get("isError"), "managed start failed"
        assert not started.get("_meta", {}).get("openbkn.ai/trace", {}).get("code"), "start degraded"
        identity = started["structuredContent"]
        interaction = identity["interaction_id"]
        context = {key: identity[key] for key in ("conversation_id", "interaction_id")}
        if tool == "run_sql":
            try:
                request(args.retrieval_url.rstrip("/") + "/api/agent-retrieval/v1/kn/run_sql",
                        {**headers, "Content-Type": "application/json"},
                        {**business_input, "bkn_context": context})
            except urllib.error.HTTPError as error:
                assert 400 <= error.code < 600, "expected original SQL failure"
                assert error.read(), "business error response missing"
            else:
                raise AssertionError("SQL unexpectedly succeeded")
        else:
            called = mcp.call(tool, {**business_input, "bkn_context": context})
            assert called.get("isError"), "expected original business failure"
            assert called["structuredContent"]["bkn_receipt"]["receipt_status"] == "failed"
        facts, _ = request(core + "/interactions/" + interaction + "/operations", headers)
        assert facts["total"] == 1, "call executed or registered more than once"
        fact = facts["entries"][0]
        assert fact["status"] == "failed" and fact["error"]["inline"].get("stage") == stage, fact
        assert fact["input"]["inline"] == business_input, "recorded business input changed"
        def durable_receipt():
            value, _ = request(core + "/receipts/" + fact["receipt_id"], headers)
            assert value["evidence_durability"] == "durable", "Ledger not confirmed"
            if tool == "run_sql":
                assert value["observed_evidence_refs"], "SQL event not consumed"
            return value

        receipt = poll(durable_receipt)
        refs = [ref["ref_id"] for ref in receipt["business_refs"]]
        assert refs == ([] if target is None else [target]), "authoritative target mismatch"
        finished = mcp.call("bkn_finish_interaction", {
            "interaction_id": interaction, "outcome": "failed", "reason": "expected test failure"})
        assert not finished.get("isError"), "managed finish failed"

        def projected():
            path = (args.opensearch_url.rstrip("/") + "/" + args.projection_index +
                    "/_doc/" + urllib.parse.quote("receipt:" + fact["receipt_id"], safe=""))
            document, _ = request(path, {})
            assert document["_source"]["receipt_status"] == "failed", "projection still pending"
            assert document["_source"]["business_refs"] == receipt["business_refs"]
            assert document["_source"]["evidence_durability"] == "durable"
            assert document["_source"]["observed_evidence_refs"] == receipt["observed_evidence_refs"]
            for event_id in receipt["observed_evidence_refs"]:
                event_path = (args.opensearch_url.rstrip("/") + "/" + args.projection_index +
                              "/_doc/" + urllib.parse.quote("evidence_event:" + event_id, safe=""))
                event_document, _ = request(event_path, {})
                event = event_document["_source"]
                assert event["operation_id"] == fact["operation_id"]
                assert event["interaction_id"] == interaction and event["attempt"] == fact["attempt"]
                assert event["request_id"] == fact["request_id"] and event["trace_id"] == fact["trace_id"]
                assert event["envelope"]["payload"]["error_stage"] == stage
                event_refs = event["envelope"]["payload"]["resource_refs"]
                assert [ref["ref_id"] for ref in event_refs] == ([] if target is None else [target])
            return document

        poll(projected)
        detail_url = core + "/business-provenance/interactions/" + interaction
        detail, _ = request(detail_url, headers)
        matching = [row for row in detail["time_rail"] if row["operation_id"] == fact["operation_id"]]
        assert len(matching) == 1 and matching[0]["error"]["inline"]["stage"] == stage
        if name == "SQL policy without target":
            operation = next(row for row in detail["operations"] if row["operation_id"] == fact["operation_id"])
            assert operation["status"] == "unresolved" and "resource_id" in operation["missing_facts"]
            assert fact.get("capability_profile") is None, "REST capability contract changed"

        poll(lambda: completed_interaction(core, headers, identity["conversation_id"], interaction))
        integrity = "complete (registered call records)"
        report.append({"case": name, "interaction_id": interaction,
                       "operation_id": fact["operation_id"], "stage": stage,
                       "business_refs": refs, "record_integrity": integrity,
                       "projection": "receipt and consumed events matched",
                       "observed_evidence_refs": receipt["observed_evidence_refs"],
                       "capability_profile_present": bool(fact.get("capability_profile")), "passed": True})
        print(name + ": passed", flush=True)
    Path(args.report).write_text(json.dumps({"passed": True, "cases": report}, indent=2) + "\n")


if __name__ == "__main__":
    main()
