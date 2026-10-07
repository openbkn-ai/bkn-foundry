"""Offline Agent thread/checkpoint/task conversion; no runtime reader."""

from collections import defaultdict
import copy
from datetime import datetime, timezone
import hashlib
import json
import msgpack

from snapshot import canonical
from native_core import _id, _time, CORE_FIELDS


def _json(value):
    return json.loads(value) if isinstance(value, str) else value


def _task_value(value):
    try:
        return _json(value)
    except json.JSONDecodeError:
        return value


def _stamp(value):
    return _time(value)


def _millis(value):
    return (
        datetime.fromtimestamp(int(value) / 1000, timezone.utc)
        .isoformat(timespec="microseconds")
        .replace("+00:00", "Z")
    )


def decode_messages(blob_hex):
    def extension(code, data):
        if code != 5:
            raise ValueError("unsupported_checkpoint_extension")
        value = msgpack.unpackb(
            data, raw=False, strict_map_key=False, ext_hook=extension
        )
        if (
            not isinstance(value, (list, tuple))
            or len(value) < 3
            or value[0]
            not in (
                "langchain_core.messages.human",
                "langchain_core.messages.ai",
                "langchain_core.messages.tool",
                "langchain_core.messages.system",
            )
            or value[1]
            not in ("HumanMessage", "AIMessage", "ToolMessage", "SystemMessage")
            or not isinstance(value[2], dict)
        ):
            raise ValueError("unsupported_checkpoint_message")
        return value[2]

    value = msgpack.unpackb(
        bytes.fromhex(blob_hex), raw=False, strict_map_key=False, ext_hook=extension
    )
    if not isinstance(value, list) or any(not isinstance(m, dict) for m in value):
        raise ValueError("invalid_checkpoint_messages")
    return value


