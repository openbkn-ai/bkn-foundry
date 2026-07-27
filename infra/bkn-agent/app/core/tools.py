import logging
from typing import Any

import aiohttp
from langchain_core.tools import StructuredTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from app import evidence, observability
from app.config import config
from app.core.skills import normalize_skill_id
from app.core.toolbox import load_toolbox_tools
from app.errors import bad_request

logger = logging.getLogger("bkn-agent.tools")


async def _trace_mcp_call(request, handler):
    """At the transport boundary, bind the MCP call to the current tool operation."""
    headers = {**(request.headers or {}), **observability.outbound_headers()}
    return await handler(request.override(headers=headers))


def _mcp_connections(tool_refs: list[dict], account_id: str, account_type: str) -> dict[str, dict]:
    """agent.tools 中 type=mcp 的显式外部 MCP 端点。平台内置工具不走这里
    （统一从执行工厂 toolbox 装载，见 load_tools）。"""
    headers = {"x-account-id": account_id, "x-account-type": account_type, **observability.outbound_headers()}
    conns: dict[str, dict] = {}
    for i, ref in enumerate(tool_refs):
        kind = ref.get("type")
        if kind == "mcp":
            url = ref.get("url")
            if not url:
                raise bad_request("ToolRef", "mcp 工具缺 url", str(ref))
            conns[ref.get("name") or f"mcp-{i}"] = {
                "transport": "streamable_http",
                "url": url,
                "headers": headers,
            }
        elif kind in ("agent", "toolbox"):
            continue  # agent-as-tool 见 _agent_tool；toolbox 见 _toolbox_tools
        else:
            raise bad_request("ToolRef", "未知工具类型", str(ref))
    return conns


async def _toolbox_tools(
    tool_refs: list[dict], account_id: str, account_type: str
) -> list[StructuredTool]:
    """执行工厂 toolbox 装载：默认 box（可配，拉不到降级告警不击穿对话）
    + agent.tools 中 type=toolbox 的显式引用（失败报错）。"""
    box_ids: list[str] = []
    for ref in tool_refs:
        if ref.get("type") == "toolbox":
            box_id = ref.get("box_id")
            if not box_id:
                raise bad_request("ToolRef", "toolbox 工具缺 box_id", str(ref))
            box_ids.append(box_id)
    tools: list[StructuredTool] = []
    for box_id in dict.fromkeys(box_ids):
        tools.extend(await load_toolbox_tools(box_id, account_id, account_type))
    for box_id in config.default_toolboxes:
        if box_id in box_ids:
            continue
        try:
            tools.extend(await load_toolbox_tools(box_id, account_id, account_type))
        except Exception as e:
            logger.warning("[Toolbox] default box %s unavailable, degraded: %s", box_id, e)
    return tools


async def _agent_tool(
    ref: dict, account_id: str, account_type: str, depth: int, parent_thread_id: str | None
) -> StructuredTool:
    """把 mode=task 的 agent 包装成工具（agent-as-tool）。执行与 /run 同路径，
    task 落库带 parent_thread_id，保证子任务同样可监控。"""
    from app import dao
    from app.core import runner
    from app.db import SessionLocal

    agent_id = ref.get("agent_id")
    if not agent_id:
        raise bad_request("ToolRef", "agent 工具缺 agent_id", str(ref))
    async with SessionLocal() as session:
        sub_agent = await dao.get_agent(session, agent_id)
    # 与 /run（mode=task）、/invoke（published）同门：否则 draft/chat 型 agent 经
    # 工具引用就能被无状态执行，绕过 API 其余入口一致的语义
    if not sub_agent or sub_agent.status != "published":
        raise bad_request(
            "ToolRef", "agent 工具引用不可用", f"agent {agent_id} 不存在或未发布", "先发布被引用的 agent。"
        )
    if sub_agent.mode != "task":
        raise bad_request(
            "ToolRef",
            "agent 工具只能引用一次性 agent",
            f"agent {agent_id} mode={sub_agent.mode}",
            "agent-as-tool 走 /run 同款一次性执行路径，被引用方须 mode=task。",
        )

    async def call_sub_agent(message: str) -> str:
        """调用子 agent 完成一次性任务，返回其最终回复。"""
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

    name = ref.get("name") or f"agent_{sub_agent.name}"
    description = ref.get("description") or f"调用子 agent「{sub_agent.name}」完成一次性任务。"
    return StructuredTool.from_function(coroutine=call_sub_agent, name=name, description=description)


