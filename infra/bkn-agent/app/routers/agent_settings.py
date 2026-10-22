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
from app.core import builtin_tools
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


async def _require_shared_admin(account: Account) -> None:
    if not await can_manage_agents(account):
        raise err(403, "BknAgent.Execution.Forbidden")


async def _shared_editor(
    session: AsyncSession, account: Account, agent_id: str
) -> AgentOut:
    """A built-in Agent the caller may change (admin-agent:manage). Its current
    state is recorded as a version first, so the change is never the first
    thing history knows about."""
    await _require_shared_admin(account)
    agent = await _builtin_agent(session, agent_id)
    await dao.record_version(session, agent_id, account.account_id)
    return agent


class ToolSourceOut(BaseModel):
    """One entry of an Agent's tools as configured, for display."""

    type: str
    # builtin: the provider; agent: the Agent it calls; toolbox: the box.
    provider: Optional[str] = None
    agent_id: Optional[str] = None
    box_id: Optional[str] = None
    name: Optional[str] = None
    # Tool names this source mounts; null means all it offers (Context Loader
    # without an allow-list) or names known only at execution time.
    names: Optional[list[str]] = None
    # What each tool tells the model, when known before execution (built-in
    # tools): name, description and parameter schema.
    definitions: Optional[list[dict]] = None


def _tool_sources(tool_refs: list) -> list[ToolSourceOut]:
    sources = []
    for ref in tool_refs:
        if not isinstance(ref, dict) or not isinstance(ref.get("type"), str):
            continue
        kind = ref["type"]
        names = None
        definitions = None
        if kind == "builtin":
            provider = builtin_tools.get(ref.get("provider") or "")
            chosen = ref.get("tools") or (provider.tool_names if provider else None)
            names = sorted(chosen) if chosen else None
            if provider and names:
                by_name = {d["name"]: d for d in builtin_tools.definitions(provider)}
                definitions = [by_name[n] for n in names if n in by_name]
        elif kind == "context_loader" and ref.get("allowed_tools") is not None:
            names = sorted(ref["allowed_tools"])
        sources.append(
            ToolSourceOut(
                type=kind,
                provider=ref.get("provider") if kind == "builtin" else None,
                agent_id=ref.get("agent_id") if kind == "agent" else None,
                box_id=ref.get("box_id") if kind == "toolbox" else None,
                name=ref.get("name"),
                names=names,
                definitions=definitions,
            )
        )
    return sources


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
    # The tools the Agent mounts, by source; read-only here.
    tools: list[ToolSourceOut] = []
    # Whether network and personal settings apply (the public execution
    # surface); the others are configured here only.
    layered: bool
    can_edit: bool
    prompt_version: Optional[int] = None
    # The current Agent version and the initial one restore returns to.
    version: Optional[int] = None
    initial_version: Optional[int] = None
    restorable: bool
    modified: bool
    update_user: str
    update_time: int


async def _shared_out(session: AsyncSession, agent: AgentOut, can_edit: bool) -> SharedOut:
    prompt = await dao.get_prompt(session, agent.prompt_id) if agent.prompt_id else None
    current = await dao.current_version(session, agent.agent_id)
    initial = await dao.initial_version(session, agent.agent_id)
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
        tools=_tool_sources(agent.tools),
        layered=agent.agent_id in PUBLIC_AGENT_IDS,
        can_edit=can_edit,
        prompt_version=prompt.current_version if prompt else None,
        version=current.f_version if current else None,
        initial_version=initial.f_version if initial else None,
        restorable=initial is not None,
        modified=initial is not None and not initial.f_current,
        update_user=agent.update_user,
        update_time=agent.update_time,
    )


