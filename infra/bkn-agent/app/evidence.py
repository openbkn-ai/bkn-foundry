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
