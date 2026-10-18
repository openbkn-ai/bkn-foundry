# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Network and user settings for public executions, behind verified credentials.

A network setting is maintained by whoever may modify the network; a user
setting belongs to its owner alone. Both only add to the shared Agent; see
app/core/agent_settings.py for how the layers merge.
"""

from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, Path, Query
from pydantic import BaseModel
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account
from app.core.agent_settings import merge, shared_limits
from app.core.context_loader import profile_of
from app.db import get_session
from app.errors import bad_request, err, not_found
from app.models import (
    AgentLimits,
    AgentOut,
    AgentSetting,
    AgentSettingOut,
    PromptOut,
    PromptPublish,
    PromptRollback,
    PromptVersionOut,
    SharedAgentUpdate,
)
from app.public_auth import (
    BUILTIN_AGENT_IDS,
    PUBLIC_AGENT_IDS,
    authorize_agent,
    authorize_network_setting,
    can_manage_agents,
    get_public_account,
)

router = APIRouter()

_ID = r"^[0-9A-Za-z_.-]+$"
AgentId = Path(min_length=1, max_length=50, pattern=_ID)
KnId = Path(min_length=1, max_length=50, pattern=_ID)


async def _public_agent(session: AsyncSession, agent_id: str) -> AgentOut:
    agent = (
        await dao.get_agent(session, agent_id) if agent_id in PUBLIC_AGENT_IDS else None
    )
    if agent is None:
        raise not_found("agent", agent_id)
    return agent


def _out(agent_id: str, row) -> AgentSettingOut:
    if row is None:
        return AgentSettingOut(agent_id=agent_id, setting=AgentSetting(), version=0)
    try:
        setting = AgentSetting.model_validate(row.f_content)
    except ValueError:
        setting = AgentSetting()
    return AgentSettingOut(
        agent_id=agent_id,
        setting=setting,
        version=row.f_version,
        update_time=row.f_update_time,
    )


def _content(setting: AgentSetting) -> dict:
    return setting.model_dump(mode="json", exclude_none=True)


@router.get("/me/agents/{agent_id}/setting", response_model=AgentSettingOut)
async def get_user_setting(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _public_agent(session, agent_id)
    await authorize_agent(account, agent_id)
    return _out(
        agent_id, await dao.get_user_setting(session, agent_id, account.account_id)
    )


@router.put("/me/agents/{agent_id}/setting", response_model=AgentSettingOut)
async def put_user_setting(
    setting: AgentSetting,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _public_agent(session, agent_id)
    await authorize_agent(account, agent_id)
    row = await dao.put_user_setting(
        session, agent_id, account.account_id, _content(setting)
    )
    return _out(agent_id, row)


@router.delete("/me/agents/{agent_id}/setting", response_model=AgentSettingOut)
async def delete_user_setting(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _public_agent(session, agent_id)
    await authorize_agent(account, agent_id)
    await dao.delete_user_setting(session, agent_id, account.account_id)
    return _out(agent_id, None)


@router.get(
    "/networks/{kn_id}/agents/{agent_id}/setting", response_model=AgentSettingOut
)
async def get_network_setting(
    kn_id: str = KnId,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _public_agent(session, agent_id)
    await authorize_network_setting(account, agent_id, kn_id, write=False)
    return _out(agent_id, await dao.get_network_setting(session, agent_id, kn_id))


@router.put(
    "/networks/{kn_id}/agents/{agent_id}/setting", response_model=AgentSettingOut
)
async def put_network_setting(
    setting: AgentSetting,
    kn_id: str = KnId,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _public_agent(session, agent_id)
    await authorize_network_setting(account, agent_id, kn_id, write=True)
    row = await dao.put_network_setting(
        session, agent_id, kn_id, _content(setting), account.account_id
    )
    return _out(agent_id, row)


@router.delete(
    "/networks/{kn_id}/agents/{agent_id}/setting", response_model=AgentSettingOut
)
async def delete_network_setting(
    kn_id: str = KnId,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _public_agent(session, agent_id)
    await authorize_network_setting(account, agent_id, kn_id, write=True)
    await dao.delete_network_setting(session, agent_id, kn_id)
    return _out(agent_id, None)


class EffectiveOut(BaseModel):
    limits: AgentLimits
    # None means every tool the shared definition loads.
    allowed_tools: Optional[list[str]] = None


class AgentConfigOut(BaseModel):
    """Everything the configuration page shows for one network, in one read."""

    agent_id: str
    name: Optional[str] = None
    model: Optional[str] = None
    status: str
    shared: EffectiveOut
    network: AgentSettingOut
    user: AgentSettingOut
    effective: EffectiveOut
    # Context Loader MCP surface; null means the service default.
    mcp_profile: Optional[str] = None
    can_edit_network: bool
    can_edit_shared: bool


def _tools(names: Optional[set[str]]) -> Optional[list[str]]:
    return None if names is None else sorted(names)


@router.get("/me/agents/{agent_id}/config", response_model=AgentConfigOut)
async def get_agent_config(
    agent_id: str = AgentId,
    kn_id: str = Query(min_length=1, max_length=50, pattern=_ID),
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await _public_agent(session, agent_id)
    await authorize_network_setting(account, agent_id, kn_id, write=False)
    try:
        await authorize_network_setting(account, agent_id, kn_id, write=True)
        can_edit = True
    except HTTPException as exc:
        if exc.status_code != 403:
            raise
        can_edit = False
    network_row = await dao.get_network_setting(session, agent_id, kn_id)
    user_row = await dao.get_user_setting(session, agent_id, account.account_id)
    network, user = _out(agent_id, network_row), _out(agent_id, user_row)
    shared = merge(agent, None, 0, None, 0)
    effective = merge(
        agent,
        network.setting if network_row else None,
        network.version,
        user.setting if user_row else None,
        user.version,
    )
    return AgentConfigOut(
        agent_id=agent_id,
        name=agent.name,
        model=agent.model or None,
        status=agent.status,
        shared=EffectiveOut(
            limits=shared_limits(agent), allowed_tools=_tools(shared.allowed_tools)
        ),
        network=network,
        user=user,
        effective=EffectiveOut(
            limits=effective.agent.limits, allowed_tools=_tools(effective.allowed_tools)
        ),
        mcp_profile=profile_of(agent.tools),
        can_edit_network=can_edit,
        can_edit_shared=await can_manage_agents(account),
    )


# ---- Shared configuration ----


async def _builtin_agent(session: AsyncSession, agent_id: str) -> AgentOut:
    agent = (
        await dao.get_agent(session, agent_id) if agent_id in BUILTIN_AGENT_IDS else None
    )
    if agent is None:
        raise not_found("agent", agent_id)
    return agent


async def _shared_editor(
    session: AsyncSession, account: Account, agent_id: str
) -> AgentOut:
    """A built-in Agent the caller may change (admin-agent:manage). Before the
    first change its shipped state is recorded, so it can always be restored."""
    agent = await _builtin_agent(session, agent_id)
    if not await can_manage_agents(account):
        raise err(403, "BknAgent.Execution.Forbidden")
    await dao.ensure_builtin_default(session, agent_id)
    return agent


class SharedOut(BaseModel):
    agent_id: str
    name: str
    model: Optional[str] = None
    status: str
    limits: AgentLimits
    mcp_profile: Optional[str] = None
    # Whether the Agent loads Context Loader at all; the MCP surface choice
    # means nothing otherwise.
    uses_context_loader: bool
    # Whether network and personal settings apply (the public execution
    # surface); the others are configured here only.
    layered: bool
    can_edit: bool
    prompt_version: Optional[int] = None
    # Whether a shipped state is recorded, and whether the Agent differs from it.
    restorable: bool
    modified: bool
    update_user: str
    update_time: int


async def _shared_out(session: AsyncSession, agent: AgentOut, can_edit: bool) -> SharedOut:
    prompt = await dao.get_prompt(session, agent.prompt_id) if agent.prompt_id else None
    default = await dao.get_builtin_default(session, agent.agent_id)
    version = prompt.current_version if prompt else None
    return SharedOut(
        agent_id=agent.agent_id,
        name=agent.name,
        model=agent.model or None,
        status=agent.status,
        limits=shared_limits(agent),
        mcp_profile=profile_of(agent.tools),
        uses_context_loader=any(
            isinstance(ref, dict) and ref.get("type") == "context_loader"
            for ref in agent.tools
        ),
        layered=agent.agent_id in PUBLIC_AGENT_IDS,
        can_edit=can_edit,
        prompt_version=version,
        restorable=default is not None,
        modified=default is not None
        and (
            agent.model != default.f_model
            or agent.tools != default.f_tools
            or (agent.limits.model_dump(exclude_none=True) if agent.limits else None)
            != (default.f_limits or None)
            or version != default.f_prompt_version
        ),
        update_user=agent.update_user,
        update_time=agent.update_time,
    )


# Reading built-in configuration only needs a verified account: the defaults
# ship in the migrations and the console gates the page. Changing it needs
# admin-agent:manage (_shared_editor).
@router.get("/admin/agents", response_model=list[SharedOut])
async def list_shared_agents(
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    can_edit = await can_manage_agents(account)
    items = []
    for agent_id in BUILTIN_AGENT_IDS:
        # A deployment may not have every built-in seeded.
        if agent := await dao.get_agent(session, agent_id):
            items.append(await _shared_out(session, agent, can_edit))
    return items


@router.get("/admin/agents/{agent_id}", response_model=SharedOut)
async def get_shared_agent(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await _builtin_agent(session, agent_id)
    return await _shared_out(session, agent, await can_manage_agents(account))


@router.put("/admin/agents/{agent_id}", response_model=SharedOut)
async def update_shared_agent(
    body: SharedAgentUpdate,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await _shared_editor(session, account, agent_id)
    changes = body.model_dump(exclude_unset=True)
    # Platform features depend on built-in Agents; they cannot be unpublished.
    if changes.get("status", "published") != "published":
        raise bad_request("BknAgent.Agent.BuiltinStaysPublished", agent_id=agent_id)
    if "mcp_profile" in changes:
        profile = changes.pop("mcp_profile")
        tools = []
        for ref in agent.tools:
            ref = dict(ref)
            if ref.get("type") == "context_loader":
                if profile:
                    ref["profile"] = profile
                else:
                    ref.pop("profile", None)
            tools.append(ref)
        changes["tools"] = tools
    if "limits" in changes:
        changes["limits"] = (
            body.limits.model_dump(exclude_none=True) or None if body.limits else None
        )
    agent = await dao.update_shared_agent(
        session, agent_id, changes, account.account_id
    )
    return await _shared_out(session, agent, True)


@router.post("/admin/agents/{agent_id}/restore", response_model=SharedOut)
async def restore_shared_agent(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    """Return a built-in Agent to what it shipped with: model, tools (including
    the MCP surface), limits and the shared Prompt version. Prompt history is
    kept; the shipped version simply becomes current again."""
    agent = await _shared_editor(session, account, agent_id)
    default = await dao.get_builtin_default(session, agent_id)
    if default is None:
        raise err(404, "BknAgent.Agent.NoBuiltinDefault", agent_id=agent_id)
    if (
        default.f_prompt_version is not None
        and agent.prompt_id
        and await dao.rollback_prompt(
            session, agent.prompt_id, default.f_prompt_version, account.account_id
        )
        is False
    ):
        raise not_found("prompt", f"{agent.prompt_id}@{default.f_prompt_version}")
    agent = await dao.update_shared_agent(
        session,
        agent_id,
        {
            "model": default.f_model,
            "tools": default.f_tools,
            "limits": default.f_limits,
            "status": "published",
        },
        account.account_id,
    )
    return await _shared_out(session, agent, True)


class SharedPromptOut(BaseModel):
    prompt: PromptOut
    versions: list[PromptVersionOut]
    # The shipped version: restorable, so it cannot be deleted.
    initial_version: Optional[int] = None


async def _initial_version(session: AsyncSession, agent_id: str) -> Optional[int]:
    default = await dao.get_builtin_default(session, agent_id)
    return default.f_prompt_version if default else None


async def _shared_prompt(session: AsyncSession, agent: AgentOut) -> SharedPromptOut:
    if not agent.prompt_id or not (
        prompt := await dao.get_prompt(session, agent.prompt_id)
    ):
        raise not_found("prompt", agent.prompt_id or agent.agent_id)
    return SharedPromptOut(
        prompt=prompt,
        versions=await dao.list_prompt_versions(session, agent.prompt_id),
        initial_version=await _initial_version(session, agent.agent_id),
    )


@router.get("/admin/agents/{agent_id}/prompt", response_model=SharedPromptOut)
async def get_shared_prompt(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    return await _shared_prompt(session, await _builtin_agent(session, agent_id))


@router.put("/admin/agents/{agent_id}/prompt", response_model=SharedPromptOut)
async def publish_shared_prompt(
    body: PromptPublish,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await _shared_editor(session, account, agent_id)
    await dao.publish_prompt_version(
        session, agent.prompt_id, body.content, body.vars_schema, account.account_id
    )
    return await _shared_prompt(session, agent)


@router.post("/admin/agents/{agent_id}/prompt/rollback", response_model=SharedPromptOut)
async def rollback_shared_prompt(
    body: PromptRollback,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await _shared_editor(session, account, agent_id)
    if (
        await dao.rollback_prompt(
            session, agent.prompt_id, body.version, account.account_id
        )
        is False
    ):
        raise not_found("prompt", f"{agent.prompt_id}@{body.version}")
    return await _shared_prompt(session, agent)


@router.delete(
    "/admin/agents/{agent_id}/prompt/versions/{version}", response_model=SharedPromptOut
)
async def delete_shared_prompt_version(
    version: int = Path(ge=1),
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    """Remove a shared Prompt version from the history. The version in use and
    the shipped one (what restore returns to) stay."""
    agent = await _shared_editor(session, account, agent_id)
    prompt = await dao.get_prompt(session, agent.prompt_id) if agent.prompt_id else None
    if prompt is None:
        raise not_found("prompt", agent.prompt_id or agent_id)
    if version == prompt.current_version:
        raise bad_request("BknAgent.Prompt.VersionInUse", version=version)
    if version == await _initial_version(session, agent_id):
        raise bad_request("BknAgent.Prompt.InitialVersion", version=version)
    if not await dao.hide_prompt_version(session, agent.prompt_id, version, account.account_id):
        raise not_found("prompt", f"{agent.prompt_id}@{version}")
    return await _shared_prompt(session, agent)
