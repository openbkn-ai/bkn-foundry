import hashlib
import time
import uuid
from typing import Optional

from sqlalchemy import delete, func, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.commons.i18n import localized_message
from app.commons.locale import get_effective_locale
from app.errors import err
from app.models import (
    AgentOut,
    AgentRow,
    AgentSpec,
    AgentVersionRow,
    NetworkSettingRow,
    PromptOverrideRow,
    PromptRow,
    PromptVersionRow,
    TaskOut,
    TaskRow,
    ThreadRow,
    UserSettingRow,
)


def _now_ms() -> int:
    return int(time.time() * 1000)


def _to_out(row: AgentRow) -> AgentOut:
    return AgentOut(
        agent_id=row.f_agent_id,
        name=row.f_name,
        mode=row.f_mode,
        prompt_id=row.f_prompt_id,
        prompt_vars_schema=row.f_prompt_vars_schema,
        model=row.f_model or "",
        tools=row.f_tools or [],
        skills=row.f_skills or [],
        limits=row.f_limits,
        status=row.f_status,
        create_user=row.f_create_user,
        update_user=row.f_update_user,
        create_time=row.f_create_time,
        update_time=row.f_update_time,
    )


async def create_agent(session: AsyncSession, spec: AgentSpec, account_id: str) -> AgentOut:
    now = _now_ms()
    row = AgentRow(
        f_agent_id=spec.agent_id or str(uuid.uuid4()),  # A preset id wins, otherwise generate one
        f_name=spec.name,
        f_mode=spec.mode,
        f_prompt_id=spec.prompt_id,
        f_prompt_vars_schema=spec.prompt_vars_schema,
        f_model=spec.model,
        f_tools=spec.tools,
        f_skills=spec.skills,
        f_limits=spec.limits.model_dump(exclude_none=True) if spec.limits else None,
        f_status=spec.status,
        f_create_user=account_id,
        f_update_user=account_id,
        f_create_time=now,
        f_update_time=now,
    )
    session.add(row)
    await session.commit()
    await record_version(session, row.f_agent_id, account_id)
    return _to_out(row)


async def get_agent(session: AsyncSession, agent_id: str) -> Optional[AgentOut]:
    row = await session.get(AgentRow, agent_id)
    return _to_out(row) if row else None


async def list_agents(session: AsyncSession, page: int, size: int) -> tuple[list[AgentOut], int]:
    rows = (
        await session.execute(
            select(AgentRow).order_by(AgentRow.f_update_time.desc()).offset((page - 1) * size).limit(size)
        )
    ).scalars().all()
    total = (await session.execute(select(func.count()).select_from(AgentRow))).scalar_one()
    return [_to_out(r) for r in rows], total


async def list_authorization_resources(
    session: AsyncSession,
    name: str,
    direction: str,
    offset: int,
    limit: int,
) -> tuple[list[tuple[str, str]], int]:
    """List authoritative agent display names for bkn-safe.

    agent_tpl uses the same rows: publishing turns an agent into a reusable
    template, so both authorization resource types share the stable agent ID.
    """
    condition = AgentRow.f_name.contains(name) if name else None
    query = select(AgentRow.f_agent_id, AgentRow.f_name)
    count_query = select(func.count()).select_from(AgentRow)
    if condition is not None:
        query = query.where(condition)
        count_query = count_query.where(condition)
    order = AgentRow.f_name.asc if direction == "asc" else AgentRow.f_name.desc
    id_order = AgentRow.f_agent_id.asc if direction == "asc" else AgentRow.f_agent_id.desc
    rows = (await session.execute(query.order_by(order(), id_order()).offset(offset).limit(limit))).all()
    total = (await session.execute(count_query)).scalar_one()
    return [(row.f_agent_id, row.f_name) for row in rows], total


async def update_agent(session: AsyncSession, agent_id: str, spec: AgentSpec, account_id: str) -> Optional[AgentOut]:
    row = await session.get(AgentRow, agent_id)
    if not row:
        return None
    row.f_name = spec.name
    row.f_mode = spec.mode
    row.f_prompt_id = spec.prompt_id
    row.f_prompt_vars_schema = spec.prompt_vars_schema
    row.f_model = spec.model
    row.f_tools = spec.tools
    row.f_skills = spec.skills
    row.f_limits = spec.limits.model_dump(exclude_none=True) if spec.limits else None
    row.f_status = spec.status
    row.f_update_user = account_id
    row.f_update_time = _now_ms()
    await session.commit()
    await record_version(session, agent_id, account_id)
    return _to_out(row)


# ---------- Agent versions: snapshots of model, tools, limits and Prompt version ----------

