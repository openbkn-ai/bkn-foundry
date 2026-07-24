#!/usr/bin/env python3
import argparse
import json
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass


TRACEPARENT_RE = re.compile(
    r"^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$"
)


@dataclass(frozen=True)
class ProbeResult:
    name: str
    status: str
    reason: str
    detail: str = ""


def extract_trace_id(traceparent: str) -> str:
    match = TRACEPARENT_RE.match(traceparent.strip().lower())
    if not match:
        raise ValueError("invalid traceparent")
    trace_id = match.group(1)
    if trace_id == "0" * 32:
        raise ValueError("invalid traceparent: zero trace id")
    return trace_id


def classify_http_result(status_code: int, body: str) -> ProbeResult:
    if 200 <= status_code < 300:
        return ProbeResult("http", "pass", "ok")

    lowered = body.lower()
    if status_code == 0 and ("ssl" in lowered or "certificate" in lowered):
        return ProbeResult("http", "fail", "tls_failed")
    if status_code == 0:
        return ProbeResult("http", "fail", "connection_failed")
    if "public base url of /studio/" in lowered:
        return ProbeResult("http", "fail", "studio_proxy_not_enabled")
    if status_code in (401, 403):
        return ProbeResult("http", "fail", "auth_failed")
    if "index_not_found_exception" in lowered:
        return ProbeResult("http", "fail", "trace_index_missing")
    if status_code == 502:
        return ProbeResult("http", "fail", "bad_gateway")
    if status_code == 404:
        return ProbeResult("http", "fail", "not_found")
    return ProbeResult("http", "fail", f"http_{status_code}")


def request_json(
    url: str,
    token: str = "",
    timeout: float = 10.0,
    insecure: bool = False,
) -> tuple[int, str]:
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    context = ssl._create_unverified_context() if insecure else None
    handlers = [urllib.request.ProxyHandler({})]
    if context:
        handlers.append(urllib.request.HTTPSHandler(context=context))
    opener = urllib.request.build_opener(*handlers)
    try:
        with opener.open(request, timeout=timeout) as response:
            return response.status, response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode("utf-8", errors="replace")
    except urllib.error.URLError as error:
        return 0, str(error.reason)


def post_json(
    url: str,
    payload: dict,
    token: str = "",
    timeout: float = 10.0,
    insecure: bool = False,
) -> tuple[int, str]:
    body = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=body,
        headers={"Accept": "application/json", "Content-Type": "application/json"},
        method="POST",
    )
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    context = ssl._create_unverified_context() if insecure else None
    handlers = [urllib.request.ProxyHandler({})]
    if context:
        handlers.append(urllib.request.HTTPSHandler(context=context))
    opener = urllib.request.build_opener(*handlers)
    try:
        with opener.open(request, timeout=timeout) as response:
            return response.status, response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode("utf-8", errors="replace")
    except urllib.error.URLError as error:
        return 0, str(error.reason)


def join_url(base_url: str, path: str, params: dict[str, str] | None = None) -> str:
    base = base_url.rstrip("/")
    url = f"{base}{path}"
    if params:
        url = f"{url}?{urllib.parse.urlencode(params)}"
    return url


def parse_json(body: str) -> dict | None:
    try:
        parsed = json.loads(body)
    except json.JSONDecodeError:
        return None
    return parsed if isinstance(parsed, dict) else None


def probe_endpoint(
    name: str,
    url: str,
    token: str,
    timeout: float,
    insecure: bool,
    retries: int,
    retry_delay: float,
) -> ProbeResult:
    attempts = max(0, retries) + 1
    retryable = {"not_found", "trace_index_missing", "connection_failed", "bad_gateway"}
    for attempt in range(attempts):
        status_code, body = request_json(url, token=token, timeout=timeout, insecure=insecure)
        result = classify_http_result(status_code, body)
        if result.status == "pass":
            break
        if result.reason not in retryable or attempt == attempts - 1:
            return ProbeResult(name, result.status, result.reason, body[:300])
        if retry_delay > 0:
            time.sleep(retry_delay)

    payload = parse_json(body)
    if payload is None:
        return ProbeResult(name, "fail", "invalid_json", body[:300])
    if payload.get("partial") is True:
        reasons = payload.get("partial_reason") or []
        return ProbeResult(name, "warn", "partial", ",".join(map(str, reasons)))
    return ProbeResult(name, "pass", "ok")


