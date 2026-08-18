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
    "SSE event stream: meta({thread_id, agent_id}) comes first; within the answer "
    "turn token({content}) and tool_call({name}) interleave; when response_format "
    "is set, one structured({content: object matching the schema}) event precedes "
    "done; a normal stream ends with done({thread_id}); a failed one ends with "
    "error({code, detail}) — failures are never silent. The human-readable error "
    "text follows the request Accept-Language; code stays stable."
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
        raise bad_request("BknAgent.Chat.ModeMismatch", agent_id=req.agent_id, mode=agent.mode)

    events = await stream_chat(session, agent, req, account.account_id, account.account_type)
    return StreamingResponse(
        events,
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