_SNAPSHOT_FIELDS = ("model", "tools", "limits", "prompt_version")


async def _snapshot(session: AsyncSession, row: AgentRow) -> dict:
    head = await session.get(PromptRow, row.f_prompt_id, populate_existing=True, with_for_update=True) if row.f_prompt_id else None
    return {
        "model": row.f_model or "",
        "tools": row.f_tools or [],
        "limits": row.f_limits or None,
        "prompt_version": head.f_current_version if head else None,
    }


def _version_snapshot(version: AgentVersionRow) -> dict:
    return {
        "model": version.f_model or "",
        "tools": version.f_tools or [],
        "limits": version.f_limits or None,
        "prompt_version": version.f_prompt_version,
    }


async def current_version(session: AsyncSession, agent_id: str, *, lock: bool = False) -> Optional[AgentVersionRow]:
    # Writers serialize on the Agent row and clear every other current flag, so
    # there is one; reading the newest still keeps execution working if a stray
    # duplicate ever exists.
    query = (select(AgentVersionRow)
             .where(AgentVersionRow.f_agent_id == agent_id, AgentVersionRow.f_current.is_(True))
             .order_by(AgentVersionRow.f_version.desc()).limit(1))
    if lock:
        query = query.with_for_update().execution_options(populate_existing=True)
    return (await session.execute(query)).scalars().first()


async def _lock_agent(session: AsyncSession, agent_id: str) -> Optional[AgentRow]:
    """Serialize version writers for one Agent on its row."""
    return (
        await session.execute(
            select(AgentRow).where(AgentRow.f_agent_id == agent_id).with_for_update().execution_options(populate_existing=True)
        )
    ).scalar_one_or_none()


async def lock_prompt_agents(session: AsyncSession, prompt_id: str) -> list[str]:
    """Lock the current binding range, not a repeatable-read snapshot of IDs.

    Force the binding index on MySQL/MariaDB: a primary-key scan for
    ORDER BY would otherwise lock unrelated Agents. The dialect-specific hint
    is omitted on SQLite. Range locks also serialize new bindings until commit.
    """
    rows = (await session.execute(
        select(AgentRow).where(AgentRow.f_prompt_id == prompt_id)
        .with_hint(AgentRow, "FORCE INDEX (ix_agent_prompt_id_agent_id)", dialect_name="mysql")
        .order_by(AgentRow.f_agent_id).with_for_update()
        .execution_options(populate_existing=True)
    )).scalars().all()
    return [row.f_agent_id for row in rows]


async def _make_current(session: AsyncSession, agent_id: str, version: int) -> None:
    """One statement leaves exactly `version` current among the Agent's versions.

    Setting the flag in SQL, not on a loaded object, matters: an object already
    in the session may still say current from before the lock, and assigning
    the same value writes nothing while the clearing statement has run.
    """
    await session.execute(
        update(AgentVersionRow)
        .where(AgentVersionRow.f_agent_id == agent_id)
        .values(f_current=AgentVersionRow.f_version == version)
        .execution_options(synchronize_session="fetch")
    )


async def initial_version(session: AsyncSession, agent_id: str) -> Optional[AgentVersionRow]:
    return (
        await session.execute(
            select(AgentVersionRow).where(
                AgentVersionRow.f_agent_id == agent_id,
                AgentVersionRow.f_initial.is_(True),
                AgentVersionRow.f_deleted.is_(False),
            )
        )
    ).scalar_one_or_none()


async def list_versions(session: AsyncSession, agent_id: str) -> list[AgentVersionRow]:
    return list(
        (
            await session.execute(
                select(AgentVersionRow)
                .where(
                    AgentVersionRow.f_agent_id == agent_id,
                    AgentVersionRow.f_deleted.is_(False),
                )
                .order_by(AgentVersionRow.f_version.desc())
            )
        ).scalars()
    )