def post_endpoint(
    name: str,
    url: str,
    payload: dict,
    token: str,
    timeout: float,
    insecure: bool,
) -> ProbeResult:
    status_code, body = post_json(url, payload, token=token, timeout=timeout, insecure=insecure)
    result = classify_http_result(status_code, body)
    if result.status != "pass":
        return ProbeResult(name, result.status, result.reason, body[:300])
    return ProbeResult(name, "pass", "ok")


def build_otlp_trace_payload(trace_id: str, span_id: str, request_id: str) -> dict:
    return {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": "bkn-trace-e2e-lite-probe"}}
                    ]
                },
                "scopeSpans": [
                    {
                        "scope": {"name": "bkn-trace-e2e-lite-probe"},
                        "spans": [
                            {
                                "traceId": trace_id,
                                "spanId": span_id,
                                "name": "bkn-trace.e2e_lite_probe",
                                "kind": 2,
                                "startTimeUnixNano": "1784894250000000000",
                                "endTimeUnixNano": "1784894251000000000",
                                "attributes": [
                                    {"key": "bkn.request.id", "value": {"stringValue": request_id}},
                                    {"key": "bkn.module.name", "value": {"stringValue": "bkn-trace"}},
                                ],
                                "status": {"code": 1},
                            }
                        ],
                    }
                ],
            }
        ]
    }


def build_evidence_payload(trace_id: str, span_id: str, request_id: str) -> dict:
    base_event = {
        "bkn.trace.schema.version": "2.0.0",
        "observed_at": "2026-07-24T11:57:30.100000000Z",
        "emitted_at": "2026-07-24T11:57:30.101000000Z",
        "producer_module": "bkn-trace-e2e-lite-probe",
        "trace_id": trace_id,
        "span_id": span_id,
        "bkn.request.id": request_id,
        "bkn.operation.name": "bkn_trace.e2e_lite_probe",
    }
    return {
        "bkn.trace.schema.version": "2.0.0",
        "trace": {
            "trace_id": trace_id,
            "bkn.request.id": request_id,
            "traceparent": f"00-{trace_id}-{span_id}-01",
            "bkn.tenant.id": "tenant_e2e_lite",
            "bkn.account.id": "acct_e2e_lite",
            "bkn.account.type": "app",
        },
        "events": [
            {
                **base_event,
                "event_id": "evt_e2e_lite_claim",
                "event_type": "claim.created",
                "payload": {
                    "claim_id": "claim_e2e_lite",
                    "claim_type": "diagnostic",
                    "claim_hash": "sha256:e2e-lite-claim",
                    "visibility": "visible",
                    "version_status": "versioned",
                },
            },
            {
                **base_event,
                "event_id": "evt_e2e_lite_evidence",
                "event_type": "evidence.refs.created",
                "payload": {
                    "claim_id": "claim_e2e_lite",
                    "evidence_refs": [
                        {
                            "ref_id": "row:e2e_lite_visible",
                            "ref_type": "row_ref",
                            "visibility": "visible",
                            "version_status": "versioned",
                        }
                    ],
                },
            },
            {
                **base_event,
                "event_id": "evt_e2e_lite_business",
                "event_type": "business.refs.resolved",
                "payload": {
                    "claim_id": "claim_e2e_lite",
                    "business_refs": [
                        {
                            "ref_id": "object:e2e_customer",
                            "ref_type": "object",
                            "label": "E2E Customer",
                            "visibility": "visible",
                            "version_status": "versioned",
                        }
                    ],
                },
            },
        ],
    }


