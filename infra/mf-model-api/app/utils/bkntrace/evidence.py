import asyncio
import hashlib
import json
import os
import secrets
from datetime import datetime, timezone
from typing import Any, Dict, Iterable, List, Optional

import aiohttp


CONTRACT_VERSION = "2.1.0"
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


def build_request_context(headers: Optional[Dict[str, str]], account_id: str = "", account_type: str = "user") -> Dict[str, Any]:
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
        "interaction_id": normalized.get("bkn-interaction-id", "").strip(),
        "operation_id": normalized.get("bkn-operation-id", "").strip(),
        "causation_event_id": normalized.get("bkn-causation-event-id", "").strip(),
        "attempt": _positive_int(normalized.get("bkn-attempt", "1")),
    }


def build_model_call_events(
    request_context: Dict[str, Any],
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
    if not all(request_context.get(key) for key in ("interaction_id", "operation_id", "causation_event_id")):
        return []
    safe_params = _safe_model_params(params)
    normalized_status = "ok" if status == "success" else "error"
    prompt_hash = hash_value({"messages": list(messages or []), "params": safe_params})
    output_hash = hash_value(
        output if output is not None else {"status": normalized_status, "error_category": error_category}
    )
    payload = {
        "model_name": model_name,
        "model_provider": model_provider,
        "status": normalized_status,
        "input_token_count": int(input_token_count or 0),
        "output_token_count": int(output_token_count or 0),
        "prompt_hash": prompt_hash,
        "output_hash": output_hash,
    }
    if normalized_status == "error":
        payload["error_category"] = error_category or "unknown"
        payload["error_hash"] = hash_value({"operation": operation, "category": error_category or "unknown"})
    now = _utc_now()
    return [
        _build_event(request_context, "model.call.observed", operation, now, payload),
    ]


def emit_model_call_events(request_context: Dict[str, Any], events: List[Dict[str, Any]]) -> None:
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


def _build_event(request_context: Dict[str, Any], event_type: str, operation: str, now: str, payload: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "event_id": _event_id(request_context, event_type),
        "event_type": event_type,
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "observed_at": now,
        "emitted_at": now,
        "producer_module": MODULE_NAME,
        "trace_id": request_context.get("trace_id", ""),
        "span_id": request_context.get("span_id", ""),
        "bkn.request.id": request_context.get("request_id", ""),
        "bkn.operation.name": operation,
        "interaction_id": request_context.get("interaction_id", ""),
        "operation_id": request_context.get("operation_id", ""),
        "causation_event_id": request_context.get("causation_event_id", ""),
        "attempt": int(request_context.get("attempt", 1) or 1),
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


def _event_id(request_context: Dict[str, Any], event_type: str) -> str:
    identity = ":".join([
        request_context.get("trace_id", ""),
        request_context.get("operation_id", ""),
        event_type,
        str(request_context.get("attempt", 1)),
    ])
    return "evt_" + hashlib.sha256(identity.encode("utf-8")).hexdigest()[:24]


def _positive_int(value: Any) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        return 1
    return parsed if parsed > 0 else 1


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