async def record_version(
    session: AsyncSession, agent_id: str, account_id: str, commit: bool = True
) -> Optional[AgentVersionRow]:
    """Write the Agent's configuration as a new current version when it differs
    from the current one. Every path that changes an Agent's runtime or the
    Prompt version it uses ends here, so versions cover all of them. A built-in
    Agent's first version is its initial version."""
    from app.public_auth import BUILTIN_AGENT_IDS

    row = await _lock_agent(session, agent_id)
    if not row:
        return None
    snapshot = await _snapshot(session, row)
    current = await current_version(session, agent_id, lock=True)
    changes = None
    if current is not None:
        before = _version_snapshot(current)
        changes = [field for field in _SNAPSHOT_FIELDS if snapshot[field] != before[field]]
        if not changes:
            # Release the lock without writing.
            if commit:
                await session.commit()
            return current
    # Deleted versions keep their numbers, so the maximum covers them too.
    latest = (
        await session.execute(
            select(AgentVersionRow.f_version).where(
                AgentVersionRow.f_agent_id == agent_id
            ).order_by(AgentVersionRow.f_version.desc()).limit(1).with_for_update()
        )
    ).scalar_one_or_none()
    version = AgentVersionRow(
        f_agent_id=agent_id,
        f_version=(latest or 0) + 1,
        f_model=snapshot["model"],
        f_tools=snapshot["tools"],
        f_limits=snapshot["limits"],
        f_prompt_version=snapshot["prompt_version"],
        f_changes=changes,
        f_initial=current is None and latest is None and agent_id in BUILTIN_AGENT_IDS,
        f_current=True,
        f_deleted=False,
        f_create_user=account_id,
        f_create_time=_now_ms(),
    )
    await _make_current(session, agent_id, version.f_version)
    session.add(version)
    if commit:
        await session.commit()
    else:
        await session.flush()
    return version


async def _record_for_prompt(
    session: AsyncSession, prompt_id: str, account_id: str, commit: bool,
    *, agent_ids: Optional[list[str]] = None,
) -> None:
    # A transactional publisher passes its current-read locked set. Repeating
    # a plain SELECT here could miss a binding newer than this session's RR snapshot.
    if agent_ids is None:
        agent_ids = list((await session.execute(
            select(AgentRow.f_agent_id).where(AgentRow.f_prompt_id == prompt_id)
            .order_by(AgentRow.f_agent_id)
        )).scalars())
    for agent_id in agent_ids:
        await record_version(session, agent_id, account_id, commit=commit)


async def activate_version(
    session: AsyncSession, agent_id: str, version: int, account_id: str, *, commit: bool = True
) -> Optional[AgentVersionRow]:
    """Make a version current again: its runtime goes back onto the Agent and
    the shared Prompt points at its version. No new version is written."""
    row = await _lock_agent(session, agent_id)
    target = await session.get(AgentVersionRow, (agent_id, version), populate_existing=True, with_for_update=True)
    if not target or target.f_deleted or not row:
        if commit:
            await session.commit()
        return None
    if target.f_prompt_version is not None and row.f_prompt_id:
        head = await session.get(PromptRow, row.f_prompt_id, populate_existing=True, with_for_update=True)
        if not head or not await session.get(
            PromptVersionRow, (row.f_prompt_id, target.f_prompt_version)
        ):
            if commit:
                await session.commit()
            return None
        head.f_current_version = target.f_prompt_version
        head.f_update_user = account_id
        head.f_update_time = _now_ms()
    row.f_model = target.f_model
    row.f_tools = target.f_tools
    row.f_limits = target.f_limits
    row.f_update_user = account_id
    row.f_update_time = _now_ms()
    await _make_current(session, agent_id, version)
    if commit:
        await session.commit()
    else:
        await session.flush()
    return target


async def delete_version(session: AsyncSession, agent_id: str, version: int, *, commit: bool = True) -> str:
    """Flag a version deleted. Returns "deleted", "missing", "current" or
    "initial"; the current and the initial version stay."""
    if not await _lock_agent(session, agent_id):
        return "missing"
    target = await session.get(AgentVersionRow, (agent_id, version), populate_existing=True, with_for_update=True)
    if not target or target.f_deleted:
        return "missing"
    if target.f_current:
        return "current"
    if target.f_initial:
        return "initial"
    target.f_deleted = True
    if commit:
        await session.commit()
    else:
        await session.flush()
    return "deleted"


async def update_shared_agent(
    session: AsyncSession, agent_id: str, changes: dict, account_id: str, *, commit: bool = True
) -> Optional[AgentOut]:
    """Change only the shared runtime fields (model, limits, status); tools,
    skills and the Prompt binding stay as defined."""
    row = await _lock_agent(session, agent_id)
    if not row:
        return None
    if "model" in changes:
        row.f_model = changes["model"]
    if "limits" in changes:
        row.f_limits = changes["limits"]
    if "status" in changes:
        row.f_status = changes["status"]
    if "tools" in changes:
        row.f_tools = changes["tools"]
    row.f_update_user = account_id
    row.f_update_time = _now_ms()
    await session.flush()
    await record_version(session, agent_id, account_id, commit=False)
    if commit:
        await session.commit()
    return _to_out(row)


async def delete_agent(session: AsyncSession, agent_id: str) -> bool:
    result = await session.execute(delete(AgentRow).where(AgentRow.f_agent_id == agent_id))
    await session.commit()
    return result.rowcount > 0