def run_probe(args: argparse.Namespace) -> list[ProbeResult]:
    trace_id = args.trace_id
    if not trace_id and args.traceparent:
        trace_id = extract_trace_id(args.traceparent)
    span_id = args.span_id
    request_id = args.request_id

    results: list[ProbeResult] = []
    if trace_id and request_id and args.emit_otlp_url:
        results.append(
            post_endpoint(
                "emit_otlp_trace",
                args.emit_otlp_url,
                build_otlp_trace_payload(trace_id, span_id, request_id),
                args.token,
                args.timeout,
                args.insecure,
            )
        )
    if trace_id and request_id and args.ingest_evidence:
        results.append(
            post_endpoint(
                "ingest_evidence",
                join_url(args.base_url, "/api/agent-observability/v1/evidence/events"),
                build_evidence_payload(trace_id, span_id, request_id),
                args.token,
                args.timeout,
                args.insecure,
            )
        )
    if trace_id:
        results.append(
            probe_endpoint(
                "trace_graph",
                join_url(args.base_url, f"/api/agent-observability/v1/traces/{trace_id}/trace-graph"),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )
        results.append(
            probe_endpoint(
                "evidence_chain_by_trace",
                join_url(
                    args.base_url,
                    f"/api/agent-observability/v1/traces/{trace_id}/evidence-chain",
                    {"limit": str(args.limit)},
                ),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )
        results.append(
            probe_endpoint(
                "business_graph_by_trace",
                join_url(
                    args.base_url,
                    f"/api/agent-observability/v1/traces/{trace_id}/business-graph",
                    {"limit": str(args.limit)},
                ),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )
        results.append(
            probe_endpoint(
                "snapshot_preview_by_trace",
                join_url(
                    args.base_url,
                    f"/api/agent-observability/v1/traces/{trace_id}/snapshot-preview",
                    {"limit": str(args.limit)},
                ),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )

    if args.request_id:
        results.append(
            probe_endpoint(
                "evidence_chain_by_request",
                join_url(
                    args.base_url,
                    "/api/agent-observability/v1/traces/by-request",
                    {"request_id": args.request_id, "limit": str(args.limit)},
                ),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )
        results.append(
            probe_endpoint(
                "business_graph_by_request",
                join_url(
                    args.base_url,
                    "/api/agent-observability/v1/traces/by-request/business-graph",
                    {"request_id": args.request_id, "limit": str(args.limit)},
                ),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )
        results.append(
            probe_endpoint(
                "snapshot_preview_by_request",
                join_url(
                    args.base_url,
                    "/api/agent-observability/v1/traces/by-request/snapshot-preview",
                    {"request_id": args.request_id, "limit": str(args.limit)},
                ),
                args.token,
                args.timeout,
                args.insecure,
                args.retries,
                args.retry_delay,
            )
        )

    if not results:
        results.append(
            ProbeResult(
                "input",
                "fail",
                "missing_trace_or_request_id",
                "provide --trace-id, --traceparent, or --request-id",
            )
        )
    return results


def print_results(results: list[ProbeResult]) -> None:
    for result in results:
        line = f"{result.status.upper():4} {result.name}: {result.reason}"
        if result.detail:
            line = f"{line} - {result.detail}"
        print(line)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Probe BKN Trace E2E Lite readiness against a real OpenBKN API endpoint."
    )
    parser.add_argument("--base-url", default="https://localhost", help="OpenBKN gateway base URL")
    parser.add_argument("--trace-id", default="", help="Trace id to query")
    parser.add_argument("--traceparent", default="", help="W3C traceparent; trace id is extracted")
    parser.add_argument("--request-id", default="", help="bkn.request.id to query")
    parser.add_argument(
        "--span-id",
        default="2222222222222222",
        help="Span id used when --emit-otlp-url or --ingest-evidence is enabled",
    )
    parser.add_argument(
        "--emit-otlp-url",
        default="",
        help="Optional OTLP HTTP traces endpoint; when set, emits one minimal real span before queries",
    )
    parser.add_argument(
        "--ingest-evidence",
        action="store_true",
        help="Post one minimal evidence event batch to the BKN Trace API before queries",
    )
    parser.add_argument("--token", default="", help="Bearer token for authenticated gateways")
    parser.add_argument("--limit", type=int, default=100, help="Query limit for graph endpoints")
    parser.add_argument("--timeout", type=float, default=10.0, help="HTTP timeout in seconds")
    parser.add_argument("--retries", type=int, default=3, help="Retry count for query endpoints")
    parser.add_argument("--retry-delay", type=float, default=1.0, help="Delay between query retries")
    parser.add_argument(
        "--insecure",
        action="store_true",
        help="Skip TLS certificate verification for local self-signed gateways",
    )
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    results = run_probe(args)
    print_results(results)
    return 1 if any(result.status == "fail" for result in results) else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
