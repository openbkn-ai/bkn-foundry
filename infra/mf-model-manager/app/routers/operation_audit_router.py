import asyncio
from datetime import datetime

import aiohttp
from fastapi import APIRouter, HTTPException, Query, Request

from app.core.config import bkn_safe_url
from app.mydb.pymysql_pool import PymysqlPool
from app.commons.locale import internal_request_headers

operation_audit_router = APIRouter()
_MAX_RANGE_SECONDS = 30 * 24 * 60 * 60
_AUDIT_ERRORS = {
    "access_denied": (403, "ModelFactory.OperationAudit.AccessDenied"),
    "invalid_timestamp": (400, "ModelFactory.OperationAudit.InvalidTimestamp"),
    "invalid_time_range": (400, "ModelFactory.OperationAudit.InvalidTimeRange"),
    "event_not_found": (404, "ModelFactory.OperationAudit.EventNotFound"),
}


def _audit_error(name):
    status_code, code = _AUDIT_ERRORS[name]
    return HTTPException(status_code=status_code, detail={"code": code, "link": ""})

async def _require_audit_reader(request: Request):
    token = request.headers.get("authorization", "")
    safe = bkn_safe_url().rstrip("/")
    if not token or not safe:
        raise _audit_error("access_denied")
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=3)) as session:
        async with session.get(
            safe + "/api/safe/v1/me",
            headers=internal_request_headers({"Authorization": token}),
        ) as response:
            if response.status != 200:
                raise _audit_error("access_denied")
            me = await response.json()
    if not me.get("enabled") or not set(me.get("roles", [])) & {"super_admin", "admin", "audit"}:
        raise _audit_error("access_denied")

def _list(from_time, to_time, limit, before_time, before_event_id, actor_id, action, target_type, target_id, outcome):
    pool=PymysqlPool.get_pool(); connection=pool.connection(); cursor=connection.cursor()
    try:
        sql="SELECT event_id,event_time,recorded_at,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM t_model_manager_operation_audit WHERE event_time>=%s AND event_time<%s"
        args=[from_time,to_time]
        for column, value in (("actor_id", actor_id), ("action", action), ("target_type", target_type), ("target_id", target_id), ("outcome", outcome)):
            if value:
                sql += " AND " + column + "=%s"; args.append(value)
        if before_time:
            sql+=" AND (event_time<%s OR (event_time=%s AND event_id>%s))";args += [before_time,before_time,before_event_id]
        sql += " ORDER BY event_time DESC,event_id ASC LIMIT %s";args.append(limit+1);cursor.execute(sql,args);rows=cursor.fetchall();return rows[:limit],len(rows)>limit
    finally: cursor.close();connection.close()

def _get(event_id):
    pool=PymysqlPool.get_pool(); connection=pool.connection(); cursor=connection.cursor()
    try:
        cursor.execute("SELECT event_id,event_time,recorded_at,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM t_model_manager_operation_audit WHERE event_id=%s",[event_id]);return cursor.fetchone()
    finally: cursor.close();connection.close()

def _time(value):
    try: return datetime.fromisoformat(value.replace("Z","+00:00"))
    except ValueError: raise _audit_error("invalid_timestamp")

@operation_audit_router.get("/operation-audits")
async def list_operation_audits(request: Request, from_: str = Query(alias="from"), to: str = Query(), limit: int = Query(50, ge=1, le=500), before_time: str = Query(""), before_event_id: str = Query(""), actor_id: str = Query(""), action: str = Query(""), target_type: str = Query(""), target_id: str = Query(""), outcome: str = Query("")):
    await _require_audit_reader(request); start,end=_time(from_),_time(to)
    if start>=end or (end-start).total_seconds()>_MAX_RANGE_SECONDS: raise _audit_error("invalid_time_range")
    before=_time(before_time) if before_time else None
    rows,more=await asyncio.to_thread(_list,start,end,limit,before,before_event_id,actor_id.strip(),action.strip(),target_type.strip(),target_id.strip(),outcome.strip())
    return {"entries":rows,"has_more":more}

@operation_audit_router.get("/operation-audits/{event_id}")
async def get_operation_audit(event_id: str, request: Request):
    await _require_audit_reader(request); row=await asyncio.to_thread(_get,event_id)
    if not row: raise _audit_error("event_not_found")
    return row
