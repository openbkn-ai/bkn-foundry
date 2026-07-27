import json
from typing import Any, AsyncIterator, Optional

from langchain_core.outputs import ChatGenerationChunk, ChatResult
from langchain_openai import ChatOpenAI

from app import evidence, observability
from app.config import config


def _header_map(metadata: dict[str, Any] | None) -> dict[str, str]:
    headers = (metadata or {}).get("headers") or {}
    return {str(key).lower(): str(value) for key, value in headers.items()}


def _json_list(value: str | None) -> list[Any]:
    if not value:
        return []
    try:
        parsed = json.loads(value)
    except (TypeError, ValueError):
        return []
    return parsed if isinstance(parsed, list) else []


def _record_model_response(message: Any, operation_id: str) -> None:
    metadata = getattr(message, "response_metadata", None)
    headers = _header_map(metadata)
    additional_kwargs = getattr(message, "additional_kwargs", None) or {}
    body = additional_kwargs.get("bkn_trace") or {}
    if not isinstance(body, dict):
        body = {}

    event_id = (
        headers.get("bkn-evidence-event-id")
        or headers.get("bkn-fact-event-id")
        or body.get("source_event_id", "")
    )
    if not event_id:
        return

    adopted_source_event_ids = _json_list(headers.get("bkn-adopted-source-event-ids"))
    if "bkn-adopted-source-event-ids" not in headers:
        adopted_source_event_ids = body.get("adopted_source_event_ids") or []
    evidence_refs = _json_list(headers.get("bkn-fact-evidence-refs"))
    if "bkn-fact-evidence-refs" not in headers:
        evidence_refs = body.get("evidence_refs") or []
    business_refs = _json_list(headers.get("bkn-fact-business-refs"))
    if "bkn-fact-business-refs" not in headers:
        business_refs = body.get("business_refs") or []

    evidence.record_model_fact(
        event_id=event_id,
        operation_id=operation_id,
        adopted_source_event_ids=[
            value for value in adopted_source_event_ids
            if isinstance(value, str)
        ],
        evidence_refs=evidence_refs if isinstance(evidence_refs, list) else [],
        business_refs=business_refs if isinstance(business_refs, list) else [],
    )


class TraceChatOpenAI(ChatOpenAI):
    """Bind each real mf-model-api request to one operation and consume its fact receipt."""

    async def _agenerate(self, messages, stop=None, run_manager=None, **kwargs) -> ChatResult:
        operation_id, parent_event_id = evidence.new_operation()
        if operation_id and parent_event_id:
            kwargs["extra_headers"] = {
                **(kwargs.get("extra_headers") or {}),
                **observability.outbound_headers(),
                **evidence.operation_headers(operation_id, parent_event_id),
                **evidence.model_context_headers(messages, operation_id),
            }
        result = await super()._agenerate(messages, stop, run_manager, **kwargs)
        if operation_id and result.generations:
            _record_model_response(result.generations[0].message, operation_id)
        return result

    async def _astream(self, *args: Any, **kwargs: Any) -> AsyncIterator[ChatGenerationChunk]:
        operation_id, parent_event_id = evidence.new_operation()
        messages = args[0] if args else kwargs.get("messages", [])
        if operation_id and parent_event_id:
            kwargs["extra_headers"] = {
                **(kwargs.get("extra_headers") or {}),
                **observability.outbound_headers(),
                **evidence.operation_headers(operation_id, parent_event_id),
                **evidence.model_context_headers(messages, operation_id),
            }
        response_metadata: dict[str, Any] = {}
        trace_body: dict[str, Any] = {}
        async for chunk in super()._astream(*args, **kwargs):
            if chunk.message.response_metadata:
                response_metadata.update(chunk.message.response_metadata)
            if chunk.generation_info:
                response_metadata.update(chunk.generation_info)
            candidate_body = chunk.message.additional_kwargs.get("bkn_trace")
            if isinstance(candidate_body, dict):
                trace_body.update(candidate_body)
            yield chunk
        if operation_id:
            receipt = type("ModelFactReceipt", (), {
                "response_metadata": response_metadata,
                "additional_kwargs": {"bkn_trace": trace_body},
            })()
            _record_model_response(receipt, operation_id)


def normalize_response_format(rf: Optional[dict[str, Any]]) -> Optional[dict[str, Any]]:
    """with_structured_output 底层过 convert_to_openai_function：纯 JSON Schema 若缺
    name/title 会被判 `Unsupported function`。缺就补个 title，调用方可直接传裸 schema。"""
    if isinstance(rf, dict) and "title" not in rf and "name" not in rf:
        return {"title": "StructuredResponse", **rf}
    return rf


def build_chat_model(
    agent_model: str, streaming: bool = True, max_output_tokens: Optional[int] = None
) -> ChatOpenAI:
    """模型一律经 mf-model-api（集群内 /api/private）。model 为空 → 系统默认模型，
    由 mf-model-api 侧解析，这里不钉模型名。

    streaming=False 用于结构化输出：结构化调用是一次性取整个对象，且部分网关在流式
    结构化时会吐 choices=None 的 chunk 触发 langchain_openai 崩溃，非流式绕开。

    max_output_tokens（limits.max_output_tokens）透传 OpenAI 兼容 max_tokens：
    provider 默认输出上限常见 ~4096，长 JSON（如 catalog 语义理解）会被中途截断。"""
    kwargs = {"max_tokens": max_output_tokens} if max_output_tokens else {}
    return TraceChatOpenAI(
        base_url=config.MF_MODEL_API_PRIVATE_BASE,
        api_key="internal",
        model=agent_model or config.DEFAULT_MODEL,
        streaming=streaming,
        include_response_headers=True,
        **kwargs,
    )
