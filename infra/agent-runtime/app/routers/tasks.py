from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core import runner
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import RunRequest, TaskOut

router = APIRouter()


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
        raise bad_request("Mode", "该 agent 不是一次性模式", f"agent {req.agent_id} mode={agent.mode}", "对话 agent 走 /chat。")

    task_input = {
        "message": req.message,
        "prompt_vars": req.prompt_vars,
        "skills": req.skills,
        "prompt_override": req.prompt_override,
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
    task = await dao.get_task(session, task_id)
    if not task:
        raise not_found("task", task_id)
    return task
