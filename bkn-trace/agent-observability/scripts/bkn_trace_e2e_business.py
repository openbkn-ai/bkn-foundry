#!/usr/bin/env python3
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

import argparse
import json
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Callable


TRACEPARENT_RE = re.compile(
    r"^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$"
)


@dataclass(frozen=True)
class RunIdentity:
    trace_id: str
    request_id: str


@dataclass(frozen=True)
class Check:
    name: str
    status: str
    detail: str = ""


@dataclass
class BusinessE2EReport:
    trace_id: str
    request_id: str
    checks: list[Check]

    @property
    def passed(self) -> bool:
        return all(check.status == "pass" for check in self.checks)

    def to_dict(self) -> dict[str, Any]:
        return {
            "passed": self.passed,
            "trace_id": self.trace_id,
            "request_id": self.request_id,
            "checks": [asdict(check) for check in self.checks],
        }


def extract_run_identity(headers: dict[str, str]) -> RunIdentity:
    normalized = {key.lower(): value.strip() for key, value in headers.items()}
    trace_id = normalized.get("x-trace-id", "")
    if not trace_id:
        traceparent = normalized.get("traceparent", "").lower()
        match = TRACEPARENT_RE.match(traceparent)
        if match:
            trace_id = match.group(1)
    request_id = normalized.get("bkn-request-id") or normalized.get(
        "x-request-id", ""
    )
    if not re.fullmatch(r"[0-9a-f]{32}", trace_id):
        raise ValueError("real agent response is missing a valid trace id")
    if not request_id:
        raise ValueError("real agent response is missing bkn-request-id")
    return RunIdentity(trace_id=trace_id, request_id=request_id)


def _pass(name: str, detail: str = "") -> Check:
    return Check(name=name, status="pass", detail=detail)


def _fail(name: str, detail: str) -> Check:
    return Check(name=name, status="fail", detail=detail)


def _check(name: str, condition: bool, detail: str) -> Check:
    return _pass(name, detail) if condition else _fail(name, detail)


def _nonempty_content(artifact: dict[str, Any] | None) -> bool:
    if not artifact or "content" not in artifact:
        return False
    content = artifact["content"]
    if isinstance(content, str):
        return bool(content.strip())
    if isinstance(content, (dict, list)):
        return bool(content)
    return content is not None


def evaluate_business_e2e(
    *,
    agent_response: dict[str, Any],
    identity: RunIdentity,
    request_summary: dict[str, Any],
    trace_page: dict[str, Any],
    trace_graph: dict[str, Any],
    evidence_chain: dict[str, Any],
    business_graph: dict[str, Any],
    artifacts: list[dict[str, Any]],
) -> BusinessE2EReport:
    checks: list[Check] = []
    checks.append(
        _check(
            "agent.succeeded",
            agent_response.get("status") == "succeeded"
            and bool(str(agent_response.get("output", "")).strip()),
            f"status={agent_response.get('status')}",
        )
    )
    checks.append(
        _check(
            "request.identity",
            request_summary.get("request_id") == identity.request_id,
            f"request_id={request_summary.get('request_id', '')}",
        )
    )
    checks.append(
        _check(
            "request.completed",
            request_summary.get("status") == "completed",
            f"status={request_summary.get('status', '')}",
        )
    )
    checks.append(
        _check(
            "request.question_preview",
            bool(str(request_summary.get("question_preview", "")).strip()),
            "question preview must be visible",
        )
    )
    checks.append(
        _check(
            "request.result_preview",
            bool(str(request_summary.get("result_preview", "")).strip()),
            "result preview must be visible",
        )
    )

    trace_entries = trace_page.get("entries") or []
    linked_trace = any(
        entry.get("trace_id") == identity.trace_id
        and (not entry.get("request_id") or entry.get("request_id") == identity.request_id)
        for entry in trace_entries
        if isinstance(entry, dict)
    )
    checks.append(
        _check(
            "request.trace_link",
            linked_trace,
            f"trace_id={identity.trace_id}",
        )
    )
    technical_data = trace_graph.get("data") or {}
    technical_nodes = technical_data.get("nodes") or []
    technical_edges = technical_data.get("edges") or []
    checks.append(
        _check(
            "trace-graph.identity",
            trace_graph.get("trace_id") == identity.trace_id
            and trace_graph.get("status") == "ok",
            (
                f"trace_id={trace_graph.get('trace_id', '')} "
                f"status={trace_graph.get('status', '')}"
            ),
        )
    )
    checks.append(
        _check(
            "trace-graph.spans",
            len(technical_nodes) > 1,
            f"span_count={len(technical_nodes)}",
        )
    )
    checks.append(
        _check(
            "trace-graph.edges",
            bool(technical_edges),
            f"edge_count={len(technical_edges)}",
        )
    )

    chain_data = evidence_chain.get("data") or {}
    claims = chain_data.get("claims") or []
    sourced_claims = [
        claim
        for claim in claims
        if isinstance(claim, dict) and claim.get("source_event_ids")
    ]
    checks.append(
        _check(
            "evidence.claim_sources",
            bool(sourced_claims),
            f"claims={len(claims)} sourced={len(sourced_claims)}",
        )
    )

    graph_data = business_graph.get("data") or {}
    nodes = graph_data.get("nodes") or []
    edges = graph_data.get("edges") or []
    node_types = {
        node.get("node_type")
        for node in nodes
        if isinstance(node, dict) and node.get("node_type")
    }
    checks.append(
        _check(
            "graph.business_nodes",
            "claim" in node_types
            and bool(node_types.intersection({"object", "relation", "property", "resource", "field"})),
            ",".join(sorted(node_types)),
        )
    )
    checks.append(
        _check(
            "graph.supports_edge",
            any(
                isinstance(edge, dict) and edge.get("edge_type") == "supports"
                for edge in edges
            ),
            f"edge_count={len(edges)}",
        )
    )
    producers = {
        str((node.get("properties") or {}).get("producer_module", "")).strip()
        for node in nodes
        if isinstance(node, dict)
    }
    producers.discard("")
    checks.append(
        _check(
            "graph.real_module_count",
            len(producers) >= 3,
            f"modules={','.join(sorted(producers))}",
        )
    )
    checks.append(
        _check(
            "graph.data_query",
            any(
                isinstance(node, dict)
                and (
                    node.get("label") == "data.query.observed"
                    or (node.get("properties") or {}).get("event_type")
                    == "data.query.observed"
                )
                for node in nodes
            ),
            "data.query.observed must be present",
        )
    )

    by_type = {
        str(artifact.get("artifact_type", "")): artifact
        for artifact in artifacts
        if isinstance(artifact, dict)
    }
    for artifact_type, check_label in (
        ("question", "question"),
        ("result", "result"),
        ("query", "query"),
        ("data_result", "data-result"),
    ):
        checks.append(
            _check(
                f"artifact.{check_label}.content",
                _nonempty_content(by_type.get(artifact_type)),
                "authorized content must be present, not hash-only",
            )
        )

    return BusinessE2EReport(
        trace_id=identity.trace_id,
        request_id=identity.request_id,
        checks=checks,
    )


