import logging
from typing import Any

import aiohttp
from langchain_core.tools import StructuredTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from app.config import config
from app.core.toolbox import load_toolbox_tools
from app.errors import bad_request

logger = logging.getLogger("bkn-agent.tools")


def _mcp_connections(tool_refs: list[dict], account_id: str, account_type: str) -> dict[str, dict]:
    """agent.tools 中 type=mcp 的显式外部 MCP 端点。平台内置工具不走这里
    （统一从执行工厂 toolbox 装载，见 load_tools）。"""
    headers = {"x-account-id": account_id, "x-account-type": account_type}
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
    if not sub_agent:
        raise bad_request("ToolRef", "agent 工具引用不存在", f"agent {agent_id} 不存在")

    async def call_sub_agent(message: str) -> str:
        """调用子 agent 完成一次性任务，返回其最终回复。"""
        async with SessionLocal() as session:
            task = await dao.create_task(
                session, sub_agent.agent_id, {"message": message}, account_id, parent_thread_id
            )
            await dao.set_task_status(session, task.task_id, "running")
        try:
            output = await runner.run_agent_once(
                sub_agent, message, {}, [], None, account_id, account_type, depth=depth + 1
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
    async def read_skill_file(capability_id: str, path: str) -> str:
        """读取已挂载技能的附属文件（渐进式加载，大文件不常驻上下文）。"""
        url = f"{config.CAPABILITIES_LAB_BASE}/capabilities/{capability_id}/skill/files/read"
        headers = {"x-account-id": account_id, "x-account-type": account_type}
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as session:
            async with session.post(url, json={"path": path}, headers=headers) as resp:
                if resp.status != 200:
                    return f"read_skill_file failed: HTTP {resp.status}"
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
        client = MultiServerMCPClient(conns)
        tools.extend(await client.get_tools())
    for ref in tool_refs:
        if ref.get("type") == "agent":
            tools.append(await _agent_tool(ref, account_id, account_type, depth, parent_thread_id))
    tools.append(_read_skill_file_tool(account_id, account_type))
    # 名字冲突去重（保留先到：显式 toolbox > 默认 box > mcp > agent）
    seen: set[str] = set()
    deduped: list[Any] = []
    for t in tools:
        name = getattr(t, "name", None)
        if name in seen:
            logger.warning("[Tools] duplicate tool name %s dropped", name)
            continue
        if name:
            seen.add(name)
        deduped.append(t)
    return deduped
