import logging
from typing import Any

import aiohttp
from langchain_core.tools import StructuredTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from app import evidence, observability
from app.commons import locale
from app.config import config
from app.core import context_loader
from app.core.skills import normalize_skill_id
from app.core.toolbox import _safe_name, load_toolbox_tools
from app.errors import bad_request

logger = logging.getLogger("bkn-agent.tools")


def _mcp_result_body_and_content(result: Any) -> tuple[dict[str, Any] | None, Any]:
    """Extract MCP structured content while preserving the model-visible content."""
    if isinstance(result, dict):
        return result, result
    if not isinstance(result, tuple) or len(result) != 2:
        return None, result
    content, artifact = result
    if not isinstance(artifact, dict):
        return None, content
    for key in ("structured_content", "structuredContent"):
        structured = artifact.get(key)
        if isinstance(structured, dict):
            return structured, content
    return None, content


def _context_loader_allowed_tools(tool_refs: list[dict]) -> set[str] | None:
    """Merge the allowlists of the Context Loader references.

    An older agent that declares no allowed_tools keeps loading everything; when
    every reference declares one, the union applies. A new read-only agent can
    therefore narrow precisely without changing how existing agents behave.
    """
    allow_list: set[str] = set()
    for ref in tool_refs:
        if ref.get("type") != "context_loader":
            continue
        allowed = ref.get("allowed_tools")
        if allowed is None:
            return None
        allow_list.update(str(name) for name in allowed)
    return allow_list


async def _trace_mcp_call(request, handler):
    """At the transport boundary, bind the MCP call to the current tool operation."""
    headers = locale.internal_request_headers(
        {**(request.headers or {}), **observability.outbound_headers()}
    )
    return await handler(request.override(headers=headers))


def _mcp_connections(tool_refs: list[dict], account_id: str, account_type: str) -> dict[str, dict]:
    """Explicit external MCP endpoints, the type=mcp entries of agent.tools.
    Built-in platform tools do not come through here; they all load from an
    execution-factory toolbox, see load_tools."""
    headers = locale.internal_request_headers(
        {"x-account-id": account_id, "x-account-type": account_type, **observability.outbound_headers()}
    )
    conns: dict[str, dict] = {}
    for i, ref in enumerate(tool_refs):
        kind = ref.get("type")
        if kind == "mcp":
            url = ref.get("url")
            if not url:
                raise bad_request("BknAgent.ToolRef.McpUrlMissing", ref=str(ref))
            conns[ref.get("name") or f"mcp-{i}"] = {
                "transport": "streamable_http",
                "url": url,
                "headers": headers,
            }
        elif kind in ("agent", "toolbox", "context_loader"):
            # agent-as-tool: see _agent_tool. toolbox: see _toolbox_tools.
            # context_loader: see app/core/context_loader.py, whose endpoint
            # comes from configuration and needs a handshake first.
            continue
        else:
            raise bad_request("BknAgent.ToolRef.UnknownType", ref=str(ref))
    return conns


async def _toolbox_tools(
    tool_refs: list[dict], account_id: str, account_type: str
) -> list[StructuredTool]:
    """Load execution-factory toolboxes: only the explicit type=toolbox entries
    of agent.tools, and a failure is an error.

    There is no implicitly mounted default box. agent.tools is the complete tool
    set, so declaring nothing means having no tools. "Load it when it is needed"
    cannot work mechanically: tool calling requires every candidate tool to be
    declared before the request goes out and the model may only choose from that
    list, so narrowing has to happen at definition time, before loading.
    """
    box_ids: list[str] = []
    for ref in tool_refs:
        if ref.get("type") == "toolbox":
            box_id = ref.get("box_id")
            if not box_id:
                raise bad_request("BknAgent.ToolRef.BoxIdMissing", ref=str(ref))
            box_ids.append(box_id)
    tools: list[StructuredTool] = []
    for box_id in dict.fromkeys(box_ids):
        tools.extend(await load_toolbox_tools(box_id, account_id, account_type))
    return tools