def _request_json(
    method: str,
    url: str,
    *,
    headers: dict[str, str],
    payload: dict[str, Any] | None,
    timeout: float,
    insecure: bool,
) -> tuple[int, dict[str, str], dict[str, Any]]:
    body = None if payload is None else json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(url, data=body, headers=headers, method=method)
    handlers: list[Any] = [urllib.request.ProxyHandler({})]
    if insecure:
        handlers.append(
            urllib.request.HTTPSHandler(context=ssl._create_unverified_context())
        )
    opener = urllib.request.build_opener(*handlers)
    try:
        with opener.open(request, timeout=timeout) as response:
            raw = response.read().decode("utf-8", errors="replace")
            parsed = json.loads(raw) if raw else {}
            return response.status, dict(response.headers.items()), parsed
    except urllib.error.HTTPError as error:
        raw = error.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = {"message": raw[:500]}
        return error.code, dict(error.headers.items()), parsed


def _core_headers(args: argparse.Namespace) -> dict[str, str]:
    headers = {
        "Accept": "application/json",
        "x-account-id": args.account_id,
        "x-account-type": args.account_type,
        "x-tenant-id": args.tenant_id,
        "x-business-domain": args.business_domain,
    }
    if args.query_token:
        headers["Authorization"] = f"Bearer {args.query_token}"
    return headers


def _core_get(
    args: argparse.Namespace, path: str
) -> tuple[int, dict[str, Any]]:
    status, _, body = _request_json(
        "GET",
        args.core_base_url.rstrip("/") + path,
        headers=_core_headers(args),
        payload=None,
        timeout=args.timeout,
        insecure=args.insecure,
    )
    return status, body


def _poll_core(
    args: argparse.Namespace,
    path: str,
    *,
    ready: Callable[[dict[str, Any]], bool] | None = None,
) -> dict[str, Any]:
    last_status = 0
    last_body: dict[str, Any] = {}
    for attempt in range(args.retries + 1):
        last_status, last_body = _core_get(args, path)
        if last_status == 200 and (ready is None or ready(last_body)):
            return last_body
        if attempt < args.retries:
            time.sleep(args.retry_delay)
    raise RuntimeError(
        f"core query failed: path={path} status={last_status} "
        f"code={last_body.get('code', '')}"
    )