def _read_skill_file_tool(account_id: str, account_type: str) -> StructuredTool:
    async def read_skill_file(skill_id: str, path: str) -> str:
        """读取已挂载技能的附属文件（渐进式加载，大文件不常驻上下文）。
        skill_id 与 path 取 system prompt 中该技能条目列出的值。"""
        sid = normalize_skill_id(skill_id)
        url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/skills/{sid}/files/read"
        headers = {"x-account-id": account_id, "x-account-type": account_type, **observability.outbound_headers()}
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as session:
            # 字段名是 rel_path，执行工厂侧 validate:"required"；发过 path 会 400
            async with session.post(url, json={"rel_path": path}, headers=headers) as resp:
                if resp.status != 200:
                    detail = (await resp.text())[:200]
                    return f"read_skill_file failed: HTTP {resp.status} {detail}"
                meta = await resp.json()
            # 与 load_skills 同样两跳：发布态只回 presigned URL，不回正文
            async with session.get(meta["url"]) as resp:
                if resp.status != 200:
                    return f"read_skill_file failed: 对象存储 HTTP {resp.status}"
                return await resp.text()

    return StructuredTool.from_function(coroutine=read_skill_file, name="read_skill_file")


async def load_tools(
    tool_refs: list[dict],
    account_id: str,
    account_type: str,
    depth: int = 0,
    parent_thread_id: str | None = None,
) -> list[Any]:
    tools: list[Any] = await _toolbox_tools(tool_refs, account_id, account_type)
    conns = _mcp_connections(tool_refs, account_id, account_type)
    if conns:
        client = MultiServerMCPClient(conns, tool_interceptors=[_trace_mcp_call])
        tools.extend(await client.get_tools())
    for ref in tool_refs:
        if ref.get("type") == "agent":
            tools.append(await _agent_tool(ref, account_id, account_type, depth, parent_thread_id))

    # 名字冲突去重（保留先到：显式 toolbox > 默认 box > mcp > agent）。
    # 内置 read_skill_file 预占名字：它是技能加载的一等能力（设计不变量），
    # 不能被同名的用户工具挤掉——反过来挤掉那个用户工具并告警。
    builtin = _read_skill_file_tool(account_id, account_type)
    seen: set[str] = {builtin.name}
    deduped: list[Any] = []
    for t in tools:
        name = getattr(t, "name", None)
        if name in seen:
            logger.warning("[Tools] duplicate tool name %s dropped", name)
            continue
        if name:
            seen.add(name)
        deduped.append(t)
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

        async def _traced(__inner=inner, __tool_name=tool_name, __tool_id=tool_id, **kwargs):
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
                body=result if isinstance(result, dict) else None,
                context_hash=evidence.tool_message_context_hash(result),
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
    """执行 AgentLimits.max_tool_calls：整轮工具调用次数用尽后，工具改为返回
    提示串（模型据此收敛作答），而非静默无视上限。None = 不限。"""
    if max_tool_calls is None:
        return tools
    budget = {"left": max(max_tool_calls, 0), "exhausted_emitted": False}
    capped: list[Any] = []
    for t in tools:
        inner = getattr(t, "coroutine", None)
        if inner is None:  # 同步工具（当前不产生）保持原样
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
                # 措辞要斩钉截铁：只说「请直接作答」时模型会继续试探性重试，
                # 白烧若干轮（VM 实测空转 9 轮才收敛）
                return (
                    f"tool call budget exhausted: 已用完本次执行的工具调用配额"
                    f"（max_tool_calls={max_tool_calls}）。禁止再调用任何工具——"
                    f"任何后续工具调用都会被拒绝。请立即用已获取的信息给出最终答案。"
                )
            budget["left"] -= 1
            return await __inner(**kwargs)

        capped.append(t.model_copy(update={"coroutine": _guarded}))
    return capped