async def get_thread_row(session: AsyncSession, thread_id: str) -> Optional[ThreadRow]:
    return await session.get(ThreadRow, thread_id)


async def touch_thread(session: AsyncSession, thread_id: str, agent_id: str, account_id: str) -> ThreadRow:
    """Record ownership for a new thread and refresh update_time for an existing
    one. The ownership check lives in the caller and fails closed."""
    now = _now_ms()
    row = await session.get(ThreadRow, thread_id)
    if row:
        row.f_update_time = now
    else:
        row = ThreadRow(
            f_thread_id=thread_id,
            f_agent_id=agent_id,
            f_account_id=account_id,
            f_create_time=now,
            f_update_time=now,
        )
        session.add(row)
    await session.commit()
    return row


def _task_out(row: TaskRow) -> TaskOut:
    return TaskOut(
        task_id=row.f_task_id,
        agent_id=row.f_agent_id,
        status=row.f_status,
        input=row.f_input,
        output=row.f_output,
        failure_detail=row.f_failure_detail,
        parent_thread_id=row.f_parent_thread_id,
        create_time=row.f_create_time,
        update_time=row.f_update_time,
    )


async def create_task(
    session: AsyncSession,
    agent_id: str,
    task_input: dict,
    account_id: str,
    parent_thread_id: Optional[str] = None,
) -> TaskOut:
    now = _now_ms()
    row = TaskRow(
        f_task_id=str(uuid.uuid4()),
        f_agent_id=agent_id,
        f_status="pending",
        f_input=task_input,
        f_parent_thread_id=parent_thread_id,
        f_account_id=account_id,
        f_create_time=now,
        f_update_time=now,
    )
    session.add(row)
    await session.commit()
    return _task_out(row)


async def get_task(
    session: AsyncSession, task_id: str, account_id: Optional[str] = None
) -> Optional[TaskOut]:
    """When account_id is given, filter by ownership: a non-owner answers exactly
    like a missing task, failing closed the same way threads do. Internal callers
    — the runner writing back, /invoke checking its own task — omit it and read
    straight by task_id."""
    row = await session.get(TaskRow, task_id)
    if not row:
        return None
    if account_id is not None and row.f_account_id != account_id:
        return None
    return _task_out(row)


async def set_task_status(
    session: AsyncSession,
    task_id: str,
    status: str,
    output: Optional[str] = None,
    failure_detail: Optional[str] = None,
) -> None:
    row = await session.get(TaskRow, task_id)
    if not row:
        return
    row.f_status = status
    if output is not None:
        row.f_output = output
    if failure_detail is not None:
        row.f_failure_detail = failure_detail
    row.f_update_time = _now_ms()
    await session.commit()


async def recover_stale_tasks(session: AsyncSession) -> int:
    """Startup safety net. In-process asyncio tasks do not survive a restart, so
    any task still stored as pending or running was lost with the previous
    process; mark them all failed to keep GET /tasks from hanging forever in a
    non-terminal state. Returns how many were reclaimed.

    **Assumes a single replica** (chart replicaCount=1 with maxSurge=0, so
    rollouts never overlap). Unconditionally reclaiming every pending/running row
    is safe only with one replica; with several it would kill tasks another
    replica is actively running, which needs task leases or an owner column
    before it can be relaxed. See the replica constraint note in values.yaml."""
    now = _now_ms()
    result = await session.execute(
        update(TaskRow)
        .where(TaskRow.f_status.in_(("pending", "running")))
        .values(
            f_status="failed",
            # This persisted, client-visible text stays in the service default
            # locale until #826 P4 carries effective_locale into async task
            # envelopes. Replacing it with English here would change the
            # response contract instead of performing an internal-text cleanup.
            f_failure_detail="服务重启中断：任务未持久化执行状态，进程重启无法恢复，请重试。",
            f_update_time=now,
        )
    )
    await session.commit()
    return result.rowcount or 0


async def get_default_prompt(
    session: AsyncSession, prompt_id: str
) -> Optional[tuple[str, Optional[dict], int]]:
    """The agent default layer: the body of the version named by
    t_agent_prompt.current_version."""
    head = await session.get(PromptRow, prompt_id)
    if not head:
        return None
    version = await session.get(PromptVersionRow, (prompt_id, head.f_current_version))
    if not version:
        return None
    return _localized_prompt_content(version), version.f_vars_schema, version.f_version


