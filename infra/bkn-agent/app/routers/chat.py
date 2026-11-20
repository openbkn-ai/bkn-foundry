from collections.abc import AsyncIterator

from fastapi import APIRouter, Depends, Request
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core import llm
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
    head_request: Request,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    agent = await dao.get_agent(session, req.agent_id)
    if not agent:
        raise not_found("agent", req.agent_id)
    if agent.mode != "chat":
        raise bad_request("BknAgent.Chat.ModeMismatch", agent_id=req.agent_id, mode=agent.mode)

    # x-bkn-agent-thinking-mode turns the model's reasoning on or off for this turn, as for tasks: a
    # caller with a small output budget turns it off so reasoning cannot spend the whole budget. The
    # chat model is built both while the stream is set up and while it runs, so both see it.
    thinking_mode = head_request.headers.get("x-bkn-agent-thinking-mode", "")
    thinking_token = llm.set_thinking_mode(thinking_mode)
    try:
        events = await stream_chat(session, agent, req, account.account_id, account.account_type)
    finally:
        llm.reset_thinking_mode(thinking_token)
    return StreamingResponse(
        _with_thinking_mode(events, thinking_mode),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


async def _with_thinking_mode(events: AsyncIterator[str], thinking_mode: str) -> AsyncIterator[str]:
    """Runs the stream with the turn's thinking mode, which the request's own context no longer holds
    once the handler has returned."""
    token = llm.set_thinking_mode(thinking_mode)
    try:
        async for event in events:
            yield event
    finally:
        try:
            llm.reset_thinking_mode(token)
        except ValueError:
            # Closed from another context, as on a client disconnect: the value goes with this one.
            pass
