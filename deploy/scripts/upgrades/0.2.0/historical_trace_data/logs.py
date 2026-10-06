"""Offline, deterministic conversion of frozen 015 management/access rows."""

import copy
import datetime as dt
import json
from pathlib import Path
import re
import uuid


_CONTRACT = json.loads((Path(__file__).parent / "contracts" / "sources.json").read_text())
_TIME = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$")
_STATUS = re.compile(r"^http_([1-5][0-9]{2})$")
_CHANNELS = {"api", "cli", "mcp", "sdk", "studio"}
_READ_ACTIONS = {"read", "list", "get", "search", "query", "refresh", "poll"}


def convert_log(source_id, row, environment, source_deployment):
    """Return a plan item, never publish it or consult current directory state.

    `_provenance` is optional evidence supplied by the frozen-input reader, not
    a live lookup. The caller must include that evidence in its input digest.
    Final schema/registry/age checks use the target native validator in the CLI.
    """
    sidecar = {"codec": _CONTRACT["contract_version"], "provenance": {}, "residue": copy.deepcopy(row)}

    def result(disposition, reason, event=None):
        return {"disposition": disposition, "reason": reason, "event": event, "sidecar": sidecar}

    contract = _CONTRACT["sources"].get(source_id)
    if contract is None:
        return result("blocked", "unknown_source_contract")
    if not isinstance(row, dict):
        return result("blocked", "invalid_source_row")
    if environment not in {"development", "test", "staging", "production"}:
        return result("blocked", "invalid_environment")
    if not isinstance(source_deployment, str) or not source_deployment:
        return result("blocked", "missing_source_deployment")
    evidence = row.get("_provenance", {})
    if not isinstance(evidence, dict):
        return result("blocked", "invalid_provenance")
    provenance = sidecar["provenance"]
    provenance["scope.environment"] = "operator_instance_parameter"
    safe = source_id.startswith("bkn-safe-")
    access = source_id == "bkn-safe-access"
    occurred_at = row.get("created_at" if safe else "event_time")
    if not isinstance(occurred_at, str) or not _TIME.fullmatch(occurred_at):
        return result("archive", "invalid_source_time")
    try:
        dt.datetime.fromisoformat(occurred_at.replace("Z", "+00:00"))
    except ValueError:
        return result("archive", "invalid_source_time")
    provenance["occurred_at"] = "source.created_at" if safe else "source.event_time"
    original_id = row.get("id" if safe else "event_id")
    if not isinstance(original_id, str) or not original_id:
        return result("blocked", "missing_source_identity")
    action = row.get("action", "")
    if action in _READ_ACTIONS:
        return result("archive", "target_scope_excluded")
    if not isinstance(action, str):
        return result("archive", "invalid_action")
    if source_id == "bkn-safe-admin":
        if row.get("method", "").upper() == "SYSTEM":
            return result("archive", "unsupported_safe_system_event")
        status = row.get("status")
        if type(status) is not int or not 200 <= status < 300:
            return result("archive", "uncommitted_safe_request")
        target_type = contract["resource_aliases"].get(row.get("resource"))
        action = action.replace(".", "_").replace("-", "_")
        outcome = "success"
        provenance["http_status"] = "source.status"
    else:
        target_type = "user" if access else contract.get("target_aliases", {}).get(
            row.get("target_type"), row.get("target_type"))
        if target_type not in contract["targets"]:
            return result("archive", "unregistered_target")
        if action not in contract["actions"]:
            return result("archive", "unregistered_action")
        outcome = row.get("outcome")
        status = row.get("http_status")
        if status is not None:
            provenance["http_status"] = "source.http_status"
        if status is None and evidence.get("http_status") is not None:
            status = evidence["http_status"]
            provenance["http_status"] = "frozen_exact_event_supplement"
        code = row.get("failure_code", "")
        encoded = _STATUS.fullmatch(code) if isinstance(code, str) else None
        if contract.get("status_from_failure_code") and encoded:
            encoded_status = int(encoded.group(1))
            expected = "denied" if encoded_status in (401, 403) else "failure" if encoded_status >= 400 else "success"
            if outcome != expected or outcome == "success":
                return result("archive", "inconsistent_source_outcome")
            if status is not None and status != encoded_status:
                return result("blocked", "conflicting_http_status")
            status = encoded_status
            provenance["http_status"] = "source_failure_code"
        if status is None and not access and source_id != "execution-factory":
            return result("archive", "missing_http_status")
    if target_type is None:
        return result("archive", "unregistered_target")
    if not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", action):
        return result("archive", "invalid_action")
    outcomes = {"success", "failure", "denied"}
    if source_id == "execution-factory":
        outcomes.add("unknown")
    if outcome not in outcomes:
        return result("archive", "invalid_outcome")
    if status is not None:
        if type(status) is not int or not 100 <= status <= 599:
            return result("archive", "invalid_http_status")
        expected = "denied" if status in (401, 403) else "failure" if status >= 400 else "success"
        if outcome != "unknown" and outcome != expected:
            return result("archive", "inconsistent_source_outcome")
    actor_id = row.get("actor_id", "")
    actor_type = "user" if access else row.get("actor_type", "")
    if not isinstance(actor_id, str) or not actor_id or actor_id.lower() in {"unknown", "unauthenticated", "anonymous"}:
        return result("archive", "unresolved_actor")
    if actor_type not in {"user", "service_account", "app", "application"}:
        return result("archive", "unresolved_actor_type")
    actor_type = "user" if actor_type == "user" else "service_account"
    if source_id == "model-manager" and evidence.get("actor_authenticated") is not True:
        return result("blocked", "unverified_actor_origin")
    actor_name = row.get("actor_name_snapshot" if safe else "actor_name", "")
    target_id = actor_id if access else row.get("target_id", "")
    target_name = actor_name if access else row.get("target_name", "")
    if not target_id and source_id == "bkn-safe-admin" and row.get("request_id"):
        target_id = target_type + ":" + row["request_id"]
        provenance["target.id"] = "derived_by_native_contract:request_target"
    if not isinstance(target_id, str) or not target_id:
        return result("archive", "missing_target_identity")
    for label, value, identity, maximum in (("actor", actor_name, actor_id, 256),
                                            ("target", target_name, target_id, 512)):
        if not isinstance(value, str) or not value:
            return result("archive", "missing_snapshot")
        if value == identity and evidence.get(label + "_name_snapshot") is not True:
            return result("archive", "ambiguous_snapshot")
        if len(value) > maximum:
            return result("archive", "oversized_snapshot")
    kn_ids = []
    if source_id == "bkn-backend":
        kn = row.get("knowledge_network_id", "")
        own_kn = target_type == "knowledge_network" and kn == target_id
        if not own_kn and evidence.get("knowledge_network_scope_verified") is not True:
            return result("blocked", "unverified_knowledge_network_scope")
        if not isinstance(kn, str) or not kn:
            return result("blocked", "missing_knowledge_network_scope")
        kn_ids = [kn]
        provenance["scope.knowledge_network_ids"] = "source.knowledge_network_id:verified_scope"
    else:
        provenance["scope.platform_scope"] = "derived_by_native_contract"
    request_id = row.get("request_id", "")
    if not isinstance(request_id, str) or (not request_id and not access):
        return result("archive", "missing_request_identity")
    method = "POST" if access else row.get("method", "")
    if not isinstance(method, str) or not method or len(method) > 16:
        return result("archive", "invalid_method")
    auth_method = row.get("auth_method", "") or "unknown"
    if not isinstance(auth_method, str) or len(auth_method) > 64:
        return result("archive", "invalid_auth_method")
    if len(actor_id) > 128 or len(target_id) > 256 or len(request_id) > 128 or any(len(kn) > 128 for kn in kn_ids):
        return result("archive", "oversized_reference")
    channel = row.get("source_channel", "")
    if source_id in {"bkn-backend", "vega", "model-manager"}:
        channel = "api"
        provenance["request_context.source_channel"] = "derived_by_native_contract"
    elif channel not in _CHANNELS:
        channel = "unknown"
        provenance["request_context.source_channel"] = "derived_by_native_contract:unknown_channel"
    if source_id == "bkn-safe-admin" and channel == "unknown":
        return result("archive", "unsupported_source_channel")
    event_name = contract.get("event_name")
    if access:
        event_name = {("login", "success"): "login.succeeded", ("login", "failure"): "login.failed",
                      ("login", "denied"): "login.failed", ("logout", "success"): "logout.succeeded"}.get((action, outcome))
        if event_name is None:
            return result("archive", "unregistered_access_event")
    if contract["identity"] == "publisher_namespace_url":
        event_id = str(uuid.uuid5(uuid.NAMESPACE_URL, original_id))
        identity_algorithm = contract["identity"]
    else:
        try:
            event_id = str(uuid.UUID(original_id))
            identity_algorithm = "preserve_source_uuid"
        except ValueError:
            name = json.dumps([source_deployment, source_id, original_id], separators=(",", ":"), ensure_ascii=False)
            event_id = str(uuid.uuid5(uuid.UUID(_CONTRACT["migration_namespace"]), name))
            identity_algorithm = contract["identity"]
    sidecar["identity_map"] = {"source_deployment": source_deployment, "source_id": source_id,
                               "source_event_id": original_id, "target_event_id": event_id,
                               "algorithm": identity_algorithm}
    facts = {"action": action}
    if access:
        facts["result"] = outcome
    elif source_id != "execution-factory":
        facts["decision"] = "denied" if outcome == "denied" else "allowed"
        provenance["facts.decision"] = "derived_by_native_contract"
    if source_id in {"bkn-backend", "vega"}:
        summary = row.get("change_summary")
        if isinstance(summary, str) and summary:
            try:
                summary = json.loads(summary)
            except ValueError:
                return result("archive", "invalid_change_summary")
        fields = row.get("changed_fields")
        if isinstance(summary, dict) and "changed_fields" in summary:
            fields = summary["changed_fields"]
        if fields is not None:
            if (not isinstance(fields, list) or len(fields) > 100 or
                    any(not isinstance(field, str) or not field or len(field) > 128 for field in fields) or
                    len(set(fields)) != len(fields)):
                return result("archive", "invalid_changed_fields")
            facts["changed_fields"] = copy.deepcopy(fields)
            provenance["facts.changed_fields"] = "source.changed_fields_or_change_summary"
    event = {
        "schema_version": "1.0", "event_id": event_id, "source_id": source_id,
        "category": "access.user" if access else "audit.admin", "event_name": event_name,
        "occurred_at": occurred_at,
        "actor": {"id": actor_id, "type": actor_type, "auth_method": auth_method,
                  "effective_subject": actor_id, "display_name_snapshot": actor_name},
        "target": {"type": target_type, "id": target_id, "name": target_name}, "outcome": outcome,
        "scope": {"business_module": contract["business_module"], "environment": environment,
                  "platform_scope": not bool(kn_ids), "knowledge_network_ids": kn_ids},
        "request_context": {"source_channel": channel, "transport": "http", "method": method.upper()},
        "correlation": {"request_id": request_id} if request_id else {},
        "summary": f"{event_name} {action} {target_type}", "facts": facts,
    }
    if status is not None and not access:
        event["http_status"] = status
    if outcome in {"failure", "denied"}:
        code = f"HTTP_{status}" if status is not None and not access else row.get("failure_code", "").upper().replace("-", "_")
        if not re.fullmatch(r"[A-Z][A-Z0-9_]{0,127}", code):
            return result("archive", "missing_or_invalid_failure_code")
        event["failure_code"] = code
    if len(json.dumps(event, ensure_ascii=False, separators=(",", ":")).encode()) > 32768:
        return result("archive", "oversized_native_event")
    # Retain the whole original row, including mapped fields, for lossless review.
    # Nothing in this sidecar is published into the bounded native event.
    for field in row:
        provenance.setdefault("source." + field, "preserved_in_residue")
    return result("convert", "native_format_candidate", event)
