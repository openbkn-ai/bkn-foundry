"""Bounded model-management audit facts; inference and model tests stay Trace-only."""
from app.commons.i18n.audit import _ACTION_NAMES, _TARGET_NAMES, SEPARATOR, BATCH, TRUNCATED
import json
import hashlib
import logging
import re
import secrets
import uuid
from datetime import datetime, timezone

from app.commons.get_user_info import get_username_by_ids

logger = logging.getLogger(__name__)
_audit_publisher = None
_SECRET_SHAPED_VALUE = re.compile(r"(?i)(?:bearer\s+[a-z0-9._~-]{8,}|bkn_[a-z0-9._~-]{8,}|^bak_[a-z0-9._-]{12,}$)")


def _audit_event_id():
    timestamp_ms = int(datetime.now(timezone.utc).timestamp() * 1000)
    random_bits = secrets.randbits(74)
    value = ((timestamp_ms & ((1 << 48) - 1)) << 80)
    value |= 0x7 << 76
    value |= ((random_bits >> 62) & 0xFFF) << 64
    value |= 0b10 << 62
    value |= random_bits & ((1 << 62) - 1)
    return str(uuid.UUID(int=value))


def _audit_request_id(request_id):
    if len(request_id) <= 128 and not _SECRET_SHAPED_VALUE.search(request_id):
        return request_id
    return "req_" + hashlib.sha256(request_id.encode("utf-8")).hexdigest()


def set_audit_publisher(publisher):
    global _audit_publisher
    _audit_publisher = publisher

_RULES = {
    ("POST", "/api/mf-model-manager/v1/llm/add"): ("create", "llm_model"),
    ("POST", "/api/mf-model-manager/v1/llm/edit"): ("update", "llm_model"),
    ("POST", "/api/mf-model-manager/v1/llm/delete"): ("delete", "llm_model"),
    ("POST", "/api/mf-model-manager/v1/llm/default/edit"): ("set_default", "llm_model"),
    ("POST", "/api/mf-model-manager/v1/small-model/add"): ("create", "small_model"),
    ("POST", "/api/mf-model-manager/v1/small-model/edit"): ("update", "small_model"),
    ("POST", "/api/mf-model-manager/v1/small-model/delete"): ("delete", "small_model"),
    ("POST", "/api/mf-model-manager/v1/small-model/set-default"): ("set_default", "small_model"),
    ("POST", "/api/mf-model-manager/v1/model-quota"): ("create", "model_quota"),
    ("POST", "/api/mf-model-manager/v1/model-quota/{conf_id}"): ("update", "model_quota"),
    ("POST", "/api/mf-model-manager/v1/user-quota"): ("create", "user_model_quota"),
    ("POST", "/api/mf-model-manager/v1/user-quota/delete"): ("delete", "user_model_quota"),
}

def _rule(request):
    path = request.url.path
    if path.startswith("/api/mf-model-manager/v1/model-quota/"):
        path = "/api/mf-model-manager/v1/model-quota/{conf_id}"
    return _RULES.get((request.method, path))

