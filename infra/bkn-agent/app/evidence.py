import asyncio
import hashlib
import json
import logging
import re
import uuid
from contextvars import ContextVar
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

import aiohttp

from app import observability
from app.config import config

logger = logging.getLogger("bkn-agent.evidence")

CONTRACT_VERSION = "2.1.0"
_HASH_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
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


@dataclass
class InteractionEvidence:
    interaction_id: str
    started_event: dict[str, Any]
    observed_at: str
    operation_sequence: int = 0
    source_event_ids: list[str] = field(default_factory=list)
    operation_ids: list[str] = field(default_factory=list)
    tool_outputs: list[dict[str, Any]] = field(default_factory=list)
    submission_tail: asyncio.Task | None = None


_interaction: ContextVar[InteractionEvidence | None] = ContextVar("bkn_evidence_interaction", default=None)


def hash_value(value: Any) -> str:
    if isinstance(value, str):
        raw = value
    else:
        raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return "sha256:" + hashlib.sha256(raw.encode("utf-8")).hexdigest()


def _stable_id(*parts: str) -> str:
    value = "\x1f".join(parts).encode("utf-8")
    return hashlib.sha256(value).hexdigest()[:32]


def _valid_hash(value: Any) -> bool:
    return isinstance(value, str) and bool(_HASH_RE.fullmatch(value))


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


def _event(
    event_type: str,
    operation_name: str,
    payload: dict[str, Any],
    *,
    event_id: str | None = None,
    interaction_id: str | None = None,
    operation_id: str | None = None,
    causation_event_id: str | None = None,
    claim_id_value: str | None = None,
    attempt: int = 1,
) -> dict[str, Any] | None:
    ctx = observability.current_context()
    if not ctx:
        return None
    current = _interaction.get()
    resolved_interaction_id = interaction_id or (current.interaction_id if current else None)
    ts = current.observed_at if current else ctx.observed_at
    stable_event_id = _stable_id(
        "event",
        ctx.trace_id,
        ctx.request_id,
        resolved_interaction_id or "",
        operation_id or "",
        event_type,
        claim_id_value or "",
        str(attempt),
    )
    event = {
        "event_id": event_id or f"evt_{stable_event_id}",
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
    causal = {
        "interaction_id": resolved_interaction_id,
        "operation_id": operation_id,
        "causation_event_id": causation_event_id,
        "claim_id": claim_id_value,
        "attempt": attempt if operation_id else None,
    }
    event.update({key: value for key, value in causal.items() if value is not None})
    return event


def begin_interaction(intent: Any, mode: str, agent_id: str, operation_name: str):
    ctx = observability.current_context()
    if not ctx:
        return _interaction.set(None)
    interaction_id = "int_" + _stable_id(
        "interaction", ctx.trace_id, ctx.request_id, mode, agent_id, operation_name
    )
    started = _event(
        "agent.interaction.started",
        operation_name,
        {"intent_hash": hash_value(intent), "mode": mode, "agent_id": agent_id},
        interaction_id=interaction_id,
    )
    if started is None:
        return _interaction.set(None)
    return _interaction.set(
        InteractionEvidence(
            interaction_id=interaction_id,
            started_event=started,
            observed_at=ctx.observed_at,
        )
    )


def end_interaction(token) -> None:
    _interaction.reset(token)


def interaction_started_event() -> dict[str, Any] | None:
    current = _interaction.get()
    return current.started_event if current else None


def has_interaction() -> bool:
    return _interaction.get() is not None


def new_operation() -> tuple[str, str | None]:
    current = _interaction.get()
    if not current:
        return "", None
    current.operation_sequence += 1
    operation_id = "op_" + _stable_id(
        "operation", current.interaction_id, str(current.operation_sequence)
    )
    parent = current.source_event_ids[-1] if current.source_event_ids else current.started_event["event_id"]
    return operation_id, parent


def operation_headers(
    operation_id: str, causation_event_id: str, attempt: int = 1
) -> dict[str, str]:
    current = _interaction.get()
    if not current:
        return {}
    return {
        "bkn-interaction-id": current.interaction_id,
        "bkn-operation-id": operation_id,
        "bkn-causation-event-id": causation_event_id,
        "bkn-attempt": str(max(attempt, 1)),
    }


def record_operation_result(event: dict[str, Any], *, tool_name: str, content: Any) -> None:
    current = _interaction.get()
    if not current:
        return
    event_id = event.get("event_id")
    operation_id = event.get("operation_id")
    if event_id and event_id not in current.source_event_ids:
        current.source_event_ids.append(event_id)
    if operation_id and operation_id not in current.operation_ids:
        current.operation_ids.append(operation_id)
    current.tool_outputs.append({"tool_name": tool_name, "content": content})


def adopted_sources() -> tuple[list[str], list[str], list[dict[str, Any]]]:
    current = _interaction.get()
    if not current:
        return [], [], []
    return list(current.source_event_ids), list(current.operation_ids), list(current.tool_outputs)


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
    current = _interaction.get()
    previous = current.submission_tail if current else None
    if previous is not None and previous.done():
        previous = None
    task = asyncio.create_task(_send_after(previous, batch))
    if current:
        current.submission_tail = task
    _background.add(task)
    task.add_done_callback(_background.discard)


async def _send_after(previous: asyncio.Task | None, batch: dict[str, Any]) -> None:
    if previous is not None:
        try:
            await previous
        except Exception:
            pass
    await _send_batch(batch)


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
    source_event_ids: list[str] | None = None,
    operation_ids: list[str] | None = None,
    causation_event_id: str | None = None,
    **_legacy: Any,
) -> dict[str, Any] | None:
    if (
        not claim_id_value
        or not _valid_hash(claim_hash)
        or not source_event_ids
        or not operation_ids
    ):
        return None
    payload = {
        "claim_id": claim_id_value,
        "claim_type": claim_type,
        "claim_hash": claim_hash,
        "source_event_ids": source_event_ids or [],
        "operation_ids": operation_ids or [],
        "visibility": "visible",
        "version_status": "unversioned",
    }
    return _event(
        "claim.created",
        operation_name,
        payload,
        claim_id_value=claim_id_value,
        causation_event_id=causation_event_id,
    )


