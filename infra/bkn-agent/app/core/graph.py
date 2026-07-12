import asyncio
import json
import uuid
from typing import AsyncIterator

from langchain_core.messages import AIMessage, AIMessageChunk, HumanMessage, ToolMessage
from langgraph.prebuilt import create_react_agent
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao, observability
from app.config import config
from app.core.checkpoint import open_checkpointer
from app.core.llm import build_chat_model
from app.core.prompt import resolve_prompt
from app.core.skills import load_skills
from app.core.tools import load_tools
from app.errors import err, not_found
from app.models import AgentOut, ChatRequest, ThreadMessage

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

    thread_row = await dao.get_thread_row(session, thread_id)
    if thread_row:
        if thread_row.f_account_id != account_id:  # 不泄露存在性，与查不到同响应
            raise not_found("thread", thread_id)
        if thread_row.f_agent_id != agent.agent_id:
            raise err(
                400,
                "Thread.AgentMismatch",
                "thread 归属其他 agent",
                f"thread {thread_id} 建立于 agent {thread_row.f_agent_id}",
                "同一 thread 只能续接创建它的 agent；换 agent 请开新 thread。",
            )
    await dao.touch_thread(session, thread_id, agent.agent_id, account_id)

    system_prompt, prompt_source, prompt_version = await resolve_prompt(
        session, agent, account_id, req.prompt_override, req.prompt_vars
    )
    skill_ids = list(dict.fromkeys([*agent.skills, *req.skills]))
    system_prompt += await load_skills(skill_ids, account_id, account_type)
    tools = await load_tools(agent.tools, account_id, account_type, depth=0, parent_thread_id=thread_id)
    model = build_chat_model(agent.model)

    limits = agent.limits or None
    max_turns = (limits.max_turns if limits and limits.max_turns else config.DEFAULT_MAX_TURNS)
    timeout_s = (limits.timeout_s if limits and limits.timeout_s else config.DEFAULT_TIMEOUT_S)

    span_attrs = {
        "agent.id": agent.agent_id,
        "agent.name": agent.name,
        "thread.id": thread_id,
        "prompt.source": prompt_source,
        "prompt.version": prompt_version,
    }

    async def _events() -> AsyncIterator[str]:
        yield _sse("meta", {"thread_id": thread_id, "agent_id": agent.agent_id})
        async with lock:
            with observability.span("agent.chat", span_attrs):
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
                        yield _sse("error", {"code": "BknAgent.Chat.Timeout", "detail": f"超过 {timeout_s}s"})
                    except Exception as e:  # 错误必须显式送到流上，不静默吞
                        yield _sse("error", {"code": "BknAgent.Chat.Failed", "detail": str(e)})

    return _events()


def _text(content) -> str:
    if isinstance(content, str):
        return content
    return "".join(p.get("text", "") if isinstance(p, dict) else str(p) for p in content)


async def read_thread_messages(thread_id: str) -> list[ThreadMessage]:
    """会话历史直读 checkpointer 最新 checkpoint；归属校验在路由层。"""
    async with open_checkpointer() as checkpointer:
        tup = await checkpointer.aget_tuple({"configurable": {"thread_id": thread_id}})
    if not tup:
        return []
    out: list[ThreadMessage] = []
    for m in tup.checkpoint.get("channel_values", {}).get("messages", []):
        if isinstance(m, HumanMessage):
            out.append(ThreadMessage(role="user", content=_text(m.content)))
        elif isinstance(m, AIMessage):
            out.append(
                ThreadMessage(
                    role="assistant",
                    content=_text(m.content),
                    tool_calls=[tc["name"] for tc in (m.tool_calls or [])],
                )
            )
        elif isinstance(m, ToolMessage):
            out.append(ThreadMessage(role="tool", content=_text(m.content)))
    return out
