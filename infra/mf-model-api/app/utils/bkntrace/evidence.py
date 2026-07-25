import asyncio
import hashlib
import json
import os
import secrets
from datetime import datetime, timezone
from typing import Any, Dict, Iterable, List, Optional

import aiohttp


CONTRACT_VERSION = "2.0.0"
MODULE_NAME = "mf-model-api"
EVIDENCE_INGEST_URL_ENV = "BKN_TRACE_EVIDENCE_INGEST_URL"
EVIDENCE_INGEST_TIMEOUT_MS_ENV = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
_background_tasks = set()


def evidence_enabled() -> bool:
    return bool(os.getenv(EVIDENCE_INGEST_URL_ENV, "").strip())


def hash_value(value: Any) -> str:
    try:
        raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
    except TypeError:
        raw = str(value).encode("utf-8")
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def build_request_context(headers: Optional[Dict[str, str]], account_id: str = "", account_type: str = "user") -> Dict[str, str]:
    headers = headers or {}
    normalized = {str(k).lower(): str(v) for k, v in headers.items()}
    traceparent = normalized.get("traceparent", "").strip()
    trace_id = ""
    span_id = ""
    if _valid_traceparent(traceparent):
        parts = traceparent.split("-")
        trace_id = parts[1]
        span_id = parts[2]
    if not trace_id:
        trace_id = secrets.token_hex(16)
    if not span_id:
        span_id = secrets.token_hex(8)

    request_id = normalized.get("bkn-request-id", "").strip() or normalized.get("x-request-id", "").strip()
    if not request_id.startswith("req_"):
        request_id = "req_" + secrets.token_hex(12)

    return {
        "trace_id": trace_id,
        "span_id": span_id,
        "traceparent": traceparent or f"00-{trace_id}-{span_id}-01",
        "request_id": request_id,
        "account_id": normalized.get("x-account-id", "").strip() or account_id or "",
        "account_type": normalized.get("x-account-type", "").strip() or account_type or "user",
        "business_domain": normalized.get("x-business-domain", "").strip(),
    }


def build_model_call_events(
    request_context: Dict[str, str],
    *,
    model_id: str,
    model_name: str,
    model_provider: str,
    operation: str,
    messages: Iterable[Dict[str, Any]],
    params: Dict[str, Any],
    status: str,
    input_token_count: int = 0,
    output_token_count: int = 0,
    output: Any = None,
    error_category: str = "",
) -> List[Dict[str, Any]]:
    safe_params = _safe_model_params(params)
    subject_summary = {
        "model_id": model_id,
        "model_name": model_name,
        "model_provider": model_provider,
        "operation": operation,
        "status": status,
        "input_unit_count": int(input_token_count or 0),
        "output_unit_count": int(output_token_count or 0),
        "parameter_hash": hash_value(safe_params),
        "prompt_hash": hash_value(list(messages or [])),
        "output_hash": hash_value(output) if output is not None else "",
        "error_category": error_category,
        "producer_module": MODULE_NAME,
        "contract_version": CONTRACT_VERSION,
    }
    claim_id = _claim_id(operation, model_id or model_name, subject_summary)
    refs = _model_evidence_refs(model_id, model_name, model_provider, subject_summary, safe_params)
    now = _utc_now()

    return [
        _build_event(request_context, "claim.created", operation, now, {
            "claim_id": claim_id,
            "claim_type": "finding",
            "claim_hash": hash_value(subject_summary),
            "visibility": "visible",
            "version_status": "unversioned",
            "partial_reason": ["model_call_refs_unversioned"],
            "subject_refs": {
                "model_id": model_id,
                "model_name": model_name,
                "model_provider": model_provider,
                "status": status,
                "parameter_hash": subject_summary["parameter_hash"],
                "prompt_hash": subject_summary["prompt_hash"],
                "output_hash": subject_summary["output_hash"],
                "input_unit_count": subject_summary["input_unit_count"],
                "output_unit_count": subject_summary["output_unit_count"],
                "error_category": error_category,
                "evidence_refs_hash": hash_value([ref["summary_hash"] for ref in refs]),
                "data.classification": "internal",
            },
        }),
        _build_event(request_context, "evidence.refs.created", operation, now, {
            "claim_id": claim_id,
            "evidence_refs": refs,
        }),
    ]


