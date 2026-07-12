from fastapi import APIRouter, Depends, Query
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import AgentDeleted, AgentList, AgentOut, AgentSpec

router = APIRouter()


@router.post("/agents", response_model=AgentOut)
async def create_agent(
    spec: AgentSpec,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    try:
        return await dao.create_agent(session, spec, account.account_id)
    except IntegrityError:
        raise bad_request("NameConflict", "agent 名称已存在", spec.name, "换一个 name。")


@router.get("/agents", response_model=AgentList)
async def list_agents(
    page: int = Query(1, ge=1),
    size: int = Query(20, ge=1, le=100),
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    items, total = await dao.list_agents(session, page, size)
    return {"items": items, "total": total, "page": page, "size": size}


@router.get("/agents/{agent_id}", response_model=AgentOut)
async def get_agent(
    agent_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await dao.get_agent(session, agent_id)
    if not agent:
        raise not_found("agent", agent_id)
    return agent


@router.put("/agents/{agent_id}", response_model=AgentOut)
async def update_agent(
    agent_id: str,
    spec: AgentSpec,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await dao.update_agent(session, agent_id, spec, account.account_id)
    if not agent:
        raise not_found("agent", agent_id)
    return agent


@router.delete("/agents/{agent_id}", response_model=AgentDeleted)
async def delete_agent(
    agent_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    if not await dao.delete_agent(session, agent_id):
        raise not_found("agent", agent_id)
    return {"deleted": agent_id}
