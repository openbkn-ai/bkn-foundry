import asyncio
import hashlib
import json
import logging
import uuid
from datetime import datetime, timezone
from typing import Any

import aiohttp

from app import observability
from app.config import config

logger = logging.getLogger("bkn-agent.evidence")

CONTRACT_VERSION = "2.0.0"
_background: set[asyncio.Task] = set()
_BUSINESS_REF_FIELDS: dict[str, tuple[str, str]] = {
    "kn_id": ("object", "bkn"),
    "knowledge_network_id": ("object", "bkn"),
    "object_type": ("object", "bkn"),
    "object_type_id": ("object", "bkn"),
    "ot_id": ("object", "bkn"),
    "property": ("property", "bkn"),
    "property_id": ("property", "bkn"),
    "property_name": ("property", "bkn"),
    "relation_type": ("relation", "bkn"),
    "relation_type_id": ("relation", "bkn"),
    "rt_id": ("relation", "bkn"),
    "action_type": ("action", "bkn"),
    "action_type_id": ("action", "bkn"),
    "logic_property": ("logic", "bkn"),
    "logical_property": ("logic", "bkn"),
    "metric_id": ("metric", "bkn"),
    "catalog_id": ("data", "vega-data"),
    "resource_id": ("data", "vega-data"),
    "data_view_id": ("data", "vega-data"),
    "table_id": ("data", "vega-data"),
    "field_id": ("data", "vega-data"),
    "field_name": ("data", "vega-data"),
}


def hash_value(value: Any) -> str:
    if isinstance(value, str):
        raw = value
    else:
        raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return "sha256:" + hashlib.sha256(raw.encode("utf-8")).hexdigest()


def schema_hash(schema: dict[str, Any] | None) -> str | None:
    if not schema:
        return None
    return hash_value(schema)


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def _span_id(ctx: observability.TraceContext) -> str:
    _, span_id = observability.parse_traceparent(ctx.traceparent)
    return span_id or uuid.uuid4().hex[:16]


def claim_id(kind: str, subject_id: str, value: Any) -> str:
    digest = hashlib.sha256(
        json.dumps(
            {"kind": kind, "subject_id": subject_id, "hash": hash_value(value)},
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        ).encode("utf-8")
    ).hexdigest()[:24]
    return f"claim_{digest}"


def _event(event_type: str, operation_name: str, payload: dict[str, Any]) -> dict[str, Any] | None:
    ctx = observability.current_context()
    if not ctx:
        return None
    ts = _now()
    return {
        "event_id": f"evt_{uuid.uuid4().hex}",
        "event_type": event_type,
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "observed_at": ts,
        "emitted_at": ts,
        "producer_module": observability.MODULE_NAME,
        "trace_id": ctx.trace_id,
        "span_id": _span_id(ctx),
        "bkn.request.id": ctx.request_id,
        "bkn.operation.name": operation_name,
        "payload": {k: v for k, v in payload.items() if v is not None},
    }


def build_batch(events: list[dict[str, Any]], account_id: str, account_type: str) -> dict[str, Any] | None:
    ctx = observability.current_context()
    if not ctx or not events:
        return None
    return {
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "trace": {
            "trace_id": ctx.trace_id,
            "traceparent": ctx.traceparent,
            "bkn.request.id": ctx.request_id,
            "business_domain": account_id,
            "bkn.account.id": account_id,
            "bkn.account.type": account_type,
        },
        "events": events,
    }


async def submit_events(events: list[dict[str, Any]], account_id: str, account_type: str) -> None:
    if not account_id or not account_type:
        return
    batch = build_batch(events, account_id, account_type)
    if not batch or not config.BKN_TRACE_EVIDENCE_INGEST_URL:
        return
    task = asyncio.create_task(_send_batch(batch))
    _background.add(task)
    task.add_done_callback(_background.discard)


async def _send_batch(batch: dict[str, Any]) -> None:
    try:
        async with aiohttp.ClientSession(
            timeout=aiohttp.ClientTimeout(total=config.BKN_TRACE_EVIDENCE_TIMEOUT_S)
        ) as session:
            async with session.post(config.BKN_TRACE_EVIDENCE_INGEST_URL, json=batch) as resp:
                if resp.status >= 400:
                    logger.warning("BKN Trace evidence ingestion rejected: HTTP %s", resp.status)
    except Exception as exc:
        logger.warning("BKN Trace evidence ingestion unavailable: %s", exc)


def claim_created(
    *,
    claim_id_value: str,
    claim_type: str,
    claim_hash: str,
    operation_name: str,
    visibility: str = "visible",
    version_status: str = "unversioned",
    subject_refs: dict[str, Any] | None = None,
    partial_reason: list[str] | None = None,
) -> dict[str, Any] | None:
    payload: dict[str, Any] = {
        "claim_id": claim_id_value,
        "claim_type": claim_type,
        "claim_hash": claim_hash,
        "visibility": visibility,
        "version_status": version_status,
        "subject_refs": subject_refs or {},
    }
    if partial_reason:
        payload["partial_reason"] = partial_reason
    return _event("claim.created", operation_name, payload)


