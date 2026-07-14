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
    """同步一次性执行，等到终态才返回（算子工厂 toolbox 工具经此调用）。

    仅 published agent 可调（draft 与不存在同响应）；chat/task 模式均可，
    执行为无状态单轮，不落 thread。任务记录照常落库可监控。
    """
    agent = await dao.get_agent(session, agent_id)
    if not agent or agent.status != "published":
        raise not_found("agent", agent_id)

    task_input = {
        "message": req.message,
        "prompt_vars": req.prompt_vars,
        "skills": req.skills,
        "prompt_override": req.prompt_override,
    }
    task = await dao.create_task(session, agent.agent_id, task_input, account.account_id)
    await runner.execute_task(task.task_id, agent, task_input, account.account_id, account.account_type)
    session.expire_all()  # 终态由 runner 在独立 session 写入，绕过本 session 缓存
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
    task = await dao.get_task(session, task_id, account_id=account.account_id)
    if not task:  # 非 owner 与不存在同响应，不泄露存在性
        raise not_found("task", task_id)
    return task
