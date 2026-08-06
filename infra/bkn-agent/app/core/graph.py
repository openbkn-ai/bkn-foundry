import asyncio
import json
import uuid
from typing import AsyncIterator

from langchain.agents import create_agent
from langchain_core.messages import AIMessage, AIMessageChunk, HumanMessage, ToolMessage
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao, evidence, observability
from app.config import config
from app.core import context_loader
from app.core.checkpoint import open_checkpointer
from app.core.llm import build_chat_model
from app.core.structured import structured_extract_with_path
from app.core.prompt import resolve_prompt
from app.core.skills import load_skills
from app.core.tools import apply_tool_call_cap, instrument_tool_calls, load_tools
from app.errors import err, not_found
from app.models import AgentOut, ChatRequest, ThreadMessage

# thread 级串行化（单副本内）：忙碌集合而非锁表。
# 用集合是因为策略是「忙则 409」不是排队：占位必须与检查在同一同步块里完成，
# 否则 setup 阶段的多个 await 之间会有竞态窗口，两个请求双双通过检查再排队执行，
# 交错写同一份 checkpoint；用完 discard，也不会像锁表那样按 thread_id 无限增长。
# 多副本的跨副本串行化仍待定（会话粘滞或 DB 锁）。
_busy_threads: set[str] = set()


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
    if thread_id in _busy_threads:
        raise err(
            409,
            "Thread.Busy",
            "会话正在处理中",
            f"thread {thread_id} 有未完成的 /chat 请求",
            "等待当前轮结束后重试。",
        )
    _busy_threads.add(thread_id)  # 检查与占位之间不能有 await，否则并发请求双双通过

    try:
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
        # 与 runner 同序：Context Loader 会话先开，它领到的 interaction_id 同时是
        # 证据链这一轮的 id。load_tools 会复用这个已开的会话，不重复握手。
        cl_session = None
        if context_loader.wanted(agent.tools):
            cl_session = await context_loader.open_session()
            context_loader.set_current(cl_session)
        tools = await load_tools(
            agent.tools,
            account_id,
            account_type,
            depth=0,
            parent_thread_id=thread_id,
            skill_ids=skill_ids,
        )
        tools = instrument_tool_calls(tools, account_id, account_type)
        limits = agent.limits or None
        max_turns = (
            limits.max_turns
            if limits and limits.max_turns
            else config.DEFAULT_MAX_TURNS
        )
        timeout_s = (
            limits.timeout_s
            if limits and limits.timeout_s
            else config.DEFAULT_TIMEOUT_S
        )
        max_out = limits.max_output_tokens if limits else None
        model = build_chat_model(agent.model, max_output_tokens=max_out)
        tools = apply_tool_call_cap(
            tools,
            limits.max_tool_calls if limits else None,
            account_id,
            account_type,
        )
    except BaseException:
        _busy_threads.discard(thread_id)  # setup 失败必须放位，否则该 thread 永久 409
        raise

    span_attrs = {
        "agent.id": agent.agent_id,
        "agent.name": agent.name,
        "thread.id": thread_id,
        "prompt.source": prompt_source,
        "prompt.version": prompt_version,
    }
    span_attrs.update(observability.context_attributes())

    async def _events() -> AsyncIterator[str]:
        answer_parts: list[str] = []
        tool_names: list[str] = []
        interaction_token = evidence.begin_interaction(
            req.message,
            "chat",
            agent.agent_id,
            "bkn.agent.chat",
            conversation_id=cl_session.conversation_id if cl_session else thread_id,
            interaction_id=cl_session.interaction_id if cl_session else None,
        )
        try:
            await evidence.submit_interaction_started(account_id, account_type)
            yield _sse("meta", {"thread_id": thread_id, "agent_id": agent.agent_id})
            with observability.span("agent.chat", span_attrs):
                async with open_checkpointer() as checkpointer:
                    graph = create_agent(
                        model,
                        tools,
                        system_prompt=system_prompt,
                        checkpointer=checkpointer,
                    )
                    cfg = {
                        "configurable": {"thread_id": thread_id},
                        "recursion_limit": max_turns * 2 + 1,
                    }
                    try:
                        async with asyncio.timeout(timeout_s):
                            async for chunk, meta in graph.astream(
                                {"messages": [("user", req.message)]},
                                cfg,
                                stream_mode="messages",
                            ):
                                if isinstance(chunk, AIMessageChunk):
                                    if chunk.content:
                                        answer_parts.append(
                                            chunk.content
                                            if isinstance(chunk.content, str)
                                            else str(chunk.content)
                                        )
                                        yield _sse("token", {"content": chunk.content})
                                    for tc in chunk.tool_call_chunks or []:
                                        if tc.get("name"):
                                            tool_names.append(tc["name"])
                                            yield _sse(
                                                "tool_call", {"name": tc["name"]}
                                            )
                            if req.response_format:
                                # 工具循环后单独抽结构化（原生优先→提示词降级），受同一 timeout 约束
                                state = await graph.aget_state(cfg)
                                struct_model = build_chat_model(
                                    agent.model,
                                    streaming=False,
                                    max_output_tokens=max_out,
                                )
                                (
                                    obj,
                                    validation_path,
                                ) = await structured_extract_with_path(
                                    struct_model,
                                    state.values["messages"],
                                    req.response_format,
                                    system_prompt,
                                )
                                yield _sse("structured", {"content": obj})
                                await _emit_chat_evidence(
                                    agent=agent,
                                    thread_id=thread_id,
                                    prompt_source=prompt_source,
                                    prompt_version=prompt_version,
                                    account_id=account_id,
                                    account_type=account_type,
                                    output=obj,
                                    claim_type="structured_output",
                                    response_format=req.response_format,
                                    structured_validation_path=validation_path,
                                    tool_names=tool_names,
                                )
                            elif answer_parts:
                                await _emit_chat_evidence(
                                    agent=agent,
                                    thread_id=thread_id,
                                    prompt_source=prompt_source,
                                    prompt_version=prompt_version,
                                    account_id=account_id,
                                    account_type=account_type,
                                    output="".join(answer_parts),
                                    claim_type="answer",
                                    response_format=None,
                                    structured_validation_path=None,
                                    tool_names=tool_names,
                                )
                        yield _sse("done", {"thread_id": thread_id})
                    except TimeoutError:
                        yield _sse(
                            "error",
                            {
                                "code": "BknAgent.Chat.Timeout",
                                "detail": f"超过 {timeout_s}s",
                            },
                        )
                    except Exception as e:  # 错误必须显式送到流上，不静默吞
                        yield _sse(
                            "error", {"code": "BknAgent.Chat.Failed", "detail": str(e)}
                        )
        except (
            Exception
        ) as e:  # 组装阶段（checkpointer/graph 建立）异常也要送 error，不裸断流
            yield _sse("error", {"code": "BknAgent.Chat.Failed", "detail": str(e)})
        finally:  # 正常结束、客户端断连（GeneratorExit）、异常，都要放位
            evidence.end_interaction(interaction_token)
            await context_loader.close_session(cl_session)
            _busy_threads.discard(thread_id)

    return _events()


