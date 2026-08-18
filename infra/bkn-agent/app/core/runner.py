import asyncio
import json
from typing import Any, Optional

from langchain.agents import create_agent
from langchain_core.messages import AIMessage, ToolMessage

from app import dao, evidence, observability
from app.config import config
from app.core import context_loader
from app.core.llm import build_chat_model
from app.core.structured import structured_extract_with_path
from app.core.prompt import resolve_prompt
from app.core.skills import load_skills
from app.db import SessionLocal
from app.errors import err
from app.models import AgentOut

MAX_AGENT_DEPTH = 3

# In-process references to background tasks, held to keep them from being
# garbage collected. Crash-recovery semantics (pending/running tasks marked
# failed after a restart) were settled with M6.
_background: set[asyncio.Task] = set()


async def run_agent_once(
    agent: AgentOut,
    message: str,
    prompt_vars: dict[str, Any],
    skills: list[str],
    prompt_override: Optional[str],
    account_id: str,
    account_type: str,
    depth: int,
    response_format: Optional[dict[str, Any]] = None,
    task_id: Optional[str] = None,
) -> str:
    if evidence.has_interaction():
        # The parent turn already has an interaction open; agent-as-tool takes
        # this path. The evidence chain reuses the parent id, but a Context
        # Loader session may not exist: when the parent agent declares no
        # context_loader, current_session() is None and the sub-agent would
        # answer with zero CL tools — and because open_session was never called,
        # not even the warning appears.
        #
        # So this restores "whoever opens it closes it": inherit when possible
        # (no second handshake, and never close someone else's session), and
        # when there is nothing to inherit but the tools are genuinely needed,
        # open one here and close it here.
        inherited = context_loader.current_session()
        own = None
        own_token = None
        if inherited is None and context_loader.wanted(agent.tools):
            own = await context_loader.open_session(message, agent_name=agent.name)
            if own is not None:
                own_token = context_loader.set_current(own)
        sub_answer: str | None = None
        sub_failure: str | None = None
        try:
            sub_answer = await _run_agent_once_core(
                agent,
                message,
                prompt_vars,
                skills,
                prompt_override,
                account_id,
                account_type,
                depth,
                response_format,
                task_id,
            )
            return sub_answer
        except Exception as e:
            sub_failure = f"{type(e).__name__}: {e}"
            raise
        finally:
            if own_token is not None:
                context_loader.reset_current(own_token)
            # Only close the session opened here; an inherited one belongs to its opener
            await context_loader.close_session(
                own,
                outcome="completed" if sub_answer is not None else "failed",
                answer=sub_answer,
                reason=sub_failure or (None if sub_answer is not None else "the sub-agent produced no reply"),
            )
    # The Context Loader session must open before begin_interaction: the
    # interaction_id it receives is also the id of this evidence-chain turn, and
    # only sharing one pair keeps the trace from splitting.
    cl_session = None
    cl_token = None
    if context_loader.wanted(agent.tools):
        # No host_conversation_key: /run and /invoke are one-shot executions and
        # each should form its own conversation. Only chat has multi-turn
        # continuity, where graph.py passes thread_id.
        cl_session = await context_loader.open_session(message, agent_name=agent.name)
        cl_token = context_loader.set_current(cl_session)
    token = evidence.begin_interaction(
        message,
        "task",
        agent.agent_id,
        "bkn.agent.task",
        conversation_id=cl_session.conversation_id if cl_session else None,
        interaction_id=cl_session.interaction_id if cl_session else None,
    )
    answer: str | None = None
    failure: str | None = None
    try:
        await evidence.submit_interaction_started(account_id, account_type)
        answer = await _run_agent_once_core(
            agent,
            message,
            prompt_vars,
            skills,
            prompt_override,
            account_id,
            account_type,
            depth,
            response_format,
            task_id,
        )
        return answer
    except Exception as e:
        failure = f"{type(e).__name__}: {e}"
        raise
    finally:
        evidence.end_interaction(token)
        # answer is mandatory when outcome=completed — omitting it is rejected
        # as closure_manifest_invalid — so this turn's answer has to be carried
        # all the way here and cannot be conjured up in a finally block.
        await context_loader.close_session(
            cl_session,
            outcome="completed" if answer is not None else "failed",
            answer=answer,
            reason=failure or (None if answer is not None else "this turn produced no reply"),
        )
        if cl_token is not None:
            context_loader.reset_current(cl_token)


