import time
import uuid
from typing import Optional

from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models import (
    AgentOut,
    AgentRow,
    AgentSpec,
    PromptOverrideRow,
    PromptRow,
    PromptVersionRow,
    TaskOut,
    TaskRow,
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
        f_agent_id=str(uuid.uuid4()),
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


async def get_task(session: AsyncSession, task_id: str) -> Optional[TaskOut]:
    row = await session.get(TaskRow, task_id)
    return _task_out(row) if row else None


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


async def get_default_prompt(
    session: AsyncSession, prompt_id: str
) -> Optional[tuple[str, Optional[dict], int]]:
    """agent 默认层：t_agent_prompt.current_version 对应版本正文。"""
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


# ---- prompt 管理面（版本化，只增不改） ----


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


async def create_prompt(session: AsyncSession, name: str, content: str, vars_schema: Optional[dict], account_id: str):
    now = _now_ms()
    prompt_id = str(uuid.uuid4())
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
    session: AsyncSession, prompt_id: str, content: str, vars_schema: Optional[dict], account_id: str
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
    await session.commit()
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
    """回滚 = current_version 指回旧版本；版本行只增不改。"""
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
