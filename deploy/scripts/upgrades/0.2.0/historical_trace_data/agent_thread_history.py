# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Offline Agent message recovery without inventing executions or technical traces."""
import hashlib
import json
from agent_history import decode_messages, _text, _millis, _payload, _tool_invocations
from core_history_batches import HISTORY_CORE_FIELDS
from native_core import _id, _time
from snapshot import canonical


def _thread_rounds(records, thread_id, before):
    blobs = {
        (r["row"].get("checkpoint_ns", ""), str(r["row"]["version"])): r["row"]
        for r in records
        if r["kind"] == "agent_blob"
        and r["row"]["thread_id"] == thread_id
        and r["row"]["channel"] == "messages"
    }
    checkpoints = []
    for r in records:
        if (
            r["kind"] != "agent_checkpoint"
            or r["row"]["thread_id"] != thread_id
            or r["row"].get("checkpoint_ns")
        ):
            continue
        cp = r["row"]["checkpoint"]
        if isinstance(cp, str):
            cp = json.loads(cp)
        if _time(cp["ts"]) < before:
            checkpoints.append((cp, r["row"]))
    seen = {}
    changed = {}
    content = {}
    latest = []
    for cp, row in sorted(
        checkpoints, key=lambda v: (_time(v[0]["ts"]), v[1]["checkpoint_id"])
    ):
        version = cp.get("channel_versions", {}).get("messages")
        if version is None:
            continue
        blob = blobs.get((row.get("checkpoint_ns", ""), str(version)))
        if blob is None:
            raise ValueError("checkpoint_message_version_missing")
        latest = decode_messages(blob["blob_hex"])
        for index, message in enumerate(latest):
            mid = message.get("id") or _id(
                "msg", (thread_id, index, message.get("type"), message.get("content"))
            )
            value = canonical(
                {
                    k: message.get(k)
                    for k in ("content", "tool_calls", "tool_call_id", "status")
                }
            )
            seen.setdefault(mid, _time(cp["ts"]))
            if content.get(mid) != value:
                changed[mid] = _time(cp["ts"])
                content[mid] = value
    rounds = []
    for index, message in enumerate(latest):
        mid = message.get("id") or _id(
            "msg", (thread_id, index, message.get("type"), message.get("content"))
        )
        if message.get("type") == "human":
            rounds.append(
                {
                    "message_id": mid,
                    "question": _text(message.get("content")),
                    "answer": "",
                    "begin": seen[mid],
                    "end": changed[mid],
                    "messages": [message],
                }
            )
        elif rounds:
            current = rounds[-1]
            current["messages"].append(message)
            current["end"] = max(current["end"], changed[mid])
            if (
                message.get("type") == "ai"
                and not message.get("tool_calls")
                and _text(message.get("content"))
            ):
                current["answer"] = _text(message["content"])
    for row in rounds:
        row["calls"] = _tool_invocations(
            row["messages"], seen, changed, row["begin"], row["end"]
        )
    return rounds


def _tool_context(messages, call_id):
    contexts = []
    for message in messages:
        if message.get("type") != "tool" or message.get("tool_call_id") != call_id:
            continue
        context = (message.get("additional_kwargs") or {}).get("bkn_context")
        if context is not None:
            if not isinstance(context, dict):
                raise ValueError("invalid stored tool execution context")
            contexts.append(context)
    if len({canonical(c) for c in contexts}) > 1:
        raise ValueError("conflicting stored tool execution contexts")
    return contexts[0] if contexts else {}


def _matched_conversation(thread, existing, bindings):
    tid = thread["f_thread_id"]
    binding = bindings.get(tid)
    keys = {tid, "mcp-host:" + hashlib.sha256(tid.encode()).hexdigest()}
    if thread.get("external_conversation_key"):
        keys.add(thread["external_conversation_key"])
    candidates = []
    for conversation in existing.get("conversations", []):
        owner = conversation["owner"]
        if (
            owner.get("effective_subject_type") != "user"
            or owner.get("effective_subject_id") != thread["f_account_id"]
        ):
            continue
        if (
            thread.get("application_principal_id")
            and owner.get("application_principal_id")
            != thread["application_principal_id"]
        ):
            continue
        if binding:
            if conversation["conversation_id"] != binding["conversation_id"]:
                continue
            if (
                binding.get("generation") is not None
                and conversation["generation"] != binding["generation"]
            ):
                continue
        elif conversation.get("external_conversation_key") not in keys:
            continue
        candidates.append(conversation)
    if binding and len(candidates) != 1:
        raise ValueError("thread_lifecycle_binding_owner_or_generation_conflict")
    if len(candidates) > 1:
        raise ValueError("thread_conversation_binding_ambiguous")
    return candidates[0] if candidates else None