def _localized_prompt_content(version: PromptVersionRow) -> str:
    """Select a stored translation, never translate user-supplied content.

    Raw Prompt management/export/history reads deliberately do not call this.
    New manually published versions have no inherited translations.
    """
    if version.f_source_language == get_effective_locale():
        return version.f_content
    variants = version.f_localized_content or {}
    translated = variants.get(get_effective_locale()) if isinstance(variants, dict) else None
    return translated if isinstance(translated, str) and translated else version.f_content


async def get_prompt_override(session: AsyncSession, agent_id: str, account_id: str) -> Optional[str]:
    row = await session.get(PromptOverrideRow, (agent_id, account_id))
    return row.f_content if row else None


async def set_prompt_override(session: AsyncSession, agent_id: str, account_id: str, content: str) -> None:
    row = await session.get(PromptOverrideRow, (agent_id, account_id))
    if row:
        row.f_content = content
        row.f_update_time = _now_ms()
    else:
        session.add(
            PromptOverrideRow(
                f_agent_id=agent_id, f_account_id=account_id, f_content=content, f_update_time=_now_ms()
            )
        )
    await session.commit()


async def delete_prompt_override(session: AsyncSession, agent_id: str, account_id: str) -> bool:
    result = await session.execute(
        delete(PromptOverrideRow).where(
            PromptOverrideRow.f_agent_id == agent_id, PromptOverrideRow.f_account_id == account_id
        )
    )
    await session.commit()
    return result.rowcount > 0


# ---- Network and user settings for public executions ----


async def get_network_setting(
    session: AsyncSession, agent_id: str, kn_id: str
) -> Optional[NetworkSettingRow]:
    return await session.get(NetworkSettingRow, (agent_id, kn_id))


async def put_network_setting(
    session: AsyncSession, agent_id: str, kn_id: str, content: dict, account_id: str
) -> NetworkSettingRow:
    row = await session.get(NetworkSettingRow, (agent_id, kn_id))
    if row:
        row.f_content = content
        row.f_version += 1
        row.f_update_user = account_id
        row.f_update_time = _now_ms()
    else:
        row = NetworkSettingRow(
            f_agent_id=agent_id,
            f_kn_id=kn_id,
            f_content=content,
            f_version=1,
            f_update_user=account_id,
            f_update_time=_now_ms(),
        )
        session.add(row)
    await session.commit()
    return row


async def delete_network_setting(session: AsyncSession, agent_id: str, kn_id: str) -> bool:
    result = await session.execute(
        delete(NetworkSettingRow).where(
            NetworkSettingRow.f_agent_id == agent_id, NetworkSettingRow.f_kn_id == kn_id
        )
    )
    await session.commit()
    return result.rowcount > 0


async def get_user_setting(
    session: AsyncSession, agent_id: str, account_id: str
) -> Optional[UserSettingRow]:
    return await session.get(UserSettingRow, (agent_id, account_id))


async def put_user_setting(
    session: AsyncSession, agent_id: str, account_id: str, content: dict
) -> UserSettingRow:
    row = await session.get(UserSettingRow, (agent_id, account_id))
    if row:
        row.f_content = content
        row.f_version += 1
        row.f_update_time = _now_ms()
    else:
        row = UserSettingRow(
            f_agent_id=agent_id,
            f_account_id=account_id,
            f_content=content,
            f_version=1,
            f_update_time=_now_ms(),
        )
        session.add(row)
    await session.commit()
    return row


async def delete_user_setting(session: AsyncSession, agent_id: str, account_id: str) -> bool:
    result = await session.execute(
        delete(UserSettingRow).where(
            UserSettingRow.f_agent_id == agent_id, UserSettingRow.f_account_id == account_id
        )
    )
    await session.commit()
    return result.rowcount > 0


# ---- Prompt management surface: versioned, append-only ----


async def _prompt_out(session: AsyncSession, head: PromptRow, *, localized: bool = False):
    from app.models import PromptOut

    version = await session.get(PromptVersionRow, (head.f_prompt_id, head.f_current_version))
    return PromptOut(
        prompt_id=head.f_prompt_id,
        name=head.f_name,
        current_version=head.f_current_version,
        content=(_localized_prompt_content(version) if localized else version.f_content) if version else "",
        vars_schema=version.f_vars_schema if version else None,
        update_user=head.f_update_user,
        update_time=head.f_update_time,
    )


async def create_prompt(
    session: AsyncSession,
    name: str,
    content: str,
    vars_schema: Optional[dict],
    account_id: str,
    prompt_id: Optional[str] = None,
):
    now = _now_ms()
    prompt_id = prompt_id or str(uuid.uuid4())  # A preset id wins, otherwise generate one
    session.add(
        PromptRow(
            f_prompt_id=prompt_id, f_name=name, f_current_version=1, f_update_user=account_id, f_update_time=now
        )
    )
    session.add(
        PromptVersionRow(
            f_prompt_id=prompt_id,
            f_version=1,
            f_content=content,
            f_vars_schema=vars_schema,
            f_create_user=account_id,
            f_create_time=now,
        )
    )
    await session.commit()
    head = await session.get(PromptRow, prompt_id)
    return await _prompt_out(session, head)


