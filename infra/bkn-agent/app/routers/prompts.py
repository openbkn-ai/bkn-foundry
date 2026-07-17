from fastapi import APIRouter, Depends, Query
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import (
    EffectivePromptOut,
    OverrideDeleted,
    OverridePut,
    OverrideState,
    PromptList,
    PromptOut,
    PromptPublish,
    PromptRollback,
    PromptSpec,
    PromptVersionList,
)

router = APIRouter()


@router.post("/prompts", response_model=PromptOut)
async def create_prompt(
    spec: PromptSpec,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    try:
        return await dao.create_prompt(
            session, spec.name, spec.content, spec.vars_schema, account.account_id, prompt_id=spec.prompt_id
        )
    except IntegrityError:
        raise bad_request(
            "Conflict", "提示词名称或 id 已存在",
            f"name={spec.name} id={spec.prompt_id}", "换一个 name，或换/去掉预设 prompt_id。",
        )


@router.get("/prompts", response_model=PromptList)
async def list_prompts(
    page: int = Query(1, ge=1),
    size: int = Query(20, ge=1, le=100),
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    items, total = await dao.list_prompts(session, page, size)
    return {"items": items, "total": total, "page": page, "size": size}


@router.get("/prompts/{prompt_id}", response_model=PromptOut)
async def get_prompt(
    prompt_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    prompt = await dao.get_prompt(session, prompt_id)
    if not prompt:
        raise not_found("prompt", prompt_id)
    return prompt


@router.put("/prompts/{prompt_id}", response_model=PromptOut)
async def publish_version(
    prompt_id: str,
    body: PromptPublish,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    """发布新版本：写入 t_agent_prompt_version 并推进 current_version，立即全局生效。"""
    prompt = await dao.publish_prompt_version(session, prompt_id, body.content, body.vars_schema, account.account_id)
    if prompt is None:
        raise not_found("prompt", prompt_id)
    return prompt


@router.get("/prompts/{prompt_id}/versions", response_model=PromptVersionList)
async def list_versions(
    prompt_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    if not await dao.get_prompt(session, prompt_id):
        raise not_found("prompt", prompt_id)
    return {"items": await dao.list_prompt_versions(session, prompt_id)}


@router.post("/prompts/{prompt_id}/rollback", response_model=PromptOut)
async def rollback(
    prompt_id: str,
    body: PromptRollback,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    result = await dao.rollback_prompt(session, prompt_id, body.version, account.account_id)
    if result is None:
        raise not_found("prompt", prompt_id)
    if result is False:
        raise bad_request("Version", "目标版本不存在", f"prompt {prompt_id} 无版本 {body.version}")
    return result


# ---- 调用方覆写（按 account 隔离，fail-closed 由 get_account 保证） ----


@router.get("/agents/{agent_id}/prompt", response_model=EffectivePromptOut)
async def get_effective_prompt(
    agent_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await dao.get_agent(session, agent_id)
    if not agent:
        raise not_found("agent", agent_id)
    override = await dao.get_prompt_override(session, agent_id, account.account_id)
    if override is not None:
        return EffectivePromptOut(source="override", content=override)
    if not agent.prompt_id:
        raise not_found("agent 默认提示词", agent_id)
    prompt = await dao.get_prompt(session, agent.prompt_id)
    if not prompt:
        raise not_found("prompt", agent.prompt_id)
    return EffectivePromptOut(
        source="default", content=prompt.content, prompt_id=prompt.prompt_id, version=prompt.current_version
    )


@router.put("/agents/{agent_id}/prompt", response_model=OverrideState)
async def put_override(
    agent_id: str,
    body: OverridePut,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    if not await dao.get_agent(session, agent_id):
        raise not_found("agent", agent_id)
    await dao.set_prompt_override(session, agent_id, account.account_id, body.content)
    return {"agent_id": agent_id, "account_id": account.account_id, "source": "override"}


@router.delete("/agents/{agent_id}/prompt", response_model=OverrideDeleted)
async def delete_override(
    agent_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    if not await dao.delete_prompt_override(session, agent_id, account.account_id):
        raise not_found("覆写", f"{agent_id}/{account.account_id}")
    return {"deleted": True, "fallback": "default"}
