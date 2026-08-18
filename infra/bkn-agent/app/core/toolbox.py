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
from urllib.parse import urlsplit

import aiohttp
from fastapi import HTTPException
from langchain_core.tools import StructuredTool
from pydantic import ConfigDict, Field, create_model

from app import evidence, observability
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
_CONTEXT_LOADER_RETRIEVAL_PATHS = {
    "/api/agent-retrieval/in/v1/kn/search_schema",
    "/api/agent-retrieval/in/v1/kn/search_instance",
    "/api/agent-retrieval/in/v1/kn/query_object_instance",
    "/api/agent-retrieval/in/v1/kn/query_instance_subgraph",
    "/api/agent-retrieval/in/v1/kn/explore_subgraph",
}


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
    if not field or field[0].isdigit() or field.startswith("_"):
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
    tool_name: str,
    method: str,
    args: dict,
    params: list[_Param],
    account_id: str,
    account_type: str,
    expected_fact_event_type: str | None = None,
) -> str:
    """经执行代理调用工具。工具级失败以字符串返回给 LLM（可自我修正），
    不抛异常击穿整轮对话。参数按元数据声明的位置分发到 body/query/path。"""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/proxy/{tool_id}"
    identity = {"x-account-id": account_id, "x-account-type": account_type}
    operation_id, parent_event_id = evidence.new_operation()
    headers = {**identity, **observability.outbound_headers()}
    payload: dict[str, Any] = {"timeout": 60, "header": headers, "body": {}, "query": {}, "path": {}}
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
    operation_name = "bkn.agent.tool.call"
    args_hash = evidence.hash_value({"body": payload["body"], "query": payload["query"], "path": payload["path"]})
    start_event = evidence.tool_called(
        tool_id=tool_id,
        tool_name=tool_name,
        toolbox_id=box_id,
        args_hash=args_hash,
        operation_name=operation_name,
        operation_id=operation_id,
        causation_event_id=parent_event_id,
    )
    if start_event:
        headers.update(evidence.operation_headers(operation_id, start_event["event_id"]))
        payload["header"].update(evidence.operation_headers(operation_id, start_event["event_id"]))

    async def _finish(
        result: str,
        *,
        success: bool,
        status_code: int | None = None,
        error: str | None = None,
        result_value: Any = None,
    ) -> str:
        result_event = evidence.tool_result_observed(
            tool_id=tool_id,
            tool_name=tool_name,
            toolbox_id=box_id,
            result_hash=evidence.hash_value(result) if success else None,
            result_length=len(result) if success else None,
            result_count=evidence.result_count(result_value if result_value is not None else result)
            if success else None,
            success=success,
            status_code=status_code,
            error_hash=evidence.hash_value(error or result) if not success else None,
            operation_name=operation_name,
            partial_reason=[] if success else ["tool_result_failed"],
            operation_id=operation_id,
            causation_event_id=start_event["event_id"] if start_event else parent_event_id or "",
        )
        await evidence.submit_events([event for event in [start_event, result_event] if event], account_id, account_type)
        return result

    with observability.span(
        operation_name,
        {
            "bkn.toolbox.id": box_id,
            "bkn.tool.id": tool_id,
            "bkn.tool.name": tool_name,
            "bkn.tool.args_hash": args_hash,
        },
    ):
        try:
            async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=90)) as http:
                async with http.post(url, json=payload, headers=headers) as resp:
                    text = await resp.text()
                    if not 200 <= resp.status < 300:
                        return await _finish(
                            f"tool call failed: HTTP {resp.status} {text[:500]}",
                            success=False,
                            status_code=resp.status,
                            error=text,
                        )
        except Exception as e:
            return await _finish(f"tool call failed: {e}", success=False, error=f"{type(e).__name__}: {e}")
        try:
            data = json.loads(text)
        except ValueError:
            return await _finish(text, success=True, status_code=200)
        if data.get("error"):
            return await _finish(f"tool error: {data['error']}", success=False, error=str(data["error"]))
        status_code = data.get("status_code") or 0
        body = data.get("body")
        body_text = body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)
        evidence.record_fact_receipt(
            operation_id=operation_id,
            headers=data.get("headers") if isinstance(data.get("headers"), dict) else {},
            body=body,
            context_hash=evidence.tool_message_context_hash(body_text),
            expected_event_type=(
                expected_fact_event_type if status_code < 400 else None
            ),
        )
        if status_code >= 400:
            return await _finish(
                f"tool target failed: HTTP {status_code} {body_text[:800]}",
                success=False,
                status_code=status_code,
                error=body_text,
            )
        return await _finish(
            body_text, success=True, status_code=status_code, result_value=body
        )


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
    expected_fact_event_type = _expected_fact_event_type(metadata)

    # 单个工具元数据坏（非法参数名、schema 畸形）不应连累整箱工具装载
    try:
        model, params = _args_model(name, metadata)
    except Exception as e:
        logger.warning("[Toolbox] skip tool %s (id=%s): args schema build failed: %s", name, tool_id, e)
        return None

    async def call(**kwargs) -> str:
        return await _execute(
            box_id,
            tool_id,
            name,
            method,
            kwargs,
            params,
            account_id,
            account_type,
            expected_fact_event_type=expected_fact_event_type,
        )

    return StructuredTool.from_function(
        coroutine=call,
        name=name,
        description=description,
        args_schema=model,
        metadata={"bkn_trace_native": True, "bkn_tool_id": tool_id},
    )


def _expected_fact_event_type(metadata: dict[str, Any]) -> str | None:
    path = str(metadata.get("path") or "").rstrip("/")
    if path not in _CONTEXT_LOADER_RETRIEVAL_PATHS:
        return None
    host = urlsplit(str(metadata.get("server_url") or "")).hostname or ""
    if host == "agent-retrieval":
        return "retrieval.completed"
    return None


async def _list_tools(box_id: str, account_id: str, account_type: str) -> list[dict]:
    """拉取一个 box 的工具列表。工厂 4xx（box 不存在/无权限）= 调用方配置问题 → 400；
    5xx 与网络故障 = 下游不可用 → 502。都走平台错误封套。"""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/tools/list"
    headers = {"x-account-id": account_id, "x-account-type": account_type, **observability.outbound_headers()}
    infos: list[dict] = []
    page = 1
    try:
        with observability.span(
            "bkn.agent.toolbox.list",
            {
                "bkn.toolbox.id": box_id,
                "bkn.operation.name": "bkn.agent.toolbox.list",
            },
        ):
            async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=15)) as http:
                while True:
                    params = {"page": page, "page_size": 100, "all": "true"}
                    async with http.get(url, params=params, headers=headers) as resp:
                        body = await resp.text()
                        if 400 <= resp.status < 500:
                            raise bad_request(
                                "BknAgent.ToolRef.BoxUnavailable",
                                box_id=box_id,
                                status=resp.status,
                                body=body[:300],
                            )
                        if resp.status != 200:
                            raise err(
                                502,
                                "BknAgent.Toolbox.ListFailed",
                                box_id=box_id,
                                status=resp.status,
                                body=body[:300],
                            )
                        data = json.loads(body)
                    infos.extend(data.get("tools") or [])
                    if not data.get("has_next"):
                        return infos
                    page += 1
    except HTTPException:
        raise
    except Exception as e:  # 连接失败/超时/响应体畸形
        raise err(502, "BknAgent.Toolbox.Upstream", box_id=box_id, error_type=type(e).__name__, error=e)


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
