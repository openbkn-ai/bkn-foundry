from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core.graph import stream_chat
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import ChatRequest

router = APIRouter()


_SSE_DOC = (
    "SSE 事件流：meta({thread_id, agent_id}) 先行；正文轮内 token({content}) 与 "
    "tool_call({name}) 交替；正常收尾 done({thread_id})；失败以 error({code, detail}) "
    "收尾（错误不静默）。"
)


@router.post(
    "/chat",
    responses={200: {"description": _SSE_DOC, "content": {"text/event-stream": {"schema": {"type": "string"}}}}},
)
async def chat(
    req: ChatRequest,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await dao.get_agent(session, req.agent_id)
    if not agent:
        raise not_found("agent", req.agent_id)
    if agent.mode != "chat":
        raise bad_request("Mode", "该 agent 不是对话模式", f"agent {req.agent_id} mode={agent.mode}", "task agent 走 /run（M3）。")

    events = await stream_chat(session, agent, req, account.account_id, account.account_type)
    return StreamingResponse(
        events,
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