def plan_agent_thread_history(
    records, before, existing_core=None, lifecycle_bindings=None, actor_names=None
):
    """Convert retained user rounds and actual calls; tasks are separate inputs.

    Existing Core is reused only by source lifecycle bindings or its exact host
    external key and owner. Host linkage alone never lends a question to an
    ordinal: lifecycle_bindings must explicitly map HumanMessage IDs to native
    Interaction IDs. Matching content/timestamps are never identity evidence.
    """
    before = _time(before)
    existing_core = existing_core or {}
    lifecycle_bindings = lifecycle_bindings or {}
    actor_names = actor_names or {}
    core = {key: [] for key in HISTORY_CORE_FIELDS}
    artifacts = []
    source_map = []
    defaults = []
    issues = []
    recovery = []
    message_events = []
    derived_count = 0
    tool_count = 0
    agents = {
        r["row"]["f_agent_id"]: r["row"].get("f_name") or r["row"]["f_agent_id"]
        for r in records
        if r["kind"] == "agent_definition"
    }
    interactions = {
        r["interaction_id"]: r for r in existing_core.get("interactions", [])
    }
    for record in sorted(
        (r for r in records if r["kind"] == "agent_thread"),
        key=lambda r: r["row"]["f_thread_id"],
    ):
        thread = record["row"]
        tid = thread["f_thread_id"]
        if _millis(thread["f_create_time"]) >= before:
            continue
        rounds = _thread_rounds(records, tid, before)
        matched = _matched_conversation(thread, existing_core, lifecycle_bindings)
        if matched:
            bindings = lifecycle_bindings.get(tid, {}).get("interaction_ids", {})
            iids = []
            for row in rounds:
                iid = bindings.get(row["message_id"])
                if iid is None:
                    issues.append(
                        {
                            "source_kind": "agent_thread",
                            "source_id": tid,
                            "source_message_id": row["message_id"],
                            "reason": "thread_round_binding_missing",
                        }
                    )
                    continue
                target = interactions.get(iid)
                if (
                    target is None
                    or target["conversation_id"] != matched["conversation_id"]
                ):
                    raise ValueError("thread_interaction_binding_conflict")
                recovery.append(
                    {
                        "source_kind": "agent_thread",
                        "source_id": tid,
                        "source_message_id": row["message_id"],
                        "conversation_id": matched["conversation_id"],
                        "interaction_id": iid,
                        "question": row["question"],
                        "answer": row["answer"],
                    }
                )
                iids.append(iid)
            source_map.append(
                {
                    "source_kind": "agent_thread",
                    "source_id": tid,
                    "conversation_id": matched["conversation_id"],
                    "interaction_ids": iids,
                    "state": "existing_core_reused",
                    "generation": matched["generation"],
                }
            )
            continue
        owner = {
            "application_principal_id": thread.get("application_principal_id")
            or "bkn-agent",
            "effective_subject_type": "user",
            "effective_subject_id": thread["f_account_id"],
            "delegation_id": "",
        }
        if not owner["effective_subject_id"]:
            raise ValueError("agent_thread_owner_missing")
        if not thread.get("application_principal_id"):
            defaults.append(
                {
                    "source_kind": "agent_thread",
                    "source_id": tid,
                    "conversation_id": tid,
                    "field": "application_principal_id",
                    "value": "bkn-agent",
                    "reason": "source_application_principal_missing",
                }
            )
        begin = _millis(thread["f_create_time"])
        end = max(begin, _millis(thread["f_update_time"]))
        core["conversations"].append(
            {
                "conversation_id": tid,
                "owner": owner,
                "agent_name": agents.get(thread["f_agent_id"], thread["f_agent_id"]),
                "actor_name_snapshot": actor_names.get(
                    thread["f_account_id"], thread["f_account_id"]
                ),
                "creation_auth_method": "unknown",
                "external_conversation_key": "mcp-host:"
                + hashlib.sha256(tid.encode()).hexdigest(),
                "generation": 1,
                "status": "active",
                "one_shot": False,
                "row_version": 1,
                "created_at": begin,
                "updated_at": end,
                "closed_at": None,
            }
        )
        defaults.append(
            {
                "source_kind": "agent_thread",
                "source_id": tid,
                "conversation_id": tid,
                "field": "status",
                "value": "active",
                "reason": "source_thread_has_no_closure_state",
            }
        )
        iids = []
        call_map = []
        reply_map = []
        for ordinal, row in enumerate(rounds, 1):
            iid = _id("int", ("agent-thread", tid, row["message_id"]))
            iids.append(iid)
            request = _id("req", ("agent-thread-message", tid, row["message_id"]))
            defaults.append(
                {
                    "source_kind": "agent_thread",
                    "source_id": tid,
                    "interaction_id": iid,
                    "field": "artifact.request_id",
                    "value": request,
                    "reason": "source_request_id_missing_artifact_marker_only",
                }
            )
            refs = {}
            question_hash = ""
            for kind, text, stamp in (
                ("question", row["question"], row["begin"]),
                ("result", row["answer"], row["end"]),
            ):
                if not text:
                    if kind != "question":
                        continue
                    text = "[Question content not recorded in source]"
                    defaults.append(
                        {
                            "source_kind": "agent_thread",
                            "source_id": tid,
                            "source_message_id": row["message_id"],
                            "interaction_id": iid,
                            "field": "question_artifact.content.text",
                            "original_value": "",
                            "value": text,
                            "reason": "source_human_content_empty",
                        }
                    )
                aid = _id("artifact", (iid, kind))
                refs[kind] = "artifact:" + aid
                if kind == "question":
                    encoded = canonical(text).replace("\u2028", "\\u2028").replace("\u2029", "\\u2029")
                    question_hash = "sha256:" + hashlib.sha256(encoded.encode()).hexdigest()
                artifacts.append(
                    {
                        "artifact_id": aid,
                        "artifact_type": kind,
                        "bkn.request.id": request,
                        "trace_id": "",
                        "interaction_id": iid,
                        "operation_id": "",
                        "content_type": "application/json",
                        "schema_version": "2.2.0",
                        "observed_at": stamp,
                        "content": text,
                        "bkn.account.id": owner["effective_subject_id"],
                        "bkn.account.type": "user",
                        "effective_subject_id": owner["effective_subject_id"],
                        "application_principal_id": owner["application_principal_id"],
                        "agent_or_app": agents.get(
                            thread["f_agent_id"], thread["f_agent_id"]
                        ),
                    }
                )
            status = "completed" if row["answer"] else "abandoned"
            if not row["answer"]:
                defaults.append(
                    {
                        "source_kind": "agent_thread",
                        "source_id": tid,
                        "interaction_id": iid,
                        "field": "execution_status",
                        "value": "abandoned",
                        "reason": "source_final_answer_missing",
                    }
                )
            core["interactions"].append(
                {
                    "interaction_id": iid,
                    "conversation_id": tid,
                    "ordinal": ordinal,
                    "execution_status": status,
                    "evidence_status": "partial",
                    "closure_manifest": {
                        "completion_manifest_version": "3.0.0",
                        "answer_artifact_ref": refs.get("result", ""),
                        "completion_reason": (
                            "answer_completed"
                            if row["answer"]
                            else "execution_incomplete"
                        ),
                    },
                    "row_version": 1,
                    "lease_token": "",
                    "lease_epoch": 0,
                    "lease_version": 0,
                    "lease_expires_at": row["end"],
                    "created_at": row["begin"],
                    "updated_at": row["end"],
                    "terminal_at": row["end"],
                }
            )
            # Offline-derived persisted reply, distinct from original tool calls.
            # This internal operation links real messages to native artifacts;
            # it carries no claim about a model/MCP execution or technical trace.
            oid = _id("op", ("agent-thread-reply", tid, row["message_id"]))
            rid = _id("rcpt", (oid, 1))
            reply_status = "completed" if row["answer"] else "pending"
            finished = row["end"] if row["answer"] else None
            key = "agent-thread-reply:" + row["message_id"]
            core["operations"].append(
                {
                    "operation_id": oid,
                    "conversation_id": tid,
                    "interaction_id": iid,
                    "operation_key": key,
                    "parent_operation_id": None,
                    "tool_name": "bkn.agent.chat",
                    "attempt": 1,
                    "attempt_status": reply_status,
                    "retryable": False,
                    "row_version": 1,
                    "created_at": row["begin"],
                    "updated_at": row["end"],
                }
            )
            core["receipts"].append(
                {
                    "receipt_id": rid,
                    "schema_version": "3.0.0",
                    "owner": owner,
                    "conversation_id": tid,
                    "interaction_id": iid,
                    "operation_id": oid,
                    "attempt": 1,
                    "operation_key": key,
                    "tool_name": "bkn.agent.chat",
                    "receipt_status": reply_status,
                    "evidence_durability": "durable",
                    "required": False,
                    "request_id": "",
                    "trace_id": "",
                    "causation_event_ids": [],
                    "observed_evidence_refs": [],
                    "business_refs": [],
                    "artifact_refs": list(refs.values()),
                    "partial_reasons": ["offline_derived_persisted_reply"],
                    "row_version": 1,
                    "issued_at": row["begin"],
                    "terminal_at": finished,
                }
            )
            core["call_facts"].append(
                {
                    "operation_id": oid,
                    "attempt": 1,
                    "conversation_id": tid,
                    "interaction_id": iid,
                    "receipt_id": rid,
                    "tool_name": "bkn.agent.chat",
                    "parent_operation_id": None,
                    "protocol": "internal",
                    "source_module": "bkn-agent",
                    "input": _payload(row["question"]),
                    "output": _payload(row["answer"]) if row["answer"] else None,
                    "error": None,
                    "request_id": "",
                    "trace_id": "",
                    "span_id": "",
                    "started_at": row["begin"],
                    "finished_at": finished,
                    "status": reply_status,
                    "retryable": False,
                }
            )
            reply_map.append(
                {
                    "source_message_id": row["message_id"],
                    "operation_id": oid,
                    "receipt_id": rid,
                }
            )
            derived_count += 1
            event_id = _id("evt", ("agent-thread-message", tid, row["message_id"]))
            message_events.append(
                {
                    "event_id": event_id,
                    "conversation_id": tid,
                    "interaction_id": iid,
                    "owner": owner,
                    "producer_id": "historical-agent-thread-converter",
                    "producer_stream_id": "agent-thread:" + tid,
                    "producer_epoch": 1,
                    "producer_sequence": ordinal,
                    "started_at": row["begin"],
                    "observed_at": row["begin"],
                    "emitted_at": row["begin"],
                    "ingested_at": row["begin"],
                    "event_type": "agent.interaction.started",
                    "payload": {
                        "question_artifact_ref": refs.get("question", ""),
                        "content_hash": question_hash,
                        "intent_hash": hashlib.sha256(
                            row["question"].encode()
                        ).hexdigest(),
                        "mode": "chat",
                        "agent_id": thread["f_agent_id"],
                    },
                }
            )
            defaults.append(
                {
                    "source_kind": "agent_thread",
                    "source_id": tid,
                    "source_message_id": row["message_id"],
                    "interaction_id": iid,
                    "operation_id": oid,
                    "event_id": event_id,
                    "reason": "offline_derived_reply_and_message_event_from_stored_messages",
                    "fields": {
                        "tool_name": "bkn.agent.chat",
                        "protocol": "internal",
                        "request_id": "",
                        "trace_id": "",
                        "span_id": "",
                        "timestamp_semantics": "checkpoint_message_observation",
                        "producer_id": "historical-agent-thread-converter",
                        "producer_epoch": 1,
                        "producer_sequence": ordinal,
                    },
                }
            )
            for call in row["calls"]:
                tool_count += 1
                context = _tool_context(row["messages"], call["id"])
                call_request = context.get("request_id") or ""
                trace_id = context.get("trace_id") or ""
                span_id = context.get("span_id") or ""
                protocol = context.get("protocol") or "internal"
                attempt = context.get("attempt", 1)
                oid = context.get("operation_id") or _id(
                    "op", ("agent-thread-tool", tid, row["message_id"], call["id"])
                )
                rid = context.get("receipt_id") or _id("rcpt", (oid, attempt))
                call_status = call["status"] if call["has_result"] else "pending"
                finished = call["end"] if call["has_result"] else None
                parent = context.get("parent_operation_id")
                core["operations"].append(
                    {
                        "operation_id": oid,
                        "conversation_id": tid,
                        "interaction_id": iid,
                        "operation_key": call["id"],
                        "parent_operation_id": parent,
                        "tool_name": call["name"],
                        "attempt": attempt,
                        "attempt_status": call_status,
                        "retryable": False,
                        "row_version": 1,
                        "created_at": call["begin"],
                        "updated_at": call["end"],
                    }
                )
                core["receipts"].append(
                    {
                        "receipt_id": rid,
                        "schema_version": "3.0.0",
                        "owner": owner,
                        "conversation_id": tid,
                        "interaction_id": iid,
                        "operation_id": oid,
                        "attempt": attempt,
                        "operation_key": call["id"],
                        "tool_name": call["name"],
                        "receipt_status": call_status,
                        "evidence_durability": "durable",
                        "required": False,
                        "request_id": call_request,
                        "trace_id": trace_id,
                        "causation_event_ids": [],
                        "observed_evidence_refs": [],
                        "business_refs": [],
                        "artifact_refs": list(refs.values()),
                        "partial_reasons": (
                            [] if trace_id else ["source_trace_context_missing"]
                        ),
                        "row_version": 1,
                        "issued_at": call["begin"],
                        "terminal_at": finished,
                    }
                )
                core["call_facts"].append(
                    {
                        "operation_id": oid,
                        "attempt": attempt,
                        "conversation_id": tid,
                        "interaction_id": iid,
                        "receipt_id": rid,
                        "tool_name": call["name"],
                        "parent_operation_id": parent,
                        "protocol": protocol,
                        "source_module": "bkn-agent",
                        "input": _payload(call["args"]),
                        "output": (
                            _payload(call["result"])
                            if call_status == "completed"
                            else None
                        ),
                        "error": (
                            _payload(call["result"])
                            if call_status == "failed" and call["has_result"]
                            else None
                        ),
                        "request_id": call_request,
                        "trace_id": trace_id,
                        "span_id": span_id,
                        "started_at": call["begin"],
                        "finished_at": finished,
                        "status": call_status,
                        "retryable": False,
                    }
                )
                call_map.append(
                    {
                        "source_tool_call_id": call["id"],
                        "operation_id": oid,
                        "receipt_id": rid,
                    }
                )
                defaults.append(
                    {
                        "source_kind": "agent_thread",
                        "source_id": tid,
                        "interaction_id": iid,
                        "operation_id": oid,
                        "fields": {
                            key: value
                            for key, value in {
                                "attempt": 1,
                                "protocol": "internal",
                                "request_id": "",
                                "trace_id": "",
                                "span_id": "",
                            }.items()
                            if not context.get(key)
                        },
                        "reason": "source_tool_execution_context_missing",
                    }
                )
        source_map.append(
            {
                "source_kind": "agent_thread",
                "source_id": tid,
                "conversation_id": tid,
                "interaction_ids": iids,
                "tool_calls": call_map,
                "derived_reply_operations": reply_map,
                "state": "original_messages_converted",
            }
        )
    return {
        "core": core,
        "artifacts": artifacts,
        "records": [],
        "message_events": message_events,
        "source_map": source_map,
        "defaults": defaults,
        "issues": issues,
        "message_content_recovery": recovery,
        "stats": {
            "thread_count": len(source_map),
            "derived_reply_operation_count": derived_count,
            "original_tool_call_count": tool_count,
            "excluded_task_count": sum(r["kind"] == "agent_task" for r in records),
        },
    }
