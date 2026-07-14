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
import keyword
import logging
import re
from dataclasses import dataclass
from typing import Any, Optional

import aiohttp
from fastapi import HTTPException
from langchain_core.tools import StructuredTool
from pydantic import ConfigDict, Field, create_model

from app.config import config
from app.errors import bad_request, err

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
# 身份头由 runtime 注入（/in 约定），不交给 LLM 决策
_IDENTITY_HEADERS = {"x-account-id", "x-account-type", "user_id"}


@dataclass(frozen=True)
class _Param:
    """一个 LLM 可见参数：field=模型字段名（python 合法），wire=下游真实参数名，
    location=body|query|path（决定执行代理 payload 落哪个桶）。"""

    field: str
    wire: str
    location: str


def _safe_name(name: str, tool_id: str) -> str:
    cleaned = _NAME_RE.sub("_", name or "")[:64]
    if not re.search(r"[a-zA-Z0-9]", cleaned):
        cleaned = f"tool_{tool_id[:8]}"
    return cleaned


def _safe_field(name: str, taken: set[str]) -> str:
    """参数名 → python 合法且非保留的字段名（pydantic create_model 要求）。"""
    field = re.sub(r"[^0-9a-zA-Z_]", "_", name or "")
    if not field or field[0].isdigit():
        field = f"p_{field}"
    if keyword.iskeyword(field):
        field = f"{field}_"
    base, i = field, 2
    while field in taken:
        field = f"{base}_{i}"
        i += 1
    return field


def _resolve_ref(schema: dict, api_spec: dict) -> dict:
    """单层解析 #/components/schemas/X 引用（.adp/impex 惯用形态）。"""
    ref = schema.get("$ref") or ""
    if ref.startswith("#/components/schemas/"):
        name = ref.rsplit("/", 1)[-1]
        return ((api_spec.get("components") or {}).get("schemas") or {}).get(name) or {}
    return schema


def _args_model(tool_name: str, metadata: dict) -> tuple[Any, list[_Param]]:
    """工具元数据 → (pydantic 动态模型, 参数位置表)。

    impex 的 api_spec 是扁平结构：request_body（请求体 schema，可能 $ref）+
    parameters（query/path/header 参数，**必填参数常只在这里**，如 contextloader
    的 kn_id/ot_id）。两处都要进 args model，否则 LLM 无处可填 → 下游 400。
    """
    api_spec = metadata.get("api_spec") or {}
    fields: dict[str, Any] = {}
    params: list[_Param] = []
    taken: set[str] = set()

    def _add(wire: str, location: str, schema: dict, required: bool, desc: str) -> None:
        if not wire or wire.lower() in _IDENTITY_HEADERS:
            return
        field = _safe_field(wire, taken)
        taken.add(field)
        params.append(_Param(field=field, wire=wire, location=location))
        typ = _TYPE_MAP.get((schema or {}).get("type"), Any)
        enum = (schema or {}).get("enum")
        if enum:
            desc = f"{desc}（可选值：{', '.join(str(e) for e in enum)}）" if desc else f"可选值：{', '.join(str(e) for e in enum)}"
        if required:
            fields[field] = (typ, Field(description=desc))
        else:
            fields[field] = (Optional[typ], Field(default=None, description=desc))

    for p in api_spec.get("parameters") or []:
        p = p or {}
        loc = (p.get("in") or "").lower()
        if loc not in ("query", "path"):
            continue  # header 参数（身份）由 runtime 注入，不给 LLM
        _add(p.get("name") or "", loc, p.get("schema") or {}, bool(p.get("required")), p.get("description") or "")

    body = api_spec.get("request_body") or {}
    schema = _resolve_ref(
        ((body.get("content") or {}).get("application/json") or {}).get("schema") or {}, api_spec
    )
    body_required = set(schema.get("required") or [])
    for pname, p in (schema.get("properties") or {}).items():
        p = p or {}
        _add(pname, "body", p, pname in body_required, p.get("description") or "")

    model = create_model(
        f"{_safe_name(tool_name, 'x')}_args",
        __config__=ConfigDict(protected_namespaces=()),  # 允许 model_* 之类的下游参数名
        **fields,
    )
    return model, params