def build_kafka_record(entry, environment):
    """Map a committed management fact to Audit v1."""
    if environment not in {"development", "test", "staging", "production"}:
        raise ValueError("invalid Audit environment")
    if entry["target_type"] not in {"llm_model", "small_model", "model_quota", "user_model_quota"}:
        raise ValueError("unregistered Audit target type")
    if entry["action"] not in {"create", "update", "delete", "set_default"}:
        raise ValueError("unregistered Audit action")
    if entry["outcome"] not in {"success", "failure", "denied"}:
        raise ValueError("invalid Audit outcome")
    actor_id = str(entry["actor_id"]).strip()
    target_id = str(entry["target_id"]).strip()
    request_id = str(entry["request_id"]).strip()
    if not actor_id or not target_id or not request_id:
        raise ValueError("missing Audit identity or target")
    for key, limit in (("actor_name", 256), ("target_name", 512)):
        name = str(entry.get(key) or "").strip()
        reference = actor_id if key == "actor_name" else target_id
        if not name or name == reference or len(name) > limit:
            raise ValueError("missing or invalid readable Audit snapshot")
    occurred_at = entry["event_time"].astimezone(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    actor = {
        "id": actor_id,
        "effective_subject": actor_id,
        "type": "user" if entry["actor_type"] == "user" else "service_account",
        "auth_method": entry["auth_method"],
    }
    if entry.get("actor_name"):
        actor["display_name_snapshot"] = str(entry["actor_name"])
    target = {"type": entry["target_type"], "id": target_id}
    if entry.get("target_name"):
        target["name"] = str(entry["target_name"])
    record = {
        "schema_version": "1.0",
        "event_id": _audit_event_id(),
        "source_id": "model-manager",
        "category": "audit.admin",
        "event_name": "model_manager.operation.observed",
        "occurred_at": occurred_at,
        "actor": actor,
        "target": target,
        "outcome": entry["outcome"],
        "http_status": entry["http_status"],
        "scope": {
            "business_module": "model_management",
            "environment": environment,
            "platform_scope": True,
            "knowledge_network_ids": [],
        },
        "request_context": {"source_channel": "api", "transport": "http", "method": entry["method"]},
        "correlation": {"request_id": _audit_request_id(request_id)},
        "summary": _ACTION_NAMES[entry["action"]] + _TARGET_NAMES[entry["target_type"]] + "“" + entry["target_name"] + "”",
        "facts": {"action": entry["action"], "decision": "denied" if entry["outcome"] == "denied" else "allowed"},
    }
    if entry["outcome"] != "success":
        record["failure_code"] = entry.get("failure_code") or f"HTTP_{entry['http_status']}"
    return record


def operation_audit_request_id(headers):
    """Keep the caller's request ID or generate one at the source boundary."""
    request_id = (headers.get("bkn-request-id") or headers.get("x-request-id") or "").strip()
    if request_id:
        return request_id, False
    return "req_" + secrets.token_hex(16), True



def operation_audit_request(request):
    """Avoid snapshot-only lookups when the publisher is disabled."""
    return request if _audit_publisher is not None else None


def set_operation_audit_target(request, ids, names):
    """Attach the actual committed IDs and business snapshots at the source."""
    if request is None:
        return
    try:
        ids = [str(value) for value in ids]
        names = [str(value).strip() for value in names]
        if not ids or not names or any(not name for name in names):
            return
        target_id = ids[0] if len(ids) == 1 else "batch:" + hashlib.sha256(json.dumps(ids).encode()).hexdigest()
        if len(ids) == 1 and len(names) == 1:
            target_name = names[0]
        else:
            shown = []
            for name in names:
                if len(shown) == 5 or len(SEPARATOR.join(shown)) + len(name) > 400:
                    break
                shown.append(name)
            if not shown:
                return
            target_name = SEPARATOR.join(shown) + BATCH.format(count=len(ids))
            if len(shown) < len(names):
                target_name += TRUNCATED
        request.state.operation_audit_target = (target_id, target_name)
    except Exception:
        logger.error("operation_audit_snapshot_failed")


async def _actor_name(actor_id, headers):
    """Resolve names from the verified identity, never from client headers."""
    try:
        name = (await get_username_by_ids([actor_id])).get(actor_id, "")
        return name if name and name != actor_id and name != "-" else ""
    except Exception:
        return ""


async def operation_audit_middleware(request, call_next):
    rule = _rule(request)
    response = await call_next(request)
    if not rule:
        return response
    # Only a business handler can establish that a management change committed.
    target = getattr(request.state, "operation_audit_target", None)
    if target is None:
        return response
    try:
        actor = request.headers.get("x-account-id", "").strip()
        actor_name = await _actor_name(actor, request.headers) if actor else ""
        if not actor_name:
            logger.error("operation_audit_rejected missing_readable_actor")
            return response
        request_id, generated = operation_audit_request_id(request.headers)
        if generated:
            response.headers["bkn-request-id"] = request_id
        outcome = "success" if response.status_code < 400 else "failure"
        entry = {
            "event_time": datetime.now(timezone.utc),
            "actor_id": actor, "actor_name": actor_name,
            "actor_type": request.headers.get("x-account-type", "user"),
            "auth_method": "api_key" if request.headers.get("authorization", "").removeprefix("Bearer ").startswith("bak_") else "oauth",
            "request_id": request_id, "method": request.method,
            "action": rule[0], "target_type": rule[1], "target_id": target[0], "target_name": target[1],
            "outcome": outcome, "http_status": response.status_code,
        }
        if _audit_publisher is None:
            logger.error("operation_audit_not_configured action=%s target_type=%s", rule[0], rule[1])
        else:
            _audit_publisher.publish(build_kafka_record(entry, _audit_publisher.environment))
    except Exception as error:
        logger.error("operation_audit_publish_failed action=%s target_type=%s error_type=%s", rule[0], rule[1], type(error).__name__)
    return response