def _text(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "\n".join(
            block.get("text", "") if isinstance(block, dict) else str(block)
            for block in value
            if isinstance(block, str)
            or isinstance(block, dict)
            and block.get("type") in ("text", "output_text")
        )
    return canonical(value) if value is not None else ""


def _tool_invocations(messages, observed, changed, begin, end):
    results = {
        m["tool_call_id"]: m
        for m in messages
        if m.get("type") == "tool" and m.get("tool_call_id")
    }
    calls = {}
    for m in messages:
        if m.get("type") != "ai":
            continue
        for position, call in enumerate(m.get("tool_calls") or []):
            identity = call.get("id") or _id("call", (m.get("id"), position, call))
            result = results.get(identity)
            started = observed.get(m.get("id"), begin)
            finished = (
                max(started, changed.get(result.get("id"), end))
                if result
                else max(started, end)
            )
            calls[identity] = {
                "id": identity,
                "name": call.get("name") or "tool.call",
                "args": call.get("args", {}),
                "result": result.get("content") if result else None,
                "has_result": result is not None,
                "status": (
                    "completed"
                    if result and result.get("status") != "error"
                    else "failed"
                ),
                "begin": started,
                "end": finished,
            }
    return list(calls.values())


def _payload(value):
    size = len(canonical(value).encode())
    if size > 1 << 20:
        return {
            "mode": "omitted",
            "media_type": "application/json",
            "byte_length": size,
            "omitted_reason": "payload_too_large",
        }
    return {
        "mode": "inline",
        "media_type": "application/json",
        "byte_length": size,
        "inline": value,
    }


def plan_agent_history(records, before, actor_names=None):
    cutoff = _stamp(before)
    actor_names = actor_names or {}
    rows = defaultdict(list)
    for record in records:
        rows[record["kind"]].append(record["row"])
    agents = {
        r["f_agent_id"]: r.get("f_name") or r["f_agent_id"]
        for r in rows["agent_definition"]
    }
    blobs = {
        (r["thread_id"], r.get("checkpoint_ns", ""), r["version"]): r
        for r in rows["agent_blob"]
        if r["channel"] == "messages"
    }
    checkpoints = defaultdict(list)
    for r in rows["agent_checkpoint"]:
        cp = _json(r["checkpoint"])
        if _stamp(cp["ts"]) < cutoff and not r.get("checkpoint_ns"):
            checkpoints[r["thread_id"]].append((cp, r))
    core = {key: [] for key in CORE_FIELDS}
    artifacts = []
    events = []
    defaults = []
    source_map = []

    def conversation(cid, aid, uid, begin, end, external):
        if not uid or not aid:
            raise ValueError("agent_source_owner_missing")
        owner = {
            "application_principal_id": aid,
            "effective_subject_type": "user",
            "effective_subject_id": uid,
            "delegation_id": "",
        }
        core["conversations"].append(
            {
                "conversation_id": cid,
                "owner": owner,
                "agent_name": agents.get(aid, aid),
                "actor_name_snapshot": actor_names.get(uid, uid),
                "creation_auth_method": "unknown",
                "external_conversation_key": external,
                "generation": 1,
                "status": "closed",
                "one_shot": False,
                "row_version": 1,
                "created_at": begin,
                "updated_at": end,
                "closed_at": end,
            }
        )
        return owner

    def round_records(
        cid,
        owner,
        ordinal,
        key,
        begin,
        end,
        question,
        answer,
        input_value,
        output_value,
        status,
        error=None,
        tool_calls=None,
    ):
        iid = _id("int", ("agent-history", cid, key))
        oid = _id("op", (iid, "agent.run"))
        rid = _id("rcpt", (oid, 1))
        request = _id("req", (iid, "request"))
        trace = hashlib.sha256(canonical(("agent-history", iid)).encode()).hexdigest()[
            :32
        ]
        refs = {}
        for kind, value, stamp in [
            ("question", question, begin),
            ("result", answer, end),
        ]:
            if not value:
                continue
            aid = _id("artifact", (iid, kind))
            refs[kind] = "artifact:" + aid
            artifact = {
                "artifact_id": aid,
                "artifact_type": kind,
                "bkn.request.id": request,
                "trace_id": trace,
                "interaction_id": iid,
                "operation_id": "",
                "content_type": "application/json",
                "schema_version": "2.2.0",
                "observed_at": stamp,
                "content": {"text": value},
                "bkn.account.id": owner["effective_subject_id"],
                "bkn.account.type": "user",
                "effective_subject_id": owner["effective_subject_id"],
                "application_principal_id": owner["application_principal_id"],
                "agent_or_app": agents.get(
                    owner["application_principal_id"], owner["application_principal_id"]
                ),
            }
            artifacts.append(artifact)
        core["interactions"].append(
            {
                "interaction_id": iid,
                "conversation_id": cid,
                "ordinal": ordinal,
                "execution_status": status,
                "evidence_status": "partial",
                "closure_manifest": {
                    "completion_manifest_version": "3.0.0",
                    "answer_artifact_ref": refs.get("result", ""),
                    "completion_reason": (
                        "answer_completed"
                        if status == "completed"
                        else "execution_failed"
                    ),
                },
                "row_version": 1,
                "lease_token": "",
                "lease_epoch": 0,
                "lease_version": 0,
                "lease_expires_at": end,
                "created_at": begin,
                "updated_at": end,
                "terminal_at": end,
            }
        )
        core["operations"].append(
            {
                "operation_id": oid,
                "conversation_id": cid,
                "interaction_id": iid,
                "operation_key": oid,
                "tool_name": "agent.run",
                "attempt": 1,
                "attempt_status": "completed" if status == "completed" else "failed",
                "retryable": False,
                "row_version": 1,
                "created_at": begin,
                "updated_at": end,
            }
        )
        receipt = {
            "receipt_id": rid,
            "schema_version": "3.0.0",
            "owner": owner,
            "conversation_id": cid,
            "interaction_id": iid,
            "operation_id": oid,
            "attempt": 1,
            "operation_key": oid,
            "tool_name": "agent.run",
            "receipt_status": "completed" if status == "completed" else "failed",
            "evidence_durability": "durable",
            "required": False,
            "request_id": request,
            "trace_id": trace,
            "causation_event_ids": [],
            "observed_evidence_refs": [],
            "business_refs": [],
            "artifact_refs": list(refs.values()),
            "partial_reasons": (
                [] if question and answer else ["source_content_incomplete"]
            ),
            "row_version": 1,
            "issued_at": begin,
            "terminal_at": end,
        }
        core["receipts"].append(receipt)
        core["call_facts"].append(
            {
                "operation_id": oid,
                "attempt": 1,
                "conversation_id": cid,
                "interaction_id": iid,
                "receipt_id": rid,
                "tool_name": "agent.run",
                "protocol": "internal",
                "source_module": "bkn-agent",
                "input": _payload(input_value),
                "output": _payload(output_value) if output_value is not None else None,
                "error": _payload(error) if error else None,
                "request_id": request,
                "trace_id": trace,
                "started_at": begin,
                "finished_at": end,
                "status": receipt["receipt_status"],
                "retryable": False,
            }
        )
        for kind, stamp, payload in [
            (
                "agent.interaction.started",
                begin,
                {"question_artifact_ref": refs.get("question", "")},
            ),
            (
                "retrieval.completed",
                end,
                {
                    "status": receipt["receipt_status"],
                    "answer_artifact_ref": refs.get("result", ""),
                },
            ),
        ]:
            e = {
                "event_id": _id("evt", (iid, kind)),
                "event_type": kind,
                "bkn.trace.schema.version": "2.2.0",
                "observed_at": stamp,
                "emitted_at": stamp,
                "producer_module": "bkn-agent",
                "trace_id": trace,
                "bkn.request.id": request,
                "interaction_id": iid,
                "operation_id": oid,
                "bkn.operation.name": "agent.run",
                "payload": payload,
            }
            events.append(
                {
                    "kind": "evidence",
                    "source_id": "bkn-agent",
                    "row": {
                        "envelope": {
                            "event": {
                                "event_id": e["event_id"],
                                "trace_id": trace,
                                "request_id": request,
                                "conversation_id": cid,
                                "interaction_id": iid,
                                "operation_id": oid,
                                "envelope": {"event": e, "owner": owner},
                            }
                        }
                    },
                }
            )
        # Checkpoint ToolMessage identities join only their original tool_call_id.
        # They are operations in this user round, never additional interactions.
        for call in tool_calls or []:
            child_oid = _id("op", (iid, "tool-call", call["id"]))
            child_rid = _id("rcpt", (child_oid, 1))
            child_status = call["status"]
            operation = copy.deepcopy(core["operations"][-1])
            operation.update(
                operation_id=child_oid,
                operation_key=child_oid,
                tool_name=call["name"],
                attempt_status=child_status,
                created_at=call["begin"],
                updated_at=call["end"],
            )
            core["operations"].append(operation)
            child_receipt = copy.deepcopy(receipt)
            child_receipt.update(
                receipt_id=child_rid,
                operation_id=child_oid,
                operation_key=child_oid,
                tool_name=call["name"],
                receipt_status=child_status,
                artifact_refs=[],
                issued_at=call["begin"],
                terminal_at=call["end"],
                partial_reasons=(
                    [] if call["has_result"] else ["source_tool_result_missing"]
                ),
            )
            core["receipts"].append(child_receipt)
            fact = copy.deepcopy(core["call_facts"][-1])
            fact.update(
                operation_id=child_oid,
                receipt_id=child_rid,
                parent_operation_id=oid,
                tool_name=call["name"],
                input=_payload(call["args"]),
                output=(
                    _payload(call["result"]) if child_status == "completed" else None
                ),
                error=(
                    _payload(call["result"])
                    if child_status == "failed" and call["has_result"]
                    else None
                ),
                started_at=call["begin"],
                finished_at=call["end"],
                status=child_status,
            )
            core["call_facts"].append(fact)
            child_event = copy.deepcopy(events[-1])
            outer = child_event["row"]["envelope"]["event"]
            event = outer["envelope"]["event"]
            event_id = _id("evt", (child_oid, "retrieval.completed"))
            outer.update(event_id=event_id, operation_id=child_oid)
            event.update(
                event_id=event_id,
                operation_id=child_oid,
                observed_at=call["end"],
                emitted_at=call["end"],
                parent_operation_id=oid,
                **{
                    "bkn.operation.name": call["name"],
                    "payload": {"status": child_status},
                }
            )
            events.append(child_event)
        if not question or not answer:
            defaults.append(
                {
                    "interaction_id": iid,
                    "missing_question": not bool(question),
                    "missing_result": not bool(answer),
                    "native_execution_status": status,
                    "reason": "original_final_content_not_retained",
                }
            )
        return iid

    for thread in sorted(rows["agent_thread"], key=lambda r: r["f_thread_id"]):
        if _millis(thread["f_create_time"]) >= cutoff:
            continue
        tid = thread["f_thread_id"]
        seen = {}
        changed = {}
        message_content = {}
        ordered = []
        latest = None
        for cp, r in sorted(
            checkpoints[tid],
            key=lambda pair: (_stamp(pair[0]["ts"]), pair[1]["checkpoint_id"]),
        ):
            version = cp.get("channel_versions", {}).get("messages")
            if version is None:
                continue
            blob = blobs.get((tid, r.get("checkpoint_ns", ""), str(version)))
            if not blob:
                raise ValueError("checkpoint_message_version_missing")
            current = decode_messages(blob["blob_hex"])
            latest = current
            for index, m in enumerate(current):
                mid = m.get("id") or _id(
                    "msg", (tid, index, m.get("type"), m.get("content"))
                )
                content = canonical(
                    {
                        k: m.get(k)
                        for k in ("content", "tool_calls", "tool_call_id", "status")
                    }
                )
                if message_content.get(mid) != content:
                    changed[mid] = _stamp(cp["ts"])
                    message_content[mid] = content
                if mid not in seen:
                    seen[mid] = _stamp(cp["ts"])
                    ordered.append((mid, m))
        # Latest history is authoritative after checkpoint message replacements.
        if latest is not None:
            latest_by_id = {
                m.get("id")
                or _id("msg", (tid, index, m.get("type"), m.get("content"))): m
                for index, m in enumerate(latest)
            }
            ordered = list(latest_by_id.items())
        rounds = []
        for mid, m in ordered:
            if m.get("type") == "human":
                rounds.append(
                    {
                        "key": mid,
                        "question": _text(m.get("content")),
                        "answer": "",
                        "begin": seen[mid],
                        "end": changed[mid],
                        "messages": [m],
                    }
                )
            elif rounds:
                rounds[-1]["messages"].append(m)
                rounds[-1]["end"] = max(rounds[-1]["end"], changed[mid])
                if (
                    m.get("type") == "ai"
                    and not m.get("tool_calls")
                    and _text(m.get("content"))
                ):
                    rounds[-1]["answer"] = _text(m["content"])
        begin = min(
            (r["begin"] for r in rounds), default=_millis(thread["f_create_time"])
        )
        end = max(
            (r["end"] for r in rounds),
            default=max(begin, _millis(thread["f_update_time"])),
        )
        owner = conversation(
            tid, thread["f_agent_id"], thread["f_account_id"], begin, end, tid
        )
        if not rounds:
            rounds = [
                {
                    "key": "missing",
                    "question": "",
                    "answer": "",
                    "begin": begin,
                    "end": end,
                    "messages": [],
                }
            ]
        iids = []
        for ordinal, r in enumerate(rounds, 1):
            iids.append(
                round_records(
                    tid,
                    owner,
                    ordinal,
                    r["key"],
                    r["begin"],
                    r["end"],
                    r["question"],
                    r["answer"],
                    {"text": r["question"]},
                    (
                        {"text": r["answer"], "messages": r["messages"]}
                        if r["answer"]
                        else None
                    ),
                    "completed" if r["answer"] else "failed",
                    tool_calls=_tool_invocations(
                        r["messages"], seen, changed, r["begin"], r["end"]
                    ),
                )
            )
        source_map.append(
            {
                "source_kind": "agent_thread",
                "source_id": tid,
                "conversation_id": tid,
                "interaction_ids": iids,
                "tool_calls": [
                    {
                        "source_tool_call_id": call["id"],
                        "operation_id": _id("op", (iid, "tool-call", call["id"])),
                    }
                    for iid, r in zip(iids, rounds)
                    for call in _tool_invocations(
                        r["messages"], seen, changed, r["begin"], r["end"]
                    )
                ],
            }
        )

    for task in sorted(rows["agent_task"], key=lambda r: r["f_task_id"]):
        if _millis(task["f_create_time"]) >= cutoff:
            continue
        key = task["f_task_id"]
        cid = _id("conv", ("agent-task", key))
        begin = _millis(task["f_create_time"])
        end = max(begin, _millis(task["f_update_time"]))
        owner = conversation(
            cid, task["f_agent_id"], task["f_account_id"], begin, end, key
        )
        inp = _task_value(task.get("f_input")) or {}
        out = _task_value(task.get("f_output")) if task.get("f_output") else None
        question = _text(inp.get("message")) if isinstance(inp, dict) else _text(inp)
        answer = _text(out)
        status = "completed" if task.get("f_status") == "succeeded" else "failed"
        iid = round_records(
            cid,
            owner,
            1,
            key,
            begin,
            end,
            question,
            answer,
            inp,
            out,
            status,
            (
                _task_value(task.get("f_failure_detail"))
                if task.get("f_failure_detail")
                else None
            ),
        )
        source_map.append(
            {
                "source_kind": "agent_task",
                "source_id": key,
                "parent_thread_id": task.get("f_parent_thread_id"),
                "conversation_id": cid,
                "interaction_ids": [iid],
            }
        )
    return {
        "core": core,
        "artifacts": artifacts,
        "records": events,
        "source_map": source_map,
        "defaults": defaults,
    }


def export_agent_source(source, before):
    """Capture original Agent data, including its explicit binary codec."""
    cutoff = int(
        datetime.fromisoformat(_stamp(before).replace("Z", "+00:00")).timestamp() * 1000
    )
    specs = [
        ("t_agent", "agent_definition", ("f_agent_id", "f_name"), None),
        (
            "t_agent_thread",
            "agent_thread",
            (
                "f_thread_id",
                "f_agent_id",
                "f_account_id",
                "f_create_time",
                "f_update_time",
            ),
            "f_create_time<" + str(cutoff),
        ),
        (
            "t_agent_task",
            "agent_task",
            (
                "f_task_id",
                "f_agent_id",
                "f_account_id",
                "f_parent_thread_id",
                "f_status",
                "f_input",
                "f_output",
                "f_failure_detail",
                "f_create_time",
                "f_update_time",
            ),
            "f_create_time<" + str(cutoff),
        ),
        (
            "checkpoints",
            "agent_checkpoint",
            (
                "thread_id",
                "checkpoint_ns",
                "checkpoint_id",
                "parent_checkpoint_id",
                "type",
                "checkpoint",
                "metadata",
            ),
            "BINARY thread_id IN (SELECT BINARY f_thread_id FROM openbkn.t_agent_thread WHERE f_create_time<"
            + str(cutoff)
            + ")",
        ),
        (
            "checkpoint_blobs",
            "agent_blob",
            ("thread_id", "checkpoint_ns", "channel", "version", "type"),
            "BINARY thread_id IN (SELECT BINARY f_thread_id FROM openbkn.t_agent_thread WHERE f_create_time<"
            + str(cutoff)
            + ") AND channel='messages'",
        ),
    ]
    result = []
    tables = []
    for table, kind, columns, condition in specs:
        found = source.query(
            "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='openbkn' AND TABLE_NAME='"
            + table
            + "'"
        )
        if not found:
            tables.append({"locator": "openbkn." + table, "state": "absent"})
            continue
        if not set(columns).issubset(set(found)):
            raise ValueError("agent_source_schema_incomplete:" + table)
        pairs = [repr(name) + ",`" + name + "`" for name in columns]
        if kind == "agent_blob":
            pairs.append("'blob_hex',HEX(`blob`)")
        sql = (
            "SELECT JSON_OBJECT('source_id','bkn-agent','kind','"
            + kind
            + "','locator','openbkn."
            + table
            + "','row',JSON_OBJECT("
            + ",".join(pairs)
            + ")) FROM openbkn.`"
            + table
            + "`"
        )
        if condition:
            sql += " WHERE " + condition
        result.extend(json.loads(line) for line in source.query(sql))
        tables.append({"locator": "openbkn." + table, "state": "present"})
    return result, tables
