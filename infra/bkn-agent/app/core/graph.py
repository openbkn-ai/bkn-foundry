import asyncio
import json
import logging
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
from app.commons.i18n import build_error_content
from app.errors import err, not_found
from app.models import AgentOut, ChatRequest, ThreadMessage

# thread 级串行化（单副本内）：忙碌集合而非锁表。
# 用集合是因为策略是「忙则 409」不是排队：占位必须与检查在同一同步块里完成，
# 否则 setup 阶段的多个 await 之间会有竞态窗口，两个请求双双通过检查再排队执行，
# 交错写同一份 checkpoint；用完 discard，也不会像锁表那样按 thread_id 无限增长。
# 多副本的跨副本串行化仍待定（会话粘滞或 DB 锁）。
logger = logging.getLogger("bkn-agent.chat")

_busy_threads: set[str] = set()


def _sse(event: str, data: dict) -> str:
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n"


def _stream_error(message_key: str, detail: str | None = None, **params) -> dict:
    """Build one SSE error event in the locale frozen for this request.

    The stream is opened inside the request context, so the generator still
    reads the same effective locale after the middleware has returned. ``code``
    stays the machine contract; ``detail`` may carry raw upstream text, which is
    left untranslated on purpose.
    """
    content = build_error_content(message_key, **params)
    return {"code": content["code"], "detail": detail if detail is not None else content["detail"]}


