from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core import runner
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import InvokeRequest, RunRequest, TaskOut

router = APIRouter()


@router.post("/invoke/{agent_id}", response_model=TaskOut)
async def invoke(
    agent_id: str,
    req: InvokeRequest,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    """Run once synchronously and return only when the task reaches a terminal state.

    Only a published agent may be called; a draft answers exactly like a missing
    one. Both chat and task modes are accepted. Execution is a single stateless
    turn and no thread is persisted, while the task record is still stored so the
    run stays observable.
    """
    agent = await dao.get_agent(session, agent_id)
    if not agent or agent.status != "published":
        raise not_found("agent", agent_id)

    task_input = {
        "message": req.message,
        "prompt_vars": req.prompt_vars,
        "skills": req.skills,
        "prompt_override": req.prompt_override,
        "response_format": req.response_format,
    }
    task = await dao.create_task(session, agent.agent_id, task_input, account.account_id)
    await runner.execute_task(task.task_id, agent, task_input, account.account_id, account.account_type)
    session.expire_all()  # The runner writes the terminal state in its own session, so bypass this cache.
    return await dao.get_task(session, task.task_id)


@router.post("/run", response_model=TaskOut)
async def run(
    req: RunRequest,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await dao.get_agent(session, req.agent_id)
    if not agent:
        raise not_found("agent", req.agent_id)
    if agent.mode != "task":
        raise bad_request("BknAgent.Task.ModeMismatch", agent_id=req.agent_id, mode=agent.mode)

    task_input = {
        "message": req.message,
        "prompt_vars": req.prompt_vars,
        "skills": req.skills,
        "prompt_override": req.prompt_override,
        "response_format": req.response_format,
    }
    task = await dao.create_task(session, agent.agent_id, task_input, account.account_id)
    runner.submit_task(task.task_id, agent, task_input, account.account_id, account.account_type)
    return task


@router.get("/tasks/{task_id}", response_model=TaskOut)
async def get_task(
    task_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    task = await dao.get_task(session, task_id, account_id=account.account_id)
    if not task:  # A non-owner answers like a missing task; existence is not disclosed.
        raise not_found("task", task_id)
    return task
