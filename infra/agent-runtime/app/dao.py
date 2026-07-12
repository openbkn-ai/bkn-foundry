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


async def get_default_prompt(session: AsyncSession, prompt_id: str) -> Optional[tuple[str, Optional[dict]]]:
    """agent 默认层：t_agent_prompt.current_version 对应版本正文。"""
    head = await session.get(PromptRow, prompt_id)
    if not head:
        return None
    version = await session.get(PromptVersionRow, (prompt_id, head.f_current_version))
    if not version:
        return None
    return version.f_content, version.f_vars_schema


async def get_prompt_override(session: AsyncSession, agent_id: str, account_id: str) -> Optional[str]:
    row = await session.get(PromptOverrideRow, (agent_id, account_id))
    return row.f_content if row else None
