"""Loading toolbox tools from the execution factory.

The tool surface is consolidated in the execution factory
(operator-integration): the built-in contextloader tool set, the sandbox, web
search, published agents, and the rest all read their metadata (OpenAPI) from a
toolbox, and every execution goes through the proxy at
POST /internal-v1/tool-box/{box_id}/proxy/{tool_id}. bkn-agent keeps no
dedicated MCP channel to agent-retrieval; an external MCP endpoint can still be
mounted explicitly through a ToolRef with type=mcp.

Identity is forwarded under the /in convention: the x-account-id and
x-account-type request headers (the operator-integration internal tree verifies
the account exists and maps it to a user_id) are carried in the headers the
execution proxy forwards, so a downstream service such as the agent-retrieval
/in route authorizes against the real caller.
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
from app.commons import locale
from app.commons.i18n import localized_message
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

# LLM tool-name constraints, following the OpenAI function name rules
_NAME_RE = re.compile(r"[^a-zA-Z0-9_-]")
# Identity headers are injected by the runtime under the /in convention; the LLM never decides them
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
    """One LLM-visible parameter: field is the model-facing name (a valid python
    identifier), wire is the real downstream parameter name, and location is
    body, query, or path, which decides the bucket it lands in inside the
    execution proxy payload."""

    field: str
    wire: str
    location: str


def _safe_name(name: str, tool_id: str) -> str:
    cleaned = _NAME_RE.sub("_", name or "")[:64]
    if not re.search(r"[a-zA-Z0-9]", cleaned):
        cleaned = f"tool_{tool_id[:8]}"
    return cleaned


def _safe_field(name: str, taken: set[str]) -> str:
    """Map a parameter name onto a valid, non-reserved python field name, as
    pydantic create_model requires."""
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
    """Resolve a single level of #/components/schemas/X references, the shape
    .adp and impex conventionally produce."""
    ref = schema.get("$ref") or ""
    if ref.startswith("#/components/schemas/"):
        name = ref.rsplit("/", 1)[-1]
        return ((api_spec.get("components") or {}).get("schemas") or {}).get(name) or {}
    return schema


def _args_model(tool_name: str, metadata: dict) -> tuple[Any, list[_Param]]:
    """Turn tool metadata into a dynamic pydantic model plus a parameter location
    table.

    The impex api_spec is flat: request_body (the body schema, possibly a $ref)
    plus parameters (query, path, and header parameters, where **required
    parameters often live exclusively**, such as contextloader's kn_id and
    ot_id). Both have to enter the args model, otherwise the LLM has nowhere to
    put them and the downstream call answers 400.
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
            hint = localized_message(
                "BknAgent.Tool.EnumHint", values=", ".join(str(e) for e in enum)
            )
            desc = f"{desc}{hint}" if desc else hint.strip()
        if required:
            fields[field] = (typ, Field(description=desc))
        else:
            fields[field] = (Optional[typ], Field(default=None, description=desc))

    for p in api_spec.get("parameters") or []:
        p = p or {}
        loc = (p.get("in") or "").lower()
        if loc not in ("query", "path"):
            continue  # Header parameters carry identity, injected by the runtime, never exposed to the LLM
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
        __config__=ConfigDict(protected_namespaces=()),  # Allow downstream parameter names such as model_*
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
    """Call a tool through the execution proxy. A tool-level failure is returned
    to the LLM as a string so it can correct itself, rather than raised as an
    exception that would tear through the whole turn. Parameters are dispatched
    to body, query, or path according to the location the metadata declares."""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/proxy/{tool_id}"
    identity = {"x-account-id": account_id, "x-account-type": account_type}
    operation_id, parent_event_id = evidence.new_operation()
    headers = locale.internal_request_headers({**identity, **observability.outbound_headers()})
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
    if name != raw_name:  # The name the LLM sees differs from the registered one; log the mapping for triage
        logger.info("[Toolbox] tool name sanitized: %r -> %s (id=%s)", raw_name, name, tool_id)
    description = info.get("description") or metadata.get("summary") or name
    expected_fact_event_type = _expected_fact_event_type(metadata)

    # One broken tool metadata entry (an invalid parameter name, a malformed
    # schema) must not take down loading for the whole box.
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
    """Fetch the tool list of one box. A 4xx from the factory (the box does not
    exist, or access is denied) is a caller configuration problem and maps to
    400; a 5xx or a network failure means the downstream is unavailable and maps
    to 502. Both go out as the platform error envelope."""
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/tool-box/{box_id}/tools/list"
    headers = locale.internal_request_headers(
        {"x-account-id": account_id, "x-account-type": account_type, **observability.outbound_headers()}
    )
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
    except Exception as e:  # Connection failure, timeout, or a malformed response body
        raise err(502, "BknAgent.Toolbox.Upstream", box_id=box_id, error_type=type(e).__name__, error=e)


async def load_toolbox_tools(box_id: str, account_id: str, account_type: str) -> list[StructuredTool]:
    """Load every enabled tool of one toolbox. A failed list fetch raises, and
    the caller decides whether to degrade (the default tool set) or report the
    error (an explicit reference)."""
    infos = await _list_tools(box_id, account_id, account_type)
    tools = []
    for info in infos:
        tool = _build_tool(box_id, info, account_id, account_type)
        if tool:
            tools.append(tool)
    return tools