def run_real_e2e(args: argparse.Namespace) -> BusinessE2EReport:
    invoke_headers = {
        "Accept": "application/json",
        "Content-Type": "application/json",
        "x-account-id": args.account_id,
        "x-account-type": args.account_type,
        "x-tenant-id": args.tenant_id,
        "x-business-domain": args.business_domain,
    }
    status, response_headers, agent_response = _request_json(
        "POST",
        (
            args.agent_base_url.rstrip("/")
            + "/api/bkn-agent/v1/invoke/"
            + urllib.parse.quote(args.agent_id, safe="")
        ),
        headers=invoke_headers,
        payload={"message": args.message, "prompt_override": args.prompt_override},
        timeout=args.agent_timeout,
        insecure=args.insecure,
    )
    if status < 200 or status >= 300:
        raise RuntimeError(
            f"real agent invocation failed: status={status} "
            f"message={agent_response.get('message', '')}"
        )
    identity = extract_run_identity(response_headers)
    request_summary = _poll_core(
        args,
        "/api/agent-observability/v1/requests/"
        + urllib.parse.quote(identity.request_id, safe=""),
        ready=lambda body: (
            body.get("status") == "completed"
            and bool(str(body.get("result_preview", "")).strip())
        ),
    )
    trace_page = _poll_core(
        args,
        "/api/agent-observability/v1/requests/"
        + urllib.parse.quote(identity.request_id, safe="")
        + "/traces?limit=50",
    )
    trace_graph = _poll_core(
        args,
        "/api/agent-observability/v1/traces/"
        + identity.trace_id
        + "/trace-graph",
        ready=lambda body: (
            body.get("trace_id") == identity.trace_id
            and body.get("status") == "ok"
            and int((body.get("page") or {}).get("node_count") or 0) > 1
            and int((body.get("page") or {}).get("edge_count") or 0) > 0
        ),
    )
    evidence_chain = _poll_core(
        args,
        "/api/agent-observability/v1/traces/"
        + identity.trace_id
        + "/evidence-chain?limit=500",
        ready=lambda body: {
            str(link.get("artifact_type", ""))
            for link in ((body.get("data") or {}).get("artifact_links") or [])
            if isinstance(link, dict)
        }.issuperset({"question", "result", "query", "data_result"}),
    )
    business_graph = _poll_core(
        args,
        "/api/agent-observability/v1/traces/"
        + identity.trace_id
        + "/business-graph?limit=500",
        ready=lambda body: (
            any(
                isinstance(node, dict)
                and node.get("node_type")
                in {"object", "relation", "property", "resource", "field"}
                for node in ((body.get("data") or {}).get("nodes") or [])
            )
            and any(
                isinstance(node, dict)
                and (
                    node.get("label") == "data.query.observed"
                    or (node.get("properties") or {}).get("event_type")
                    == "data.query.observed"
                )
                for node in ((body.get("data") or {}).get("nodes") or [])
            )
        ),
    )

    artifacts: list[dict[str, Any]] = []
    links = (evidence_chain.get("data") or {}).get("artifact_links") or []
    for link in links:
        if not isinstance(link, dict):
            continue
        reference = str(link.get("artifact_ref", ""))
        if not reference.startswith("artifact:"):
            continue
        artifact_id = reference.removeprefix("artifact:")
        artifact = _poll_core(
            args,
            "/api/agent-observability/v1/evidence/artifacts/"
            + urllib.parse.quote(artifact_id, safe=""),
        )
        artifacts.append(artifact)

    return evaluate_business_e2e(
        agent_response=agent_response,
        identity=identity,
        request_summary=request_summary,
        trace_page=trace_page,
        trace_graph=trace_graph,
        evidence_chain=evidence_chain,
        business_graph=business_graph,
        artifacts=artifacts,
    )


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run and verify a real business-first BKN Trace E2E."
    )
    parser.add_argument("--agent-base-url", default="http://127.0.0.1:18084")
    parser.add_argument("--core-base-url", default="http://127.0.0.1:18085")
    parser.add_argument("--agent-id", required=True)
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--account-type", default="user")
    parser.add_argument("--tenant-id", required=True)
    parser.add_argument("--business-domain", required=True)
    parser.add_argument("--query-token", default="")
    parser.add_argument("--message", required=True)
    parser.add_argument("--prompt-override", required=True)
    parser.add_argument("--timeout", type=float, default=15)
    parser.add_argument("--agent-timeout", type=float, default=180)
    parser.add_argument("--retries", type=int, default=10)
    parser.add_argument("--retry-delay", type=float, default=1)
    parser.add_argument("--report-file", default="")
    parser.add_argument("--insecure", action="store_true")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    try:
        report = run_real_e2e(args)
    except (RuntimeError, ValueError, urllib.error.URLError, json.JSONDecodeError) as error:
        print(f"FAIL e2e.runtime: {error}")
        return 1
    for check in report.checks:
        print(f"{check.status.upper():4} {check.name}: {check.detail}")
    encoded = json.dumps(report.to_dict(), ensure_ascii=False, indent=2)
    if args.report_file:
        Path(args.report_file).write_text(encoded + "\n", encoding="utf-8")
    print(encoded)
    return 0 if report.passed else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
