"""Bounded model-management audit facts; inference and model tests stay Trace-only."""
import asyncio
import hashlib
import json
import secrets
from datetime import datetime, timezone

from app.commons.get_user_info import get_username_by_ids
from app.mydb.pymysql_pool import PymysqlPool

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

def _event_id(tenant, request_id, method, path):
    return "evt_" + hashlib.sha256("\n".join((tenant, request_id, method, path)).encode()).hexdigest()


def operation_audit_request_id(headers):
    """Keep the caller's request ID or generate one at the source boundary."""
    request_id = (headers.get("bkn-request-id") or headers.get("x-request-id") or "").strip()
    if request_id:
        return request_id, False
    return "req_" + secrets.token_hex(16), True

def _write(entry):
    pool = PymysqlPool.get_pool()
    connection = pool.connection()
    cursor = connection.cursor()
    try:
        cursor.execute("""INSERT INTO t_model_manager_operation_audit
        (event_id,event_time,recorded_at,tenant_id,business_domain_id,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message)
        VALUES (%(event_id)s,%(event_time)s,%(recorded_at)s,%(tenant_id)s,%(business_domain_id)s,%(actor_id)s,%(actor_name)s,%(actor_type)s,%(auth_method)s,%(request_id)s,%(source_channel)s,%(method)s,%(action)s,%(target_type)s,%(target_id)s,%(target_name)s,%(outcome)s,%(failure_code)s,%(failure_message)s)
        ON DUPLICATE KEY UPDATE event_id=VALUES(event_id)""", entry)
        connection.commit()
    finally:
        cursor.close(); connection.close()


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
    response = await call_next(request)
    tenant = request.headers.get("x-tenant-id", "").strip()
    domain = request.headers.get("x-business-domain", "").strip()
    actor = request.headers.get("x-account-id", "").strip()
    request_id, generated_request_id = operation_audit_request_id(request.headers)
    if generated_request_id:
        response.headers["bkn-request-id"] = request_id
    # A complete source fact requires the trusted scope and verified actor.
    if not tenant or not domain or not actor or not request_id:
        return response
    try:
        payload = json.loads(body.decode() or "{}") if len(body) <= 65536 else {}
    except (ValueError, UnicodeDecodeError):
        payload = {}
    target_id = next((str(payload[key]) for key in ("model_id", "id", "conf_id") if payload.get(key)), "")
    if not target_id and request.path_params:
        target_id = next(iter(request.path_params.values()), "")
    if not target_id:
        target_id = f"{rule[1]}:{request_id}"
    target_name = next((str(payload[key]) for key in ("model_name", "name", "display_name") if payload.get(key)), target_id)
    outcome = "success" if response.status_code < 400 else ("denied" if response.status_code in (401,403) else "failure")
    now = datetime.now(timezone.utc)
    entry = {"event_id": _event_id(tenant, request_id, request.method, request.url.path), "event_time": now, "recorded_at": now,
             "tenant_id": tenant, "business_domain_id": domain, "actor_id": actor, "actor_name": await _actor_name(actor, request.headers),
             "actor_type": request.headers.get("x-account-type", "user"), "auth_method": "api_key" if request.headers.get("authorization", "").removeprefix("Bearer ").startswith("bak_") else "oauth",
             "request_id": request_id, "source_channel": "api", "method": request.method, "action": rule[0], "target_type": rule[1], "target_id": target_id, "target_name": target_name,
             "outcome": outcome, "failure_code": "" if outcome == "success" else f"http_{response.status_code}", "failure_message": "" if outcome == "success" else "management request failed"}
    try:
        await asyncio.to_thread(_write, entry)
    except Exception:
        # Never overturn a completed management request; pipeline health is monitored separately.
        pass
    return response