async def _execute(
    box_id: str,
    tool_id: str,
    method: str,
    args: dict,
    params: list[_Param],
    account_id: str,
    account_type: str,
) -> str:
    """经执行代理调用工具。工具级失败以字符串返回给 LLM（可自我修正），
    不抛异常击穿整轮对话。参数按元数据声明的位置分发到 body/query/path。"""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/proxy/{tool_id}"
    identity = {"x-account-id": account_id, "x-account-type": account_type}
    payload: dict[str, Any] = {"timeout": 60, "header": identity, "body": {}, "query": {}, "path": {}}
    by_field = {p.field: p for p in params}
    fallback = "query" if method.upper() in ("GET", "DELETE") else "body"
    for field, value in args.items():
        if value is None:
            continue
        p = by_field.get(field)
        bucket, wire = (p.location, p.wire) if p else (fallback, field)
        if bucket == "path":
            payload["path"][wire] = str(value)
        else:
            payload[bucket][wire] = value
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
    raw_name = info.get("name") or ""
    name = _safe_name(raw_name, tool_id)
    if name != raw_name:  # LLM 见到的名字与注册名不同，日志留映射便于排障
        logger.info("[Toolbox] tool name sanitized: %r -> %s (id=%s)", raw_name, name, tool_id)
    description = info.get("description") or metadata.get("summary") or name

    # 单个工具元数据坏（非法参数名、schema 畸形）不应连累整箱工具装载
    try:
        model, params = _args_model(name, metadata)
    except Exception as e:
        logger.warning("[Toolbox] skip tool %s (id=%s): args schema build failed: %s", name, tool_id, e)
        return None

    async def call(**kwargs) -> str:
        return await _execute(box_id, tool_id, method, kwargs, params, account_id, account_type)

    return StructuredTool.from_function(
        coroutine=call,
        name=name,
        description=description,
        args_schema=model,
    )


async def _list_tools(box_id: str, account_id: str, account_type: str) -> list[dict]:
    """拉取一个 box 的工具列表。工厂 4xx（box 不存在/无权限）= 调用方配置问题 → 400；
    5xx 与网络故障 = 下游不可用 → 502。都走平台错误封套。"""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/tools/list"
    headers = {"x-account-id": account_id, "x-account-type": account_type}
    infos: list[dict] = []
    page = 1
    try:
        async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=15)) as http:
            while True:
                params = {"page": page, "page_size": 100, "all": "true"}
                async with http.get(url, params=params, headers=headers) as resp:
                    body = await resp.text()
                    if 400 <= resp.status < 500:
                        raise bad_request(
                            "ToolRef.BoxUnavailable",
                            "引用的工具箱不可用",
                            f"toolbox {box_id}: HTTP {resp.status} {body[:300]}",
                            "检查 agent.tools 里的 box_id 是否存在、当前账户是否有权访问。",
                        )
                    if resp.status != 200:
                        raise err(
                            502,
                            "Toolbox.Upstream",
                            "算子工厂不可用",
                            f"toolbox {box_id} list failed: HTTP {resp.status} {body[:300]}",
                            "稍后重试；持续失败检查 operator-integration。",
                        )
                    data = json.loads(body)
                infos.extend(data.get("tools") or [])
                if not data.get("has_next"):
                    return infos
                page += 1
    except HTTPException:
        raise
    except Exception as e:  # 连接失败/超时/响应体畸形
        raise err(
            502,
            "Toolbox.Upstream",
            "算子工厂不可用",
            f"toolbox {box_id} list failed: {type(e).__name__}: {e}",
            "稍后重试；持续失败检查 operator-integration 与网络。",
        )


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
