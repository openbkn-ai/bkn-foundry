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

# Thread-level serialization within one replica: a busy set rather than a lock
# table. A set fits because the policy is "busy means 409", not queueing: the
# claim has to happen in the same synchronous block as the check, otherwise the
# awaits during setup open a race window where two requests both pass the check,
# then queue up and interleave writes into the same checkpoint. Entries are
# discarded after use, so unlike a lock table it does not grow without bound per
# thread_id. Cross-replica serialization is still undecided: session stickiness
# or a database lock.
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
    _busy_threads.add(thread_id)  # No await may sit between the check and the claim, or concurrent requests both pass

    # Bound before the try so the except branch can reference it safely when
    # setup raises early.
    cl_session = None

    try:
        thread_row = await dao.get_thread_row(session, thread_id)
        if thread_row:
            if thread_row.f_account_id != account_id:  # Do not disclose existence; answer exactly like a missing thread
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
        # Same order as the runner: the Context Loader session opens first, and
        # the interaction_id it receives is also the id of this evidence-chain
        # turn. load_tools reuses that open session rather than handshaking
        # again.
        if context_loader.wanted(agent.tools):
            # thread_id anchors the session, so every turn of one thread lands
            # in the same conversation.
            cl_session = await context_loader.open_session(
                req.message, agent_name=agent.name, host_conversation_key=thread_id
            )
            if cl_session is not None:
                # Set only when a session was really opened.
                #
                # An earlier version reset this in the finally of _events(). That
                # is an async generator running in its own copied context, and
                # resetting the token across contexts raises
                # ValueError: Token was created in a different Context — thrown
                # at the top of the finally, which also wiped out the cleanup
                # that followed. So it is set and never reset.
                #
                # The version before that narrowed its lifetime to load_tools
                # only, and as a result the sub-agent of an agent-as-tool saw
                # current_session() as None throughout execution and answered
                # with zero CL tools, silently and without a warning. The scope
                # of this ContextVar is the whole turn, not just the loading
                # phase.
                #
                # Not resetting is safe: it lives in this request's own context
                # and dies with the request, whereas a cross-context reset (from
                # the generator or an aclose task) always raises ValueError.
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
        _busy_threads.discard(thread_id)  # A failed setup must release the claim, or the thread answers 409 forever
        # When setup raises, _events() was never constructed, so its finally can
        # never run. If the handshake had already succeeded, that interaction
        # would stay active forever — deterministically so: every retry of the
        # same request leaks one more. This performs the cleanup instead.
        #
        # A failure inside the cleanup must not rewrite the original exception:
        # the caller needs to see why setup failed, not why cleanup did.
        try:
            await context_loader.close_session(
                cl_session, outcome="failed", reason="session setup failed; this turn never started"
            )
        except BaseException as close_err:  # noqa: BLE001 - a failed cleanup must not mask the original exception
            logger.warning(
                "[ContextLoader] cleanup after a failed setup also failed: %s", close_err
            )
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
        # The terminal state the cleanup reports. _completed is set only when the
        # run actually reaches done, and the answer on the structured-output path
        # is not in answer_parts, so it is recorded separately in _final_answer.
        _completed = False
        _final_answer: str | None = None
        _failure_reason: str | None = "this turn produced no reply (an exception or a client disconnect)"
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
                                # Extract the structure separately after the tool loop (native first,
                                # then the prompt fallback), under the same timeout.
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
                        # Clean up before yielding done; it cannot be left to the
                        # finally.
                        #
                        # For SSE the finally hangs off the finalization of an
                        # async generator: the client disconnects as soon as it
                        # has read everything, the driving task is cancelled, the
                        # generator stays parked on a yield, and the finally only
                        # runs once GC triggers aclose — if it runs at all. Over
                        # three turns on the VM the observable result was that the
                        # interaction opened in turn one stayed active and turns
                        # two and three were both blocked by
                        # interaction_in_progress. Releasing cross-service state
                        # must not depend on when a generator is finalized.
                        # close_session is idempotent, so the one in the finally
                        # is only a backstop.
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
                    except Exception as e:  # Errors must be sent explicitly onto the stream, never swallowed
                        yield _sse("error", _stream_error("BknAgent.Chat.Failed", detail=str(e)))
        except (
            Exception
        ) as e:  # An assembly-stage failure (building the checkpointer or graph) must also
            # emit error rather than cutting the stream bare.
            yield _sse("error", _stream_error("BknAgent.Chat.Failed", detail=str(e)))
        finally:  # A normal end, a client disconnect (GeneratorExit), and an exception all release the claim
            # Releasing the claim is the first statement of the finally; nothing
            # that can raise may precede it.
            #
            # Everything that could sit in front of it is a mine. close_session
            # catches only Exception, so it stops neither the CancelledError of a
            # disconnect nor the BaseExceptionGroup of an MCP TaskGroup.
            # evidence.end_interaction resets a ContextVar, while this finally may
            # be driven by a GC-triggered aclose task — a different context, where
            # the reset always raises ValueError. If either one raises, the thread
            # stays stuck on 409 Thread.Busy until the process restarts.
            _busy_threads.discard(thread_id)
            try:
                evidence.end_interaction(interaction_token)
            except ValueError as e:  # A cross-context reset; this evidence-chain turn is already persisted
                logger.warning(
                    "[Chat] resetting the evidence interaction failed "
                    "(across contexts): %s", e
                )
            # The ContextVar is not reset here, because this function body may
            # run in a different context. It dies with the request context, so no
            # explicit reset is needed.
            # completed must carry an answer; without one it is rejected as
            # closure_manifest_invalid. Success is tracked by an explicit flag
            # rather than by "the answer is non-empty": the structured-output path
            # goes through the structured event and leaves answer_parts empty, so
            # an emptiness test would report a successful turn as failed — and
            # conversely, a stream that timed out halfway has a non-empty answer
            # without having completed.
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
    """Thread history reads the latest checkpointer checkpoint directly; the
    ownership check lives in the router layer."""
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
