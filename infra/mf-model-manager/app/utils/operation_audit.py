"""Bounded model-management audit facts; inference and model tests stay Trace-only."""
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
    """Map a management attempt to Audit v1 without inventing changed state."""
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
        "summary": f"model_manager.operation.observed {entry['action']} {entry['target_type']}",
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

async def _actor_name(actor_id, headers):
    """Persist the display-name snapshot when the authenticated source can resolve it.

    The identifier remains the authoritative actor key.  A directory lookup failure
    must not turn a successful management request into a failure, so the stable ID
    is the explicit fallback rather than a fabricated display name.
    """
    explicit = headers.get("x-account-name", "").strip()
    if explicit:
        return explicit
    try:
        return (await get_username_by_ids([actor_id])).get(actor_id, actor_id)
    except Exception:
        return actor_id

async def operation_audit_middleware(request, call_next):
    rule = _rule(request)
    if not rule:
        return await call_next(request)
    body = await request.body()

    async def receive():
        return {"type": "http.request", "body": body, "more_body": False}

    # The audit middleware consumes the ASGI body before FastAPI parses Body(...).
    # Replay it so the downstream route can parse the same payload.
    request._receive = receive
    response = await call_next(request)
    actor = request.headers.get("x-account-id", "").strip()
    request_id, generated_request_id = operation_audit_request_id(request.headers)
    if generated_request_id:
        response.headers["bkn-request-id"] = request_id
    # A complete source fact requires the verified actor and request identity.
    if not actor or not request_id:
        return response
    try:
        payload = json.loads(body.decode() or "{}") if len(body) <= 65536 else {}
    except (ValueError, UnicodeDecodeError):
        payload = {}
    target_id = next((str(payload[key]) for key in ("model_id", "id", "conf_id") if payload.get(key)), "")
    if not target_id and request.path_params:
        target_id = next(iter(request.path_params.values()), "")
    if not target_id:
        target_id = f"{rule[1]}:{_audit_request_id(request_id)}"
    target_name = next((str(payload[key]) for key in ("model_name", "name", "display_name") if payload.get(key)), target_id)
    outcome = "success" if response.status_code < 400 else ("denied" if response.status_code in (401,403) else "failure")
    now = datetime.now(timezone.utc)
    entry = {"event_time": now,
             "actor_id": actor, "actor_name": await _actor_name(actor, request.headers),
             "actor_type": request.headers.get("x-account-type", "user"), "auth_method": "api_key" if request.headers.get("authorization", "").removeprefix("Bearer ").startswith("bak_") else "oauth",
             "request_id": request_id, "method": request.method, "action": rule[0], "target_type": rule[1], "target_id": target_id, "target_name": target_name,
             "outcome": outcome, "http_status": response.status_code,
             "failure_code": "" if outcome == "success" else f"HTTP_{response.status_code}"}
    try:
        if _audit_publisher is None:
            logger.error("operation_audit_not_configured action=%s target_type=%s", rule[0], rule[1])
        else:
            record = build_kafka_record(entry, _audit_publisher.environment)
            _audit_publisher.publish(record)
    except Exception as error:
        logger.error("operation_audit_publish_failed action=%s target_type=%s error_type=%s",
                     rule[0], rule[1], type(error).__name__)
    return response