async def _agent_tool(
    ref: dict, account_id: str, account_type: str, depth: int, parent_thread_id: str | None
) -> StructuredTool:
    """Wrap a mode=task agent as a tool (agent-as-tool). Execution follows the
    same path as /run, and the persisted task carries parent_thread_id so a
    sub-task stays just as observable."""
    from app import dao
    from app.core import runner
    from app.db import SessionLocal

    agent_id = ref.get("agent_id")
    if not agent_id:
        raise bad_request("BknAgent.ToolRef.AgentIdMissing", ref=str(ref))
    async with SessionLocal() as session:
        sub_agent = await dao.get_agent(session, agent_id)
    # Same door as /run (mode=task) and /invoke (published): otherwise a draft
    # or chat agent could be executed statelessly through a tool reference,
    # bypassing the semantics every other API entry point enforces.
    if not sub_agent or sub_agent.status != "published":
        raise bad_request("BknAgent.ToolRef.AgentUnavailable", agent_id=agent_id)
    if sub_agent.mode != "task":
        raise bad_request("BknAgent.ToolRef.AgentNotTask", agent_id=agent_id, mode=sub_agent.mode)

    async def call_sub_agent(message: str) -> str:
        """Call a child agent for a one-shot task and return its final response."""
        async with SessionLocal() as session:
            task = await dao.create_task(
                session, sub_agent.agent_id, {"message": message}, account_id, parent_thread_id
            )
            await dao.set_task_status(session, task.task_id, "running")
        event = evidence.agent_as_tool_invoked(
            parent_thread_id=parent_thread_id,
            child_task_id=task.task_id,
            child_agent_id=sub_agent.agent_id,
            depth=depth + 1,
            message_hash=evidence.hash_value(message),
            operation_name="bkn.agent.agent_as_tool",
        )
        await evidence.submit_events([event] if event else [], account_id, account_type)
        try:
            output = await runner.run_agent_once(
                sub_agent,
                message,
                {},
                [],
                None,
                account_id,
                account_type,
                depth=depth + 1,
                task_id=task.task_id,
            )
        except Exception as e:
            async with SessionLocal() as session:
                await dao.set_task_status(session, task.task_id, "failed", failure_detail=str(e))
            raise
        async with SessionLocal() as session:
            await dao.set_task_status(session, task.task_id, "succeeded", output=output)
        return output

    name = _derive_agent_tool_name(ref, sub_agent.name, sub_agent.agent_id)
    description = (
        ref.get("description")
        or f"Call the child agent '{sub_agent.name}' for a one-shot task."
    )
    return StructuredTool.from_function(coroutine=call_sub_agent, name=name, description=description)


def _derive_agent_tool_name(ref: dict, agent_name: str, agent_id: str) -> str:
    """OpenAI-legal tool name for an agent-as-tool.

    AgentSpec.name allows Chinese and up to 100 chars, but an OpenAI function
    name must be ASCII [a-zA-Z0-9_-] and <=64 — so a raw `agent_{name}` like
    `agent_语义理解` makes the model 400 on every call before the tool even
    runs, and agent-as-tool is a core capability with a wide blast radius. Run
    the explicit (AgentToolRef.name) or derived name through the same sanitizer
    the toolbox tools use: it strips illegal chars, truncates to 64, and falls
    back to `tool_{agent_id prefix}` when nothing usable survives. Semantics are
    carried by the description instead.
    """
    raw_name = ref.get("name") or f"agent_{agent_name}"
    return _safe_name(raw_name, agent_id)


def _read_skill_file_tool(account_id: str, account_type: str) -> StructuredTool:
    async def read_skill_file(skill_id: str, path: str) -> str:
        """Read an attached file from a mounted skill using progressive loading.

        Use a skill_id and path listed for that skill in the system prompt.
        """
        sid = normalize_skill_id(skill_id)
        url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/skills/{sid}/files/read"
        headers = {"x-account-id": account_id, "x-account-type": account_type, **observability.outbound_headers()}
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as session:
            # The field is rel_path, marked validate:"required" on the execution factory
            # side; sending path instead answers 400.
            async with session.post(url, json={"rel_path": path}, headers=headers) as resp:
                if resp.status != 200:
                    detail = (await resp.text())[:200]
                    return f"read_skill_file failed: HTTP {resp.status} {detail}"
                meta = await resp.json()
            # Two hops, as in load_skills: the published surface returns a presigned URL, not the body
            async with session.get(meta["url"]) as resp:
                if resp.status != 200:
                    return f"read_skill_file failed: object storage HTTP {resp.status}"
                return await resp.text()

    return StructuredTool.from_function(coroutine=read_skill_file, name="read_skill_file")