def evidence_refs_created(
    *,
    claim_id_value: str,
    evidence_refs: list[dict[str, Any]],
    operation_name: str,
    partial_reason: list[str] | None = None,
) -> dict[str, Any] | None:
    payload: dict[str, Any] = {
        "claim_id": claim_id_value,
        "evidence_refs": evidence_refs,
    }
    if partial_reason:
        payload["partial_reason"] = partial_reason
    return _event("evidence.refs.created", operation_name, payload)


def business_refs_resolved(
    *,
    claim_id_value: str,
    business_refs: list[dict[str, Any]],
    operation_name: str,
    partial_reason: list[str] | None = None,
) -> dict[str, Any] | None:
    payload: dict[str, Any] = {
        "claim_id": claim_id_value,
        "business_refs": business_refs,
    }
    if partial_reason:
        payload["partial_reason"] = partial_reason
    return _event("business.refs.resolved", operation_name, payload)


def extract_business_refs_from_tool_outputs(tool_outputs: list[dict[str, Any]], max_refs: int = 20) -> list[dict[str, Any]]:
    refs: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str]] = set()

    def add_ref(key: str, value: Any, tool_name: str) -> None:
        if len(refs) >= max_refs or not isinstance(value, str) or not value.strip() or len(value) > 200:
            return
        ref_type, source_system = _BUSINESS_REF_FIELDS[key]
        ref_value = value.strip()
        dedupe_key = (ref_type, source_system, ref_value)
        if dedupe_key in seen:
            return
        seen.add(dedupe_key)
        ref_hash = hash_value({"type": ref_type, "source": source_system, "value": ref_value})
        refs.append(
            {
                "ref_id": f"{ref_type}:{ref_hash[7:23]}",
                "ref_type": ref_type,
                "source_system": source_system,
                "summary_hash": ref_hash,
                "label": ref_value,
                "tool_name": tool_name,
                "validity": "observed",
                "visibility": "visible",
                "version_status": "unversioned",
                "resolver_status": "unresolved",
                "partial_reason": ["business_ref_unversioned"],
            }
        )

    def walk(value: Any, tool_name: str, depth: int = 0) -> None:
        if depth > 5 or len(refs) >= max_refs:
            return
        if isinstance(value, dict):
            for key, child in value.items():
                normalized_key = str(key).lower()
                if normalized_key in _BUSINESS_REF_FIELDS:
                    add_ref(normalized_key, child, tool_name)
                walk(child, tool_name, depth + 1)
        elif isinstance(value, list):
            for item in value[:50]:
                walk(item, tool_name, depth + 1)

    for output in tool_outputs:
        tool_name = str(output.get("tool_name") or "")
        content = output.get("content")
        parsed: Any = content
        if isinstance(content, str):
            try:
                parsed = json.loads(content)
            except ValueError:
                continue
        walk(parsed, tool_name)
    return refs


def structured_output_validated(
    *,
    claim_id_value: str,
    schema_hash_value: str | None,
    validation_path: str,
    valid: bool,
    operation_name: str,
) -> dict[str, Any] | None:
    return _event(
        "structured_output.validated",
        operation_name,
        {
            "claim_id": claim_id_value,
            "schema_hash": schema_hash_value,
            "validation_result": "valid" if valid else "invalid",
            "validation_path": validation_path,
        },
    )


def tool_budget_exhausted(
    *,
    max_tool_calls: int,
    operation_name: str,
    tool_name: str | None = None,
) -> dict[str, Any] | None:
    return _event(
        "tool.budget.exhausted",
        operation_name,
        {
            "max_tool_calls": max_tool_calls,
            "tool_name": tool_name,
            "partial_reason": ["tool_budget_exhausted"],
        },
    )


def tool_called(
    *,
    tool_id: str,
    tool_name: str,
    toolbox_id: str | None,
    args_hash: str,
    operation_name: str,
) -> dict[str, Any] | None:
    return _event(
        "tool.called",
        operation_name,
        {
            "tool_id": tool_id,
            "tool_name": tool_name,
            "toolbox_id": toolbox_id,
            "args_hash": args_hash,
            "visibility": "visible",
            "version_status": "unversioned",
        },
    )


def tool_result_observed(
    *,
    tool_id: str,
    tool_name: str,
    toolbox_id: str | None,
    result_hash: str | None,
    result_length: int | None,
    success: bool,
    operation_name: str,
    status_code: int | None = None,
    error_hash: str | None = None,
    partial_reason: list[str] | None = None,
) -> dict[str, Any] | None:
    payload: dict[str, Any] = {
        "tool_id": tool_id,
        "tool_name": tool_name,
        "toolbox_id": toolbox_id,
        "result_hash": result_hash,
        "result_length": result_length,
        "status": "success" if success else "failed",
        "status_code": status_code,
        "error_hash": error_hash,
        "visibility": "visible",
        "version_status": "unversioned",
    }
    if partial_reason:
        payload["partial_reason"] = partial_reason
    return _event("tool.result.observed", operation_name, payload)


def agent_as_tool_invoked(
    *,
    parent_thread_id: str | None,
    child_task_id: str,
    child_agent_id: str,
    depth: int,
    message_hash: str,
    operation_name: str,
) -> dict[str, Any] | None:
    return _event(
        "agent_as_tool.invoked",
        operation_name,
        {
            "parent_thread_id": parent_thread_id,
            "child_task_id": child_task_id,
            "child_agent_id": child_agent_id,
            "depth": depth,
            "message_hash": message_hash,
        },
    )