async def get_prompt(session: AsyncSession, prompt_id: str, *, localized: bool = False):
    head = await session.get(PromptRow, prompt_id)
    return await _prompt_out(session, head, localized=localized) if head else None


async def list_prompts(session: AsyncSession, page: int, size: int):
    from sqlalchemy import func

    heads = (
        await session.execute(
            select(PromptRow).order_by(PromptRow.f_update_time.desc()).offset((page - 1) * size).limit(size)
        )
    ).scalars().all()
    total = (await session.execute(select(func.count()).select_from(PromptRow))).scalar_one()
    return [await _prompt_out(session, h) for h in heads], total


async def publish_prompt_version(
    session: AsyncSession, prompt_id: str, content: str, vars_schema: Optional[dict], account_id: str,
    commit: bool = True,
    *, preserve_vars_schema: bool = False, version: int | None = None,
    source_language: str | None = None,
):
    locked_agent_ids = None
    lock = not commit or version is not None
    if lock:
        # Imports reach this function without already owning an Agent lock.
        locked_agent_ids = await lock_prompt_agents(session, prompt_id)
    head = await session.get(
        PromptRow, prompt_id, **({"populate_existing": True, "with_for_update": True} if lock else {})
    )
    if not head:
        return None
    if preserve_vars_schema:
        # Inherit the active version after taking the Prompt lock, including
        # after a rollback. A locking read avoids an older MariaDB RR snapshot.
        current = await session.get(
            PromptVersionRow, (prompt_id, head.f_current_version),
            populate_existing=True, with_for_update=True,
        )
        vars_schema = current.f_vars_schema if current else None
    latest_query = (select(PromptVersionRow.f_version)
                    .where(PromptVersionRow.f_prompt_id == prompt_id)
                    .order_by(PromptVersionRow.f_version.desc()).limit(1))
    if lock:
        latest_query = latest_query.with_for_update()
    latest = (await session.execute(latest_query)).scalar_one()
    if version is not None and version != latest + 1:
        raise err(409, "BknAgent.Prompt.VersionConflict", version=version, next_version=latest + 1)
    now = _now_ms()
    session.add(
        PromptVersionRow(
            f_prompt_id=prompt_id,
            f_version=latest + 1,
            f_content=content,
            f_source_language=source_language,
            f_vars_schema=vars_schema,
            f_create_user=account_id,
            f_create_time=now,
        )
    )
    head.f_current_version = latest + 1
    head.f_update_user = account_id
    head.f_update_time = now
    if commit:
        await session.commit()
    else:
        await session.flush()
    await _record_for_prompt(session, prompt_id, account_id, commit, agent_ids=locked_agent_ids)
    return await _prompt_out(session, head)


async def next_prompt_version(session: AsyncSession, prompt_id: str) -> int:
    latest = (await session.execute(
        select(func.max(PromptVersionRow.f_version)).where(PromptVersionRow.f_prompt_id == prompt_id)
    )).scalar_one()
    return (latest or 0) + 1


async def list_prompt_versions(session: AsyncSession, prompt_id: str, *, localized: bool = False):
    from app.models import PromptVersionOut

    rows = (
        await session.execute(
            select(PromptVersionRow)
            .where(PromptVersionRow.f_prompt_id == prompt_id)
            .order_by(PromptVersionRow.f_version.desc())
        )
    ).scalars().all()
    return [
        PromptVersionOut(
            version=r.f_version,
            content=_localized_prompt_content(r) if localized else r.f_content,
            vars_schema=r.f_vars_schema,
            create_user=r.f_create_user,
            create_time=r.f_create_time,
        )
        for r in rows
    ]


async def rollback_prompt(session: AsyncSession, prompt_id: str, version: int, account_id: str):
    """A rollback repoints current_version at an older version; version rows are
    append-only and never edited."""
    head = await session.get(PromptRow, prompt_id)
    if not head:
        return None
    target = await session.get(PromptVersionRow, (prompt_id, version))
    if not target:
        return False
    head.f_current_version = version
    head.f_update_user = account_id
    head.f_update_time = _now_ms()
    await session.commit()
    await _record_for_prompt(session, prompt_id, account_id, True)
    return await _prompt_out(session, head)