async def load_tools(
    tool_refs: list[dict],
    account_id: str,
    account_type: str,
    depth: int = 0,
    parent_thread_id: str | None = None,
    skill_ids: list[str] | None = None,
) -> list[Any]:
    tools: list[Any] = await _toolbox_tools(tool_refs, account_id, account_type)
    if context_loader.wanted(tool_refs):
        # Only use the session the caller (runner or graph) already opened; never
        # open one here.
        #
        # This used to read `current_session() or await open_session()`, meant to
        # cover callers that invoke load_tools directly. In practice that leaked:
        # when the main handshake failed it opened a second interaction, while
        # the finally block in graph closes only the cl_session it holds (None at
        # that point), so the fallback interaction could never be closed. The
        # server allows one active interaction per conversation, so every turn
        # leaked one and the next turn was blocked by interaction_in_progress —
        # from its second turn onward a thread could never obtain tools again.
        #
        # No session means no tools. open_session already logged a warning, and
        # leaving the failure where it can be seen beats silently opening an
        # interaction nobody can close.
        session = context_loader.current_session()
        if session is not None:
            tools.extend(session.tools(_context_loader_allowed_tools(tool_refs)))
    conns = _mcp_connections(tool_refs, account_id, account_type)
    if conns:
        client = MultiServerMCPClient(conns, tool_interceptors=[_trace_mcp_call])
        tools.extend(await client.get_tools())
    for ref in tool_refs:
        if ref.get("type") == "agent":
            tools.append(await _agent_tool(ref, account_id, account_type, depth, parent_thread_id))

    # The built-in read_skill_file never grows a tools node on its own: it either
    # rides along with existing tools or mounts once skills are declared, which
    # are the only thing it can read. If an agent with no tools and no skills
    # grew a tools node just for it, the model could still burn a turn on an
    # empty tool call — exactly the shape of #447.
    mount_skill_reader = bool(tools) or bool(skill_ids)

    # Deduplicate name collisions, first one wins: toolbox > mcp > agent.
    # When mounted, read_skill_file claims its name up front: it is a first-class
    # part of skill loading and a design invariant, so a user tool of the same
    # name must not displace it — the user tool is dropped instead, with a
    # warning.
    builtin = _read_skill_file_tool(account_id, account_type) if mount_skill_reader else None
    seen: set[str] = {builtin.name} if builtin else set()
    deduped: list[Any] = []
    for t in tools:
        name = getattr(t, "name", None)
        if name in seen:
            logger.warning("[Tools] duplicate tool name %s dropped", name)
            continue
        if name:
            seen.add(name)
        deduped.append(t)
    if builtin:
        deduped.append(builtin)
    return deduped


