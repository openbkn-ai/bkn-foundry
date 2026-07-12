import asyncio
import json
import uuid
from typing import AsyncIterator, Optional

from langchain_core.messages import AIMessageChunk
from langgraph.prebuilt import create_react_agent
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config
from app.core.checkpoint import open_checkpointer
from app.core.llm import build_chat_model
from app.core.prompt import resolve_prompt
from app.core.skills import load_skills
from app.core.tools import load_tools
from app.errors import err
from app.models import AgentOut, ChatRequest

# thread 级串行化（单副本内）。多副本部署的跨副本串行化随 M6 落定（会话粘滞或 DB 锁）。
_thread_locks: dict[str, asyncio.Lock] = {}


def _sse(event: str, data: dict) -> str:
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n"


async def stream_chat(
    session: AsyncSession,
    agent: AgentOut,
    req: ChatRequest,
    account_id: str,
    account_type: str,
) -> AsyncIterator[str]:
    thread_id = req.thread_id or str(uuid.uuid4())
    lock = _thread_locks.setdefault(thread_id, asyncio.Lock())
    if lock.locked():
        raise err(
            409,
            "Thread.Busy",
            "会话正在处理中",
            f"thread {thread_id} 有未完成的 /chat 请求",
            "等待当前轮结束后重试。",
        )

    system_prompt = await resolve_prompt(session, agent, account_id, req.prompt_override, req.prompt_vars)
    skill_ids = list(dict.fromkeys([*agent.skills, *req.skills]))
    system_prompt += await load_skills(skill_ids, account_id, account_type)
    tools = await load_tools(agent.tools, account_id, account_type)
    model = build_chat_model(agent.model)

    limits = agent.limits or None
    max_turns = (limits.max_turns if limits and limits.max_turns else config.DEFAULT_MAX_TURNS)
    timeout_s = (limits.timeout_s if limits and limits.timeout_s else config.DEFAULT_TIMEOUT_S)

    async def _events() -> AsyncIterator[str]:
        yield _sse("meta", {"thread_id": thread_id, "agent_id": agent.agent_id})
        async with lock:
            async with open_checkpointer() as checkpointer:
                graph = create_react_agent(model, tools, prompt=system_prompt, checkpointer=checkpointer)
                cfg = {
                    "configurable": {"thread_id": thread_id},
                    "recursion_limit": max_turns * 2 + 1,
                }
                try:
                    async with asyncio.timeout(timeout_s):
                        async for chunk, meta in graph.astream(
                            {"messages": [("user", req.message)]}, cfg, stream_mode="messages"
                        ):
                            if isinstance(chunk, AIMessageChunk):
                                if chunk.content:
                                    yield _sse("token", {"content": chunk.content})
                                for tc in chunk.tool_call_chunks or []:
                                    if tc.get("name"):
                                        yield _sse("tool_call", {"name": tc["name"]})
                    yield _sse("done", {"thread_id": thread_id})
                except TimeoutError:
                    yield _sse("error", {"code": "AgentRuntime.Chat.Timeout", "detail": f"超过 {timeout_s}s"})
                except Exception as e:  # 错误必须显式送到流上，不静默吞
                    yield _sse("error", {"code": "AgentRuntime.Chat.Failed", "detail": str(e)})

    return _events()


def get_history_config(thread_id: str) -> Optional[dict]:
    return {"configurable": {"thread_id": thread_id}}
