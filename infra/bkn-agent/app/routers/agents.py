from fastapi import APIRouter, Depends, Query
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.bootstrap import toolbox_sync
from app.db import get_session
from app.errors import bad_request, forbidden, not_found
from app.models import AgentDeleted, AgentList, AgentOut, AgentSpec

router = APIRouter()


async def _load_owned_agent(session: AsyncSession, agent_id: str, account: Account) -> AgentOut:
    """Fetch an agent and assert the caller may mutate it. Mutation is
    restricted to the creator: without this, any authenticated account could
    edit or delete every agent on the platform (the account dependency used to
    be resolved and then ignored).

    A legacy row whose creator is unknown (empty f_create_user — agents created
    before ownership was recorded, or while AUTH_ENABLED was off) is left
    editable rather than locked to nobody; tightening that is deferred to the
    ownership model (#332). Read paths (list/get) are intentionally NOT gated
    here so existing agents stay visible to their current users.
    """
    agent = await dao.get_agent(session, agent_id)
    if not agent:
        raise not_found("agent", agent_id)
    owner = (agent.create_user or "").strip()
    if owner and owner != account.account_id:
        raise forbidden(
            "无权操作他人 agent",
            f"agent {agent_id} 属于 {owner}，调用方为 {account.account_id}",
            "只有创建者可修改或删除该 agent。",
        )
    return agent


@router.post("/agents", response_model=AgentOut)
async def create_agent(
    spec: AgentSpec,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    try:
        agent = await dao.create_agent(session, spec, account.account_id)
    except IntegrityError:
        raise bad_request(
            "Conflict", "agent 名称或 id 已存在",
            f"name={spec.name} id={spec.agent_id}", "换一个 name，或换/去掉预设 agent_id。",
        )
    toolbox_sync.schedule_resync()
    return agent


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
    await _load_owned_agent(session, agent_id, account)
    try:
        agent = await dao.update_agent(session, agent_id, spec, account.account_id)
    except IntegrityError:
        raise bad_request("Conflict", "agent 名称已存在", f"name={spec.name}", "换一个 name。")
    if not agent:
        raise not_found("agent", agent_id)
    toolbox_sync.schedule_resync()
    return agent


@router.delete("/agents/{agent_id}", response_model=AgentDeleted)
async def delete_agent(
    agent_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    await _load_owned_agent(session, agent_id, account)
    if not await dao.delete_agent(session, agent_id):
        raise not_found("agent", agent_id)
    toolbox_sync.schedule_resync()
    return {"deleted": agent_id}