async def stream_chat(
    session: AsyncSession,
    agent: AgentOut,
    req: ChatRequest,
    account_id: str,
    account_type: str,
) -> AsyncIterator[str]:
    thread_id = req.thread_id or str(uuid.uuid4())
    if thread_id in _busy_threads:
        raise err(409, "BknAgent.Thread.Busy", thread_id=thread_id)
    _busy_threads.add(thread_id)  # 检查与占位之间不能有 await，否则并发请求双双通过

    # 在 try 之前绑定：setup 早期抛异常时 except 分支也要能安全引用
    cl_session = None

    try:
        thread_row = await dao.get_thread_row(session, thread_id)
        if thread_row:
            if thread_row.f_account_id != account_id:  # 不泄露存在性，与查不到同响应
                raise not_found("thread", thread_id)
            if thread_row.f_agent_id != agent.agent_id:
                raise err(
                    400,
                    "BknAgent.Thread.AgentMismatch",
                    thread_id=thread_id,
                    owner_agent_id=thread_row.f_agent_id,
                )
        await dao.touch_thread(session, thread_id, agent.agent_id, account_id)

        system_prompt, prompt_source, prompt_version = await resolve_prompt(
            session, agent, account_id, req.prompt_override, req.prompt_vars
        )
        skill_ids = list(dict.fromkeys([*agent.skills, *req.skills]))
        system_prompt += await load_skills(skill_ids, account_id, account_type)
        # 与 runner 同序：Context Loader 会话先开，它领到的 interaction_id 同时是
        # 证据链这一轮的 id。load_tools 会复用这个已开的会话，不重复握手。
        if context_loader.wanted(agent.tools):
            # thread_id 当会话锚：同一 thread 的多轮归进同一个 conversation
            cl_session = await context_loader.open_session(
                req.message, agent_name=agent.name, host_conversation_key=thread_id
            )
            if cl_session is not None:
                # 只在真开出会话时置位。ContextVar 的作用域就只有 load_tools
                # 这一段——tools.py 靠它拿会话——所以设与复位都收在这里，
                # 且必须在同一个 context 里成对出现。
                #
                # 早先是在 _events() 的 finally 里复位的：那是个异步生成器，跑在
                # 复制出来的独立 context，token 跨 context 复位直接抛
                # ValueError: Token was created in a different Context，
                # 而且它抛在 finally 开头，把后面的兜底收尾一并废掉。
                # 设了就不复位。
                #
                # 上一版把它收窄到只活过 load_tools，结果 agent-as-tool 的子 agent
                # 在执行期 current_session() 恒为 None，带着零个 CL 工具作答，
                # 而且不报错不告警。ContextVar 的作用域是整轮，不是装载那一段。
                #
                # 不复位是安全的：它活在这个请求自己的 context 里，随请求消亡；
                # 而跨 context 复位（生成器 / aclose 任务）必抛 ValueError。
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
        # setup 阶段抛异常时 _events() 根本没被构造，它的 finally 也就永远不会跑。
        # 握手若已经成功，那次交互会永久停在 active——而且是确定性的：同一个请求
        # 每重试一次就再泄一个。这里补收尾。
        #
        # 收尾自身再出错不能改写原始异常：调用方要看到的是 setup 为什么失败，
        # 不是收尾为什么失败。
        try:
            await context_loader.close_session(
                cl_session, outcome="failed", reason="会话建立阶段失败，本轮未开始"
            )
        except BaseException as close_err:  # noqa: BLE001 - 收尾失败不得掩盖原始异常
            logger.warning("[ContextLoader] setup 失败后的收尾也失败了：%s", close_err)
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
        # 收尾要报的终态。_completed 只在真正走到 done 时置位；结构化输出那条
        # 路的答案不在 answer_parts 里，单独记进 _final_answer。
        _completed = False
        _final_answer: str | None = None
        _failure_reason: str | None = "本轮未产出回复（异常或客户端断连）"
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
                                _final_answer = json.dumps(obj, ensure_ascii=False)
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
                        _completed = True
                        # 在 yield done 之前收尾，不能留给 finally。
                        #
                        # SSE 的 finally 挂在异步生成器的终结上：客户端收完就断，
                        # 驱动生成器的任务被取消，生成器停在 yield 上，finally 要等
                        # GC 触发 aclose 才跑——甚至可能不跑。VM 三轮实测的表现是
                        # 第一轮开的交互一直 active，第二、三轮全被
                        # interaction_in_progress 挡掉。跨服务状态的释放不能依赖
                        # 生成器终结时机。close_session 是幂等的，finally 那次是兜底。
                        _answer_now = (
                            _final_answer if _final_answer is not None else "".join(answer_parts)
                        )
                        if _answer_now:
                            await context_loader.close_session(
                                cl_session, outcome="completed", answer=_answer_now
                            )
                        yield _sse("done", {"thread_id": thread_id})
                    except TimeoutError:
                        yield _sse("error", _stream_error("BknAgent.Chat.Timeout", timeout=timeout_s))
                    except Exception as e:  # 错误必须显式送到流上，不静默吞
                        yield _sse("error", _stream_error("BknAgent.Chat.Failed", detail=str(e)))
        except (
            Exception
        ) as e:  # 组装阶段（checkpointer/graph 建立）异常也要送 error，不裸断流
            yield _sse("error", _stream_error("BknAgent.Chat.Failed", detail=str(e)))
        finally:  # 正常结束、客户端断连（GeneratorExit）、异常，都要放位
            # 放位是 finally 的第一条，前面不许有任何会抛的语句。
            #
            # 排在它前面的每一条都是地雷：close_session 只 catch Exception，
            # 挡不住断连的 CancelledError 与 MCP TaskGroup 的 BaseExceptionGroup；
            # evidence.end_interaction 是 ContextVar 复位，而这段 finally 可能由
            # GC 触发的 aclose 任务驱动——那是另一个 context，复位必抛 ValueError。
            # 任何一个抛出来，thread 就永久停在 409 Thread.Busy 直到进程重启。
            _busy_threads.discard(thread_id)
            try:
                evidence.end_interaction(interaction_token)
            except ValueError as e:  # 跨 context 复位；证据链这一轮已经落完
                logger.warning("[Chat] 证据链交互复位失败（跨 context）：%s", e)
            # ContextVar 不在这里复位：本函数体可能跑在别的 context 里。
            # 它随请求 context 消亡，不需要显式复位。
            # completed 必须带 answer（不带会被 closure_manifest_invalid 拒）。
            # 用显式的成功标记而不是「答案非空」：结构化输出那条路走 structured
            # 事件、answer_parts 是空的，按空判会把成功的一轮误报成 failed；
            # 反过来流了一半再超时，答案非空却不是完成。
            _answer = _final_answer if _final_answer is not None else "".join(answer_parts)
            _ok = _completed and bool(_answer)
            await context_loader.close_session(
                cl_session,
                outcome="completed" if _ok else "failed",
                answer=_answer if _ok else None,
                reason=None if _ok else _failure_reason,
            )

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
