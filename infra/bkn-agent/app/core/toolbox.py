"""执行工厂 toolbox 工具装载。

工具面收敛到执行工厂（operator-integration）：contextloader 内置工具集、沙箱、
联网搜索、published agent 等统一从 toolbox 读取元数据（OpenAPI），执行统一走
执行代理 POST /internal-v1/tool-box/{box_id}/proxy/{tool_id}。bkn-agent 不与
agent-retrieval 保持专用 MCP 通道；外部 MCP 端点仍可经 ToolRef type=mcp 显式挂载。

身份按 /in 约定透传：请求头 x-account-id / x-account-type（operator-integration
内部树会校验账户存在并映射 user_id），执行代理转发的 header 里同样带上，下游
（如 agent-retrieval /in 路由）按真实调用者授权。
"""

import json
import logging
import re
from typing import Any, Optional

import aiohttp
from langchain_core.tools import StructuredTool
from pydantic import Field, create_model

from app.config import config

logger = logging.getLogger("bkn-agent.toolbox")

_TYPE_MAP = {
    "string": str,
    "integer": int,
    "number": float,
    "boolean": bool,
    "object": dict,
    "array": list,
}

# LLM 工具名约束（OpenAI function name 规则）
_NAME_RE = re.compile(r"[^a-zA-Z0-9_-]")


def _safe_name(name: str, tool_id: str) -> str:
    cleaned = _NAME_RE.sub("_", name or "")[:64]
    if not re.search(r"[a-zA-Z0-9]", cleaned):
        cleaned = f"tool_{tool_id[:8]}"
    return cleaned


def _resolve_ref(schema: dict, api_spec: dict) -> dict:
    """单层解析 #/components/schemas/X 引用（.adp/impex 惯用形态）。"""
    ref = schema.get("$ref") or ""
    if ref.startswith("#/components/schemas/"):
        name = ref.rsplit("/", 1)[-1]
        return ((api_spec.get("components") or {}).get("schemas") or {}).get(name) or {}
    return schema


def _args_model(tool_name: str, metadata: dict):
    """从工具元数据挖请求体 schema → pydantic 动态模型。impex 的 api_spec 是
    扁平结构（request_body/responses 数组），非 OpenAPI paths 树。
    无参数工具返回空模型（LLM 侧零参调用）。"""
    api_spec = metadata.get("api_spec") or {}
    body = api_spec.get("request_body") or {}
    schema = ((body.get("content") or {}).get("application/json") or {}).get("schema") or {}
    schema = _resolve_ref(schema, api_spec)
    props = schema.get("properties") or {}
    required = set(schema.get("required") or [])
    fields: dict[str, Any] = {}
    for pname, p in props.items():
        p = p or {}
        typ = _TYPE_MAP.get(p.get("type"), Any)
        desc = p.get("description") or ""
        if pname in required:
            fields[pname] = (typ, Field(description=desc))
        else:
            fields[pname] = (Optional[typ], Field(default=None, description=desc))
    return create_model(f"{_safe_name(tool_name, 'x')}_args", **fields)


async def _execute(
    box_id: str, tool_id: str, method: str, args: dict, account_id: str, account_type: str
) -> str:
    """经执行代理调用工具。工具级失败以字符串返回给 LLM（可自我修正），
    不抛异常击穿整轮对话。"""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/proxy/{tool_id}"
    identity = {"x-account-id": account_id, "x-account-type": account_type}
    clean = {k: v for k, v in args.items() if v is not None}
    payload: dict[str, Any] = {"timeout": 60, "header": identity, "body": {}, "query": {}, "path": {}}
    if method.upper() in ("GET", "DELETE"):
        payload["query"] = clean
    else:
        payload["body"] = clean
    try:
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=90)) as http:
            async with http.post(url, json=payload, headers=identity) as resp:
                text = await resp.text()
                if not 200 <= resp.status < 300:
                    return f"tool call failed: HTTP {resp.status} {text[:500]}"
    except Exception as e:
        return f"tool call failed: {e}"
    try:
        data = json.loads(text)
    except ValueError:
        return text
    if data.get("error"):
        return f"tool error: {data['error']}"
    status_code = data.get("status_code") or 0
    body = data.get("body")
    body_text = body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)
    if status_code >= 400:
        return f"tool target failed: HTTP {status_code} {body_text[:800]}"
    return body_text


def _build_tool(box_id: str, info: dict, account_id: str, account_type: str) -> StructuredTool | None:
    if info.get("status") != "enabled":
        return None
    if info.get("metadata_type") != "openapi":
        logger.info("[Toolbox] skip non-openapi tool %s (%s)", info.get("name"), info.get("metadata_type"))
        return None
    metadata = info.get("metadata") or {}
    method = metadata.get("method") or "POST"
    tool_id = info["tool_id"]
    name = _safe_name(info.get("name") or "", tool_id)
    description = info.get("description") or metadata.get("summary") or name

    async def call(**kwargs) -> str:
        return await _execute(box_id, tool_id, method, kwargs, account_id, account_type)

    return StructuredTool.from_function(
        coroutine=call,
        name=name,
        description=description,
        args_schema=_args_model(name, metadata),
    )


async def _list_tools(box_id: str, account_id: str, account_type: str) -> list[dict]:
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/tools/list"
    headers = {"x-account-id": account_id, "x-account-type": account_type}
    infos: list[dict] = []
    page = 1
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=15)) as http:
        while True:
            params = {"page": page, "page_size": 100, "all": "true"}
            async with http.get(url, params=params, headers=headers) as resp:
                if resp.status != 200:
                    raise RuntimeError(f"toolbox {box_id} list failed: HTTP {resp.status} {(await resp.text())[:300]}")
                data = await resp.json()
            infos.extend(data.get("tools") or [])
            if not data.get("has_next"):
                return infos
            page += 1


async def load_toolbox_tools(box_id: str, account_id: str, account_type: str) -> list[StructuredTool]:
    """装载一个 toolbox 的全部 enabled 工具。列表拉取失败抛异常，由调用方
    决定降级（默认工具集）或报错（显式引用）。"""
    infos = await _list_tools(box_id, account_id, account_type)
    tools = []
    for info in infos:
        tool = _build_tool(box_id, info, account_id, account_type)
        if tool:
            tools.append(tool)
    return tools