def instrument_tool_calls(tools: list[Any], account_id: str, account_type: str) -> list[Any]:
    """Give every non-native LangChain tool call its own causal operation.

    Toolbox tools already emit at the HTTP boundary and carry a native marker,
    so wrapping them again would create duplicate business events.
    """
    instrumented: list[Any] = []
    for tool in tools:
        metadata = dict(getattr(tool, "metadata", None) or {})
        inner = getattr(tool, "coroutine", None)
        if inner is None or metadata.get("bkn_trace_native"):
            instrumented.append(tool)
            continue
        tool_name = str(getattr(tool, "name", "tool"))
        tool_id = str(metadata.get("bkn_tool_id") or tool_name)

        async def _traced(
            __inner=inner,
            __tool_name=tool_name,
            __tool_id=tool_id,
            __trust_mcp_receipt=metadata.get("bkn_context_loader") is True,
            **kwargs,
        ):
            operation_id, parent_event_id = evidence.new_operation()
            called = evidence.tool_called(
                tool_id=__tool_id,
                tool_name=__tool_name,
                toolbox_id=None,
                args_hash=evidence.hash_value(kwargs),
                operation_name="bkn.agent.tool.call",
                operation_id=operation_id,
                causation_event_id=parent_event_id,
            )
            headers = evidence.operation_headers(operation_id, called["event_id"] if called else parent_event_id or "")
            header_token = observability.set_operation_headers(headers)
            try:
                result = await __inner(**kwargs)
            except Exception as exc:
                observed = evidence.tool_result_observed(
                    tool_id=__tool_id,
                    tool_name=__tool_name,
                    toolbox_id=None,
                    result_hash=None,
                    result_length=None,
                    result_count=None,
                    success=False,
                    operation_name="bkn.agent.tool.call",
                    error_hash=evidence.hash_value(f"{type(exc).__name__}:{exc}"),
                    operation_id=operation_id,
                    causation_event_id=called["event_id"] if called else parent_event_id or "",
                )
                await evidence.submit_events([event for event in (called, observed) if event], account_id, account_type)
                raise
            finally:
                observability.reset_operation_headers(header_token)
            result_body, result_content = _mcp_result_body_and_content(result)
            result_text = result if isinstance(result, str) else str(result)
            observed = evidence.tool_result_observed(
                tool_id=__tool_id,
                tool_name=__tool_name,
                toolbox_id=None,
                result_hash=evidence.hash_value(result),
                result_length=len(result_text),
                result_count=evidence.result_count(result),
                success=True,
                operation_name="bkn.agent.tool.call",
                operation_id=operation_id,
                causation_event_id=called["event_id"] if called else parent_event_id or "",
            )
            evidence.record_fact_receipt(
                operation_id=operation_id,
                body=result_body,
                context_hash=evidence.tool_message_context_hash(result_content),
                trust_mcp_receipt=__trust_mcp_receipt,
            )
            await evidence.submit_events([event for event in (called, observed) if event], account_id, account_type)
            return result

        instrumented.append(tool.model_copy(update={"coroutine": _traced}))
    return instrumented


def apply_tool_call_cap(
    tools: list[Any],
    max_tool_calls: int | None,
    account_id: str = "",
    account_type: str = "",
) -> list[Any]:
    """Enforce AgentLimits.max_tool_calls. Once the turn's tool-call budget is
    spent, the tools return a notice string the model can converge on, rather
    than the limit being silently ignored. None means unlimited."""
    if max_tool_calls is None:
        return tools
    budget = {"left": max(max_tool_calls, 0), "exhausted_emitted": False}
    capped: list[Any] = []
    for t in tools:
        inner = getattr(t, "coroutine", None)
        if inner is None:  # A synchronous tool (none are produced today) is left as it is
            capped.append(t)
            continue

        tool_name = getattr(t, "name", None)

        async def _guarded(__inner=inner, __tool_name=tool_name, **kwargs) -> str:
            if budget["left"] <= 0:
                if not budget["exhausted_emitted"]:
                    budget["exhausted_emitted"] = True
                    event = evidence.tool_budget_exhausted(
                        max_tool_calls=max_tool_calls,
                        operation_name="bkn.agent.tool.call",
                        tool_name=__tool_name,
                    )
                    await evidence.submit_events([event] if event else [], account_id, account_type)
                # The wording has to be categorical: with a mere "just answer"
                # the model keeps probing with retries and burns turns (observed
                # on the VM: nine empty turns before it converged).
                # This steers how the model should answer next, so it follows
                # the system-prompt boundary rather than the tool-description
                # one above; see the note in core/skills.py and #826.
                return (
                    f"tool call budget exhausted: 已用完本次执行的工具调用配额"
                    f"（max_tool_calls={max_tool_calls}）。禁止再调用任何工具——"
                    f"任何后续工具调用都会被拒绝。请立即用已获取的信息给出最终答案。"
                )
            budget["left"] -= 1
            return await __inner(**kwargs)

        capped.append(t.model_copy(update={"coroutine": _guarded}))
    return capped