def evidence_refs_created(
    *,
    claim_id_value: str,
    evidence_refs: list[dict[str, Any]],
    operation_name: str,
    operation_id: str | None = None,
    causation_event_id: str | None = None,
    **_legacy: Any,
) -> dict[str, Any] | None:
    if not claim_id_value or not evidence_refs:
        return None
    return _event(
        "evidence.refs.created",
        operation_name,
        {"claim_id": claim_id_value, "evidence_refs": evidence_refs},
        claim_id_value=claim_id_value,
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


def business_refs_resolved(
    *,
    claim_id_value: str,
    business_refs: list[dict[str, Any]],
    operation_name: str,
    operation_id: str | None = None,
    causation_event_id: str | None = None,
    **_legacy: Any,
) -> dict[str, Any] | None:
    return _event(
        "business.refs.resolved",
        operation_name,
        {
            "claim_id": claim_id_value,
            "business_refs": business_refs,
            "resolver_status": "resolved" if business_refs else "unresolved",
        },
        claim_id_value=claim_id_value,
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


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
                "validity": "observed",
                "visibility": "visible",
                "version_status": "unversioned",
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


def action_recommended(
    *,
    claim_id_value: str,
    operation_id: str,
    causation_event_id: str,
    action_instance_id: str,
    action_type: str,
    target_refs: list[str],
    reason_hash: str,
    operation_name: str,
    attempt: int = 1,
) -> dict[str, Any] | None:
    if not target_refs or not all(
        (claim_id_value, operation_id, causation_event_id, action_instance_id, action_type)
    ):
        return None
    if not _valid_hash(reason_hash):
        return None
    return _event(
        "action.recommended",
        operation_name,
        {
            "action_instance_id": action_instance_id,
            "action_type": action_type,
            "target_refs": target_refs,
            "reason_hash": reason_hash,
            "status": "recommended",
        },
        operation_id=operation_id,
        causation_event_id=causation_event_id,
        claim_id_value=claim_id_value,
        attempt=attempt,
    )


def action_approval_requested(
    *,
    claim_id_value: str,
    operation_id: str,
    causation_event_id: str,
    action_instance_id: str,
    policy_ref: str,
    operation_name: str,
    attempt: int = 1,
) -> dict[str, Any] | None:
    if not all(
        (claim_id_value, operation_id, causation_event_id, action_instance_id, policy_ref)
    ):
        return None
    return _event(
        "action.approval_requested",
        operation_name,
        {
            "action_instance_id": action_instance_id,
            "policy_ref": policy_ref,
            "status": "approval_requested",
        },
        operation_id=operation_id,
        causation_event_id=causation_event_id,
        claim_id_value=claim_id_value,
        attempt=attempt,
    )


def action_execution_headers(
    *,
    recommended: dict[str, Any],
    approval_requested: dict[str, Any],
    action_type: str,
    policy_ref: str,
    reversible: bool,
) -> dict[str, str]:
    payload = recommended.get("payload") or {}
    if (
        recommended.get("event_type") != "action.recommended"
        or approval_requested.get("event_type") != "action.approval_requested"
        or approval_requested.get("causation_event_id") != recommended.get("event_id")
    ):
        return {}
    return {
        **operation_headers(
            str(recommended.get("operation_id") or ""),
            str(recommended.get("causation_event_id") or ""),
            int(recommended.get("attempt") or 1),
        ),
        "bkn-claim-id": str(recommended.get("claim_id") or ""),
        "bkn-action-instance-id": str(payload.get("action_instance_id") or ""),
        "bkn-action-type": action_type,
        "bkn-action-reversible": "true" if reversible else "false",
        "bkn-action-policy-ref": policy_ref,
        "bkn-action-observed-at": str(recommended.get("observed_at") or ""),
        "bkn-action-approval-requested-event-id": str(
            approval_requested.get("event_id") or ""
        ),
    }


def structured_output_validated(
    *,
    claim_id_value: str,
    schema_hash_value: str | None,
    validation_path: str,
    valid: bool,
    operation_name: str,
) -> None:
    return None


def tool_budget_exhausted(
    *,
    max_tool_calls: int,
    operation_name: str,
    tool_name: str | None = None,
) -> None:
    return None


def tool_called(
    *,
    tool_id: str,
    tool_name: str,
    toolbox_id: str | None,
    args_hash: str,
    operation_name: str,
    operation_id: str,
    causation_event_id: str | None,
) -> dict[str, Any] | None:
    if not _valid_hash(args_hash):
        return None
    return _event(
        "tool.called",
        operation_name,
        {
            "tool_id": tool_id,
            "tool_name": tool_name,
            "args_hash": args_hash,
            "visibility": "visible",
            "version_status": "unversioned",
        },
        operation_id=operation_id,
        causation_event_id=causation_event_id,
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
    operation_id: str,
    causation_event_id: str,
    partial_reason: list[str] | None = None,
) -> dict[str, Any] | None:
    if success and not _valid_hash(result_hash):
        return None
    if not success and not _valid_hash(error_hash):
        return None
    payload: dict[str, Any] = {
        "tool_id": tool_id,
        "tool_name": tool_name,
        "result_hash": result_hash,
        "result_length": result_length,
        "status": "success" if success else "error",
        "error_hash": error_hash,
        "error_category": "tool_execution" if not success else None,
        "visibility": "visible",
        "version_status": "unversioned",
    }
    return _event(
        "tool.result.observed",
        operation_name,
        payload,
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


def agent_as_tool_invoked(
    *,
    parent_thread_id: str | None,
    child_task_id: str,
    child_agent_id: str,
    depth: int,
    message_hash: str,
    operation_name: str,
) -> None:
    return None