def emit_model_call_events(request_context: Dict[str, str], events: List[Dict[str, Any]]) -> None:
    ingest_url = os.getenv(EVIDENCE_INGEST_URL_ENV, "").strip()
    if not ingest_url or not events:
        return
    payload = {
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "trace": {
            "trace_id": request_context.get("trace_id", ""),
            "traceparent": request_context.get("traceparent", ""),
            "bkn.request.id": request_context.get("request_id", ""),
            "business_domain": request_context.get("business_domain", ""),
            "bkn.account.id": request_context.get("account_id", ""),
            "bkn.account.type": request_context.get("account_type", ""),
        },
        "events": events,
    }
    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        return
    task = loop.create_task(_post_batch(ingest_url, payload))
    _background_tasks.add(task)
    task.add_done_callback(_background_tasks.discard)


async def _post_batch(ingest_url: str, payload: Dict[str, Any]) -> None:
    timeout_ms = int(os.getenv(EVIDENCE_INGEST_TIMEOUT_MS_ENV, "1000") or "1000")
    timeout = aiohttp.ClientTimeout(total=max(timeout_ms, 1) / 1000)
    try:
        async with aiohttp.ClientSession(timeout=timeout) as session:
            async with session.post(ingest_url, json=payload) as response:
                await response.read()
    except Exception:
        return


def _model_evidence_refs(
    model_id: str,
    model_name: str,
    model_provider: str,
    subject_summary: Dict[str, Any],
    safe_params: Dict[str, Any],
) -> List[Dict[str, Any]]:
    model_summary = {
        "kind": "model",
        "model_id": model_id,
        "model_name": model_name,
        "model_provider": model_provider,
        "version_status": "unversioned",
    }
    prompt_summary = {
        "kind": "message_context",
        "message_hash": subject_summary["prompt_hash"],
        "parameter_hash": hash_value(safe_params),
    }
    output_summary = {
        "kind": "model_result",
        "status": subject_summary["status"],
        "result_hash": subject_summary["output_hash"],
        "input_unit_count": subject_summary["input_unit_count"],
        "output_unit_count": subject_summary["output_unit_count"],
        "error_category": subject_summary["error_category"],
    }
    return [
        _ref(f"source:model:{model_id or hash_value(model_name)[7:23]}", "source_ref", model_summary, ["model_ref_unversioned"]),
        _ref(f"source:message_hash:{subject_summary['prompt_hash'][7:31]}", "source_ref", prompt_summary, ["message_ref_hash_only"]),
        _ref(f"source:model_result:{subject_summary['output_hash'][7:31] if subject_summary['output_hash'] else subject_summary['status']}", "source_ref", output_summary, ["model_result_hash_only"]),
    ]


def _ref(ref_id: str, ref_type: str, summary: Dict[str, Any], partial_reason: List[str]) -> Dict[str, Any]:
    return {
        "ref_id": ref_id,
        "ref_type": ref_type,
        "source_system": MODULE_NAME,
        "summary_hash": hash_value(summary),
        "summary": summary,
        "validity": "observed",
        "version_status": "unversioned",
        "visibility": "visible",
        "partial_reason": partial_reason,
    }


def _build_event(request_context: Dict[str, str], event_type: str, operation: str, now: str, payload: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "event_id": "evt_" + secrets.token_hex(12),
        "event_type": event_type,
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "observed_at": now,
        "emitted_at": now,
        "producer_module": MODULE_NAME,
        "trace_id": request_context.get("trace_id", ""),
        "span_id": request_context.get("span_id", ""),
        "bkn.request.id": request_context.get("request_id", ""),
        "bkn.operation.name": operation,
        "payload": payload,
    }


def _safe_model_params(params: Dict[str, Any]) -> Dict[str, Any]:
    allowed = [
        "stream",
        "top_p",
        "temperature",
        "frequency_penalty",
        "presence_penalty",
        "max_tokens",
        "top_k",
        "response_format",
        "stop",
        "tool_choice",
    ]
    safe = {key: params.get(key) for key in allowed if key in params}
    safe["has_tools"] = bool(params.get("tools"))
    return safe


def _claim_id(operation: str, subject_id: str, value: Dict[str, Any]) -> str:
    return "claim_" + hashlib.sha256(hash_value({
        "operation": operation,
        "subject_id": subject_id,
        "value": value,
    }).encode("utf-8")).hexdigest()[:24]


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def _valid_traceparent(value: str) -> bool:
    parts = value.split("-")
    return (
        len(parts) == 4
        and parts[0] == "00"
        and len(parts[1]) == 32
        and len(parts[2]) == 16
        and parts[1] != "0" * 32
        and parts[2] != "0" * 16
    )
