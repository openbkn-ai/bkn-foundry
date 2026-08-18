import time
import uuid
from typing import Optional

from sqlalchemy import delete, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.commons.i18n import localized_message
from app.models import (
    AgentOut,
    AgentRow,
    AgentSpec,
    PromptOverrideRow,
    PromptRow,
    PromptVersionRow,
    TaskOut,
    TaskRow,
    ThreadRow,
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
    from sqlalchemy import func

    total = (await session.execute(select(func.count()).select_from(AgentRow))).scalar_one()
    return [_to_out(r) for r in rows], total


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
    return version.f_content, version.f_vars_schema, version.f_version


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


# ---- Prompt management surface: versioned, append-only ----


async def _prompt_out(session: AsyncSession, head: PromptRow):
    from app.models import PromptOut

    version = await session.get(PromptVersionRow, (head.f_prompt_id, head.f_current_version))
    return PromptOut(
        prompt_id=head.f_prompt_id,
        name=head.f_name,
        current_version=head.f_current_version,
        content=version.f_content if version else "",
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


async def get_prompt(session: AsyncSession, prompt_id: str):
    head = await session.get(PromptRow, prompt_id)
    return await _prompt_out(session, head) if head else None


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
):
    head = await session.get(PromptRow, prompt_id)
    if not head:
        return None
    latest = (
        await session.execute(
            select(PromptVersionRow.f_version)
            .where(PromptVersionRow.f_prompt_id == prompt_id)
            .order_by(PromptVersionRow.f_version.desc())
            .limit(1)
        )
    ).scalar_one()
    now = _now_ms()
    session.add(
        PromptVersionRow(
            f_prompt_id=prompt_id,
            f_version=latest + 1,
            f_content=content,
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
    return await _prompt_out(session, head)


async def list_prompt_versions(session: AsyncSession, prompt_id: str):
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
            content=r.f_content,
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