# ---------- Import and export (impex): upsert under the original id; a name collision is an error ----------


async def check_import_conflict(
    session: AsyncSession,
    agent_id: str,
    agent_name: str,
    prompt_id: Optional[str],
    prompt_name: Optional[str],
    account_id: str = "",
) -> Optional[str]:
    """Read-only pre-check: returns the conflict reason, or None when there is
    none.

    An import must check before it writes. Prompt and agent used to commit
    separately, so discovering an agent name collision halfway through left no
    way back: a rollback could not undo the prompt commit, that agent reported
    failed, and the new prompt version was already live. This pre-check resolves
    both name conflicts in one pass.

    Ownership is checked here too. An import upserts by agent_id, so hitting
    somebody else's existing agent would overwrite its definition — tools,
    prompt, and model all rewritten — which is the same class of privilege
    violation as a direct PUT, only through another door. It lives in the
    pre-check rather than raising 403 to preserve the per-item semantics of an
    import: one ownership mismatch fails that item without interrupting the
    batch. Legacy rows with an unknown creator are allowed through, matching the
    trade-off the write endpoints already make.
    """
    if account_id:
        existing = await session.get(AgentRow, agent_id)
        if existing:
            owner = (existing.f_create_user or "").strip()
            if owner and owner != account_id:
                return localized_message(
                    "BknAgent.Impex.OwnedByAnotherAccount", agent_id=agent_id, owner=owner
                )
    dup_agent = (
        await session.execute(
            select(AgentRow).where(AgentRow.f_name == agent_name, AgentRow.f_agent_id != agent_id)
        )
    ).scalar_one_or_none()
    if dup_agent:
        return localized_message(
            "BknAgent.Impex.AgentNameTaken",
            agent_name=agent_name,
            holder_id=dup_agent.f_agent_id,
        )
    if prompt_id and prompt_name:
        dup_prompt = (
            await session.execute(
                select(PromptRow).where(
                    PromptRow.f_name == prompt_name, PromptRow.f_prompt_id != prompt_id
                )
            )
        ).scalar_one_or_none()
        if dup_prompt:
            return localized_message(
                "BknAgent.Impex.PromptNameTaken",
                prompt_name=prompt_name,
                holder_id=dup_prompt.f_prompt_id,
            )
    return None


async def upsert_agent_with_id(
    session: AsyncSession, agent_id: str, spec: AgentSpec, account_id: str, commit: bool = True
) -> tuple[AgentOut, str]:
    """Upsert by agent_id, which is the import semantic: idempotent, so a
    repeated import is a sync. Returns (agent, "created"|"updated") and raises
    ValueError when the same name arrives under a different id."""
    dup = (
        await session.execute(
            select(AgentRow).where(AgentRow.f_name == spec.name, AgentRow.f_agent_id != agent_id)
        )
    ).scalar_one_or_none()
    if dup:
        raise ValueError(
            localized_message(
                "BknAgent.Impex.AgentNameTaken",
                agent_name=spec.name,
                holder_id=dup.f_agent_id,
            )
        )
    now = _now_ms()
    row = await session.get(AgentRow, agent_id)
    action = "updated" if row else "created"
    if not row:
        row = AgentRow(f_agent_id=agent_id, f_create_user=account_id, f_create_time=now)
        session.add(row)
    row.f_name = spec.name
    row.f_mode = spec.mode
    row.f_prompt_id = spec.prompt_id
    row.f_prompt_vars_schema = spec.prompt_vars_schema
    row.f_model = spec.model
    row.f_tools = spec.tools
    row.f_skills = spec.skills
    row.f_limits = spec.limits.model_dump(exclude_none=True) if spec.limits else None
    row.f_status = spec.status
    row.f_update_user = account_id
    row.f_update_time = now
    if commit:
        await session.commit()
    else:
        await session.flush()
    await record_version(session, agent_id, account_id, commit=commit)
    return _to_out(row), action