async def _run_agent_once_core(
    agent: AgentOut,
    message: str,
    prompt_vars: dict[str, Any],
    skills: list[str],
    prompt_override: Optional[str],
    account_id: str,
    account_type: str,
    depth: int,
    response_format: Optional[dict[str, Any]] = None,
    task_id: Optional[str] = None,
) -> str:
    """One-shot stateless execution: a single graph run with no checkpointer,
    shared by /run and agent-as-tool.

    A non-empty response_format (a JSON Schema) selects structured output: after
    the tool loop it makes one more structured call and returns the serialized
    JSON string. Otherwise it returns the last AI text reply.
    """
    if depth > MAX_AGENT_DEPTH:
        raise err(409, "BknAgent.Task.DepthExceeded", limit=MAX_AGENT_DEPTH)

    from app.core.tools import apply_tool_call_cap, instrument_tool_calls, load_tools  # Imported late to break the tools <-> runner cycle

    async with SessionLocal() as session:
        system_prompt, prompt_source, prompt_version = await resolve_prompt(
            session, agent, account_id, prompt_override, prompt_vars
        )
    skill_ids = list(dict.fromkeys([*agent.skills, *skills]))
    system_prompt += await load_skills(skill_ids, account_id, account_type)
    tools = await load_tools(
        agent.tools, account_id, account_type, depth=depth, skill_ids=skill_ids
    )
    tools = instrument_tool_calls(tools, account_id, account_type)
    limits = agent.limits
    max_turns = limits.max_turns if limits and limits.max_turns else config.DEFAULT_MAX_TURNS
    timeout_s = limits.timeout_s if limits and limits.timeout_s else config.DEFAULT_TIMEOUT_S
    max_out = limits.max_output_tokens if limits else None
    model = build_chat_model(agent.model, max_output_tokens=max_out)
    tools = apply_tool_call_cap(
        tools,
        limits.max_tool_calls if limits else None,
        account_id,
        account_type,
    )

    with observability.span(
        "agent.task",
        {
            "agent.id": agent.agent_id,
            "agent.name": agent.name,
            "task.depth": depth,
            "prompt.source": prompt_source,
            "prompt.version": prompt_version,
        },
    ):
        graph = create_agent(model, tools, system_prompt=system_prompt)
        async with asyncio.timeout(timeout_s):
            result = await graph.ainvoke(
                {"messages": [("user", message)]},
                {"recursion_limit": max_turns * 2 + 1},
            )
            if response_format:
                # Extract the structure separately once the tool loop is done:
                # native support first, prompt-based degradation otherwise.
                struct_model = build_chat_model(agent.model, streaming=False, max_output_tokens=max_out)
                obj, validation_path = await structured_extract_with_path(
                    struct_model, result["messages"], response_format, system_prompt
                )
                output = json.dumps(obj, ensure_ascii=False)
                await _emit_task_evidence(
                    agent=agent,
                    task_id=task_id,
                    prompt_source=prompt_source,
                    prompt_version=prompt_version,
                    account_id=account_id,
                    account_type=account_type,
                    output=obj,
                    claim_type="structured_output",
                    response_format=response_format,
                    structured_validation_path=validation_path,
                    result_messages=result["messages"],
                )
                return output
    for msg in reversed(result["messages"]):
        if isinstance(msg, AIMessage) and msg.content:
            output = msg.content if isinstance(msg.content, str) else str(msg.content)
            await _emit_task_evidence(
                agent=agent,
                task_id=task_id,
                prompt_source=prompt_source,
                prompt_version=prompt_version,
                account_id=account_id,
                account_type=account_type,
                output=output,
                claim_type="answer",
                response_format=None,
                structured_validation_path=None,
                result_messages=result["messages"],
            )
            return output
    raise RuntimeError("the graph finished without producing an AI reply")


async def execute_task(task_id: str, agent: AgentOut, req_input: dict, account_id: str, account_type: str) -> None:
    """Run to a terminal state and persist it, where succeeded must mean the
    result is actually usable. /run executes this in the background while
    /invoke waits for it synchronously."""
    async with SessionLocal() as session:
        await dao.set_task_status(session, task_id, "running")
    try:
        output = await run_agent_once(
            agent,
            req_input["message"],
            req_input.get("prompt_vars") or {},
            req_input.get("skills") or [],
            req_input.get("prompt_override"),
            account_id,
            account_type,
            depth=1,
            response_format=req_input.get("response_format"),
            task_id=task_id,
        )
        async with SessionLocal() as session:
            # succeeded must mean the result is usable (the vega build-task lesson)
            await dao.set_task_status(session, task_id, "succeeded", output=output)
    except Exception as e:  # A failure must persist failure_detail; never swallow the error
        detail = getattr(e, "detail", None)
        detail_text = str(detail) if detail else f"{type(e).__name__}: {e}"
        async with SessionLocal() as session:
            await dao.set_task_status(session, task_id, "failed", failure_detail=detail_text)


def submit_task(task_id: str, agent: AgentOut, req_input: dict, account_id: str, account_type: str) -> None:
    task = asyncio.create_task(execute_task(task_id, agent, req_input, account_id, account_type))
    _background.add(task)
    task.add_done_callback(_background.discard)


async def _emit_task_evidence(
    *,
    agent: AgentOut,
    task_id: str | None,
    prompt_source: str,
    prompt_version: str,
    account_id: str,
    account_type: str,
    output,
    claim_type: str,
    response_format: dict[str, Any] | None,
    structured_validation_path: str | None,
    result_messages: list[Any],
) -> None:
    subject_id = task_id or agent.agent_id
    cid = evidence.claim_id(claim_type, subject_id, output)
    source_event_ids, operation_ids, evidence_refs, business_refs = evidence.adopted_sources()
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
        operation_name="bkn.agent.task",
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
                operation_name="bkn.agent.task",
                operation_id=operation_ids[-1] if operation_ids else None,
                causation_event_id=claim_event["event_id"] if claim_event else None,
            )
        )
    events.append(
        evidence.business_refs_resolved(
            claim_id_value=cid,
            business_refs=business_refs,
            operation_name="bkn.agent.task",
            operation_id=operation_ids[-1],
            causation_event_id=claim_event["event_id"],
        )
    )
    await evidence.submit_events([event for event in events if event], account_id, account_type)
