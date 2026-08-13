import asyncio
import os
from datetime import datetime

import aiohttp
from fastapi import APIRouter, Header, HTTPException, Query, Request

from app.mydb.pymysql_pool import PymysqlPool

operation_audit_router = APIRouter()
_MAX_RANGE_SECONDS = 30 * 24 * 60 * 60

async def _require_audit_reader(request: Request):
    token = request.headers.get("authorization", "")
    safe = os.getenv("BKN_SAFE_URL", "").rstrip("/")
    if not token or not safe:
        raise HTTPException(403, "operation audit access denied")
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=3)) as session:
        async with session.get(safe + "/api/safe/v1/me", headers={"Authorization": token}) as response:
            if response.status != 200:
                raise HTTPException(403, "operation audit access denied")
            me = await response.json()
    if not me.get("enabled") or not set(me.get("roles", [])) & {"super_admin", "admin", "audit"}:
        raise HTTPException(403, "operation audit access denied")

def _list(tenant, domain, from_time, to_time, limit, before_time, before_event_id, actor_id, action, target_type, target_id, outcome):
    pool=PymysqlPool.get_pool(); connection=pool.connection(); cursor=connection.cursor()
    try:
        sql="SELECT event_id,event_time,recorded_at,tenant_id,business_domain_id,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM t_model_manager_operation_audit WHERE tenant_id=%s AND business_domain_id=%s AND event_time>=%s AND event_time<%s"
        args=[tenant,domain,from_time,to_time]
        for column, value in (("actor_id", actor_id), ("action", action), ("target_type", target_type), ("target_id", target_id), ("outcome", outcome)):
            if value:
                sql += " AND " + column + "=%s"; args.append(value)
        if before_time:
            sql+=" AND (event_time<%s OR (event_time=%s AND event_id>%s))";args += [before_time,before_time,before_event_id]
        sql += " ORDER BY event_time DESC,event_id ASC LIMIT %s";args.append(limit+1);cursor.execute(sql,args);rows=cursor.fetchall();return rows[:limit],len(rows)>limit
    finally: cursor.close();connection.close()

def _get(event_id,tenant,domain):
    pool=PymysqlPool.get_pool(); connection=pool.connection(); cursor=connection.cursor()
    try:
        cursor.execute("SELECT event_id,event_time,recorded_at,tenant_id,business_domain_id,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM t_model_manager_operation_audit WHERE event_id=%s AND tenant_id=%s AND business_domain_id=%s",[event_id,tenant,domain]);return cursor.fetchone()
    finally: cursor.close();connection.close()

def _time(value):
    try: return datetime.fromisoformat(value.replace("Z","+00:00"))
    except ValueError: raise HTTPException(400,"from/to must be RFC3339")

@operation_audit_router.get("/operation-audits")
async def list_operation_audits(request: Request, from_: str = Query(alias="from"), to: str = Query(), limit: int = Query(50, ge=1, le=500), before_time: str = Query(""), before_event_id: str = Query(""), actor_id: str = Query(""), action: str = Query(""), target_type: str = Query(""), target_id: str = Query(""), outcome: str = Query(""), x_tenant_id: str = Header(alias="x-tenant-id"), x_business_domain: str = Header(alias="x-business-domain")):
    await _require_audit_reader(request); start,end=_time(from_),_time(to)
    if start>=end or (end-start).total_seconds()>_MAX_RANGE_SECONDS: raise HTTPException(400,"from/to must be a valid RFC3339 range of at most 30 days")
    before=_time(before_time) if before_time else None
    rows,more=await asyncio.to_thread(_list,x_tenant_id,x_business_domain,start,end,limit,before,before_event_id,actor_id.strip(),action.strip(),target_type.strip(),target_id.strip(),outcome.strip())
    return {"entries":rows,"has_more":more}

@operation_audit_router.get("/operation-audits/{event_id}")
async def get_operation_audit(event_id: str, request: Request, x_tenant_id: str = Header(alias="x-tenant-id"), x_business_domain: str = Header(alias="x-business-domain")):
    await _require_audit_reader(request); row=await asyncio.to_thread(_get,event_id,x_tenant_id,x_business_domain)
    if not row: raise HTTPException(404,"operation audit event not found")
    return row