# Built-in configuration and Prompt history require admin-agent:manage for
# both reads and writes. A client-side menu gate is not an authorization check.
@router.get("/admin/agents", response_model=list[SharedOut])
async def list_shared_agents(
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _require_shared_admin(account)
    items = []
    for agent_id in BUILTIN_AGENT_IDS:
        # A deployment may not have every built-in seeded.
        if agent := await dao.get_agent(session, agent_id):
            items.append(await _shared_out(session, agent, True))
    return items


@router.get("/admin/agents/{agent_id}", response_model=SharedOut)
async def get_shared_agent(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _require_shared_admin(account)
    agent = await _builtin_agent(session, agent_id)
    return await _shared_out(session, agent, True)


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
            # Leave anything that is not a reference exactly as stored.
            if not isinstance(ref, dict):
                tools.append(ref)
                continue
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


class AgentVersionOut(BaseModel):
    version: int
    model: Optional[str] = None
    mcp_profile: Optional[str] = None
    limits: Optional[dict] = None
    prompt_version: Optional[int] = None
    prompt_content: Optional[str] = None
    # What this version changed against the one before it (model, tools,
    # limits, prompt_version); empty for a first version.
    changes: list[str] = []
    initial: bool
    current: bool
    create_user: str
    create_time: int


async def _versions_out(session: AsyncSession, agent: AgentOut) -> list[AgentVersionOut]:
    contents = (
        {v.version: v.content for v in await dao.list_prompt_versions(session, agent.prompt_id)}
        if agent.prompt_id
        else {}
    )
    return [
        AgentVersionOut(
            version=v.f_version,
            model=v.f_model or None,
            mcp_profile=profile_of(v.f_tools or []),
            limits=v.f_limits,
            prompt_version=v.f_prompt_version,
            prompt_content=contents.get(v.f_prompt_version),
            changes=v.f_changes or [],
            initial=v.f_initial,
            current=v.f_current,
            create_user=v.f_create_user,
            create_time=v.f_create_time,
        )
        for v in await dao.list_versions(session, agent.agent_id)
    ]


@router.get("/admin/agents/{agent_id}/versions", response_model=list[AgentVersionOut])
async def list_agent_versions(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _require_shared_admin(account)
    return await _versions_out(session, await _builtin_agent(session, agent_id))


@router.post(
    "/admin/agents/{agent_id}/versions/{version}/activate", response_model=SharedOut
)
async def activate_agent_version(
    version: int = Path(ge=1),
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    """Make a version current: its model, tools, limits and Prompt version go
    back onto the Agent. No new version is written."""
    await _shared_editor(session, account, agent_id)
    if not await dao.activate_version(session, agent_id, version, account.account_id):
        raise not_found("agent_version", f"{agent_id}@{version}")
    return await _shared_out(session, await _builtin_agent(session, agent_id), True)


@router.delete("/admin/agents/{agent_id}/versions/{version}", response_model=list[AgentVersionOut])
async def delete_agent_version(
    version: int = Path(ge=1),
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    """Remove a version from the history. The current and the initial version
    stay; a deleted number is never handed out again."""
    agent = await _shared_editor(session, account, agent_id)
    result = await dao.delete_version(session, agent_id, version)
    if result == "current":
        raise bad_request("BknAgent.Agent.VersionInUse", version=version)
    if result == "initial":
        raise bad_request("BknAgent.Agent.InitialVersion", version=version)
    if result == "missing":
        raise not_found("agent_version", f"{agent_id}@{version}")
    return await _versions_out(session, agent)


@router.post("/admin/agents/{agent_id}/restore", response_model=SharedOut)
async def restore_shared_agent(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    """Make the initial version current again: what the built-in Agent shipped
    with. History is kept."""
    await _shared_editor(session, account, agent_id)
    initial = await dao.initial_version(session, agent_id)
    if initial is None or not await dao.activate_version(
        session, agent_id, initial.f_version, account.account_id
    ):
        raise err(404, "BknAgent.Agent.NoInitialVersion", agent_id=agent_id)
    agent = await _builtin_agent(session, agent_id)
    if agent.status != "published":
        agent = await dao.update_shared_agent(
            session, agent_id, {"status": "published"}, account.account_id
        )
    return await _shared_out(session, agent, True)


class SharedPromptOut(BaseModel):
    prompt: PromptOut


async def _shared_prompt(session: AsyncSession, agent: AgentOut) -> SharedPromptOut:
    if not agent.prompt_id or not (
        prompt := await dao.get_prompt(session, agent.prompt_id)
    ):
        raise not_found("prompt", agent.prompt_id or agent.agent_id)
    return SharedPromptOut(prompt=prompt)


@router.get("/admin/agents/{agent_id}/prompt", response_model=SharedPromptOut)
async def get_shared_prompt(
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    await _require_shared_admin(account)
    return await _shared_prompt(session, await _builtin_agent(session, agent_id))


@router.put("/admin/agents/{agent_id}/prompt", response_model=SharedPromptOut)
async def publish_shared_prompt(
    body: PromptPublish,
    agent_id: str = AgentId,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    """Publish new shared Prompt content; it becomes a new Agent version."""
    agent = await _shared_editor(session, account, agent_id)
    await dao.publish_prompt_version(
        session, agent.prompt_id, body.content, body.vars_schema, account.account_id
    )
    return await _shared_prompt(session, agent)