async def _emit_chat_evidence(
    *,
    agent: AgentOut,
    thread_id: str,
    prompt_source: str,
    prompt_version: str,
    account_id: str,
    account_type: str,
    output,
    claim_type: str,
    response_format: dict | None,
    structured_validation_path: str | None,
    tool_names: list[str],
) -> None:
    cid = evidence.claim_id(claim_type, thread_id, output)
    source_event_ids, operation_ids, evidence_refs, business_refs = (
        evidence.adopted_sources()
    )
    if not source_event_ids or not operation_ids:
        return
    artifact = evidence.result_artifact(
        output,
        claim_id_value=cid,
        business_refs=business_refs,
        account_id=account_id,
        account_type=account_type,
    )
    artifact_confirmed = await evidence.submit_artifact(artifact)
    if not artifact_confirmed:
        return
    claim_event = evidence.claim_created(
        claim_id_value=cid,
        claim_type=claim_type,
        claim_hash=evidence.hash_value(output),
        operation_name="bkn.agent.chat",
        source_event_ids=source_event_ids,
        operation_ids=operation_ids,
        causation_event_id=source_event_ids[-1] if source_event_ids else None,
        result_artifact_ref=evidence.artifact_ref(artifact),
    )
    events = [claim_event]
    if evidence_refs:
        events.append(
            evidence.evidence_refs_created(
                claim_id_value=cid,
                evidence_refs=evidence_refs,
                operation_name="bkn.agent.chat",
                operation_id=operation_ids[-1] if operation_ids else None,
                causation_event_id=claim_event["event_id"] if claim_event else None,
            )
        )
    events.append(
        evidence.business_refs_resolved(
            claim_id_value=cid,
            business_refs=business_refs,
            operation_name="bkn.agent.chat",
            operation_id=operation_ids[-1],
            causation_event_id=claim_event["event_id"],
        )
    )
    await evidence.submit_events(
        [event for event in events if event], account_id, account_type
    )


def _text(content) -> str:
    if isinstance(content, str):
        return content
    return "".join(
        p.get("text", "") if isinstance(p, dict) else str(p) for p in content
    )


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