async def upsert_prompt_with_id(
    session: AsyncSession,
    prompt_id: str,
    name: str,
    content: str,
    vars_schema: Optional[dict],
    account_id: str,
    commit: bool = True,
) -> str:
    """Upsert by prompt_id. When the prompt exists and its content changed, a new
    version is published so the target environment grows its own version history;
    unchanged content is a no-op. Returns "created", "version_published", or
    "unchanged", and raises ValueError when the same name arrives under a
    different id."""
    dup = (
        await session.execute(
            select(PromptRow).where(PromptRow.f_name == name, PromptRow.f_prompt_id != prompt_id)
        )
    ).scalar_one_or_none()
    if dup:
        raise ValueError(
            localized_message(
                "BknAgent.Impex.PromptNameTaken", prompt_name=name, holder_id=dup.f_prompt_id
            )
        )
    head = await session.get(PromptRow, prompt_id)
    if not head:
        now = _now_ms()
        session.add(
            PromptRow(
                f_prompt_id=prompt_id, f_name=name, f_current_version=1, f_update_user=account_id, f_update_time=now
            )
        )
        session.add(
            PromptVersionRow(
                f_prompt_id=prompt_id,
                f_version=1,
                f_content=content,
                f_vars_schema=vars_schema,
                f_create_user=account_id,
                f_create_time=now,
            )
        )
        if commit:
            await session.commit()
        else:
            await session.flush()
        return "created"
    current = await session.get(PromptVersionRow, (prompt_id, head.f_current_version))
    if current and current.f_content == content and current.f_vars_schema == vars_schema:
        return "unchanged"
    await publish_prompt_version(session, prompt_id, content, vars_schema, account_id, commit=commit)
    return "version_published"


async def reserve_execution(
    session: AsyncSession,
    run_id: str,
    agent_id: str,
    task_input: dict,
    account_id: str,
    thread_id: str,
) -> bool:
    """The existing primary key is the cross-request idempotency boundary."""
    from sqlalchemy.exc import IntegrityError

    now = _now_ms()
    session.add(
        TaskRow(
            f_task_id=run_id,
            f_agent_id=agent_id,
            f_status="pending",
            f_input=task_input,
            f_account_id=account_id,
            f_parent_thread_id=thread_id,
            f_create_time=now,
            f_update_time=now,
        )
    )
    try:
        await session.commit()
        return True
    except IntegrityError:
        await session.rollback()
        return False


async def execution_thread_input(
    session: AsyncSession, thread_id: str
) -> Optional[dict]:
    result = await session.execute(
        select(TaskRow.f_input)
        .where(TaskRow.f_parent_thread_id == thread_id)
        .order_by(TaskRow.f_create_time, TaskRow.f_task_id)
        .limit(1)
    )
    return result.scalar_one_or_none()


async def finish_execution(
    session: AsyncSession,
    run_id: str,
    status: str,
    output: Optional[str] = None,
    failure_detail: Optional[str] = None,
) -> None:
    # Stop/completion/restart recovery must never rewrite an existing terminal.
    await session.execute(
        update(TaskRow)
        .where(
            TaskRow.f_task_id == run_id, TaskRow.f_status.in_(("pending", "running"))
        )
        .values(
            f_status=status,
            f_output=output,
            f_failure_detail=failure_detail,
            f_update_time=_now_ms(),
        )
    )
    await session.commit()


async def begin_execution(session: AsyncSession, run_id: str) -> bool:
    result = await session.execute(
        update(TaskRow)
        .where(TaskRow.f_task_id == run_id, TaskRow.f_status == "pending")
        .values(f_status="running", f_update_time=_now_ms())
    )
    await session.commit()
    return result.rowcount == 1


async def put_prompt_translation(
    session: AsyncSession,
    agent_id: str,
    prompt_id: str,
    version: int,
    language: str,
    content: str,
    source_language: str,
    source_content_sha256: str,
    account_id: str,
):
    """Edit one version's translation, with the same lock order as publish/delete.

    Never record a new Prompt/Agent version or change the active pointer. Locking
    the parent serializes JSON map updates and prevents concurrent lost locales.
    """
    await lock_prompt_agents(session, prompt_id)
    agent = await _lock_agent(session, agent_id)
    head = await session.get(
        PromptRow, prompt_id, populate_existing=True, with_for_update=True
    )
    if (
        not agent
        or agent.f_prompt_id != prompt_id
        or not head
        or head.f_current_version != version
    ):
        raise err(409, "BknAgent.Prompt.TranslationStale")
    row = await session.get(
        PromptVersionRow,
        (prompt_id, version),
        populate_existing=True,
        with_for_update=True,
    )
    if row is None:
        raise err(404, "BknAgent.NotFound", resource="Prompt", ident=str(version))
    if hashlib.sha256(row.f_content.encode()).hexdigest() != source_content_sha256:
        raise err(409, "BknAgent.Prompt.TranslationStale")
    if row.f_source_language not in (None, source_language):
        raise err(409, "BknAgent.Prompt.TranslationStale")
    if language == source_language:
        raise err(400, "BknAgent.Prompt.TranslationIsSource")
    variants = dict(row.f_localized_content or {})
    variants[language] = content
    row.f_localized_content = variants
    row.f_source_language = source_language
    head.f_update_user = account_id
    head.f_update_time = _now_ms()
    await session.flush()
