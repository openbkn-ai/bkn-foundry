from typing import Any

import aiohttp
from langchain_core.tools import StructuredTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from app.config import config
from app.errors import bad_request


def _mcp_connections(tool_refs: list[dict], account_id: str, account_type: str) -> dict[str, dict]:
    """agent.tools 中 type=mcp 的条目 + 默认 agent-retrieval 内置工具集。
    身份经 header 透传（/in 约定，授权押下游）。"""
    headers = {"x-account-id": account_id, "x-account-type": account_type}
    conns: dict[str, dict] = {
        "agent-retrieval": {
            "transport": "streamable_http",
            "url": config.AGENT_RETRIEVAL_MCP_URL,
            "headers": headers,
        }
    }
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
        elif kind == "agent":
            # agent-as-tool，M3（#208）实现
            raise bad_request(
                "ToolRef.NotYet", "agent-as-tool 尚未支持", str(ref), "随 M3（issue #208）落地。"
            )
        elif kind == "toolbox":
            # 算子工厂 toolbox 直挂，M7（#212）联调
            raise bad_request(
                "ToolRef.NotYet", "toolbox 引用尚未支持", str(ref), "M2 请以 MCP 端点形式挂载。"
            )
        else:
            raise bad_request("ToolRef", "未知工具类型", str(ref))
    return conns


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


async def load_tools(tool_refs: list[dict], account_id: str, account_type: str) -> list[Any]:
    client = MultiServerMCPClient(_mcp_connections(tool_refs, account_id, account_type))
    tools = await client.get_tools()
    tools.append(_read_skill_file_tool(account_id, account_type))
    return tools
