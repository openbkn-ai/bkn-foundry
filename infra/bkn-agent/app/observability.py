import logging
import os
import re
import uuid
from contextvars import ContextVar
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Optional

logger = logging.getLogger("bkn-agent.otel")

_tracer = None
TRACE_SCHEMA_VERSION = "1.0.0"
MODULE_NAME = "bkn-agent"
REQUEST_ID_HEADER = "bkn-request-id"
LEGACY_REQUEST_ID_HEADER = "x-request-id"
TRACE_ID_HEADER = "x-trace-id"
EVENT_OBSERVED_AT_HEADER = "bkn-event-observed-at"
CONVERSATION_ID_HEADER = "bkn-conversation-id"
LEGACY_OBSERVED_AT_HEADER = "bkn-trace-observed-at"
_TRACEPARENT_RE = re.compile(r"^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$")
_REQUEST_ID_RE = re.compile(r"^(req_[0-9A-Za-z_.-]+|[0-9A-Za-z_.-]{8,128})$")


def _utc_now() -> str:
    return (
        datetime.now(timezone.utc)
        .isoformat(timespec="microseconds")
        .replace("+00:00", "Z")
    )


@dataclass(frozen=True)
class TraceContext:
    trace_id: str
    request_id: str
    traceparent: str
    entry_boundary: str
    upstream_span_id: Optional[str] = None
    conversation_id: Optional[str] = None
    tenant_id: Optional[str] = None
    business_domain: Optional[str] = None
    account_id: Optional[str] = None
    account_type: Optional[str] = None
    observed_at: str = field(default_factory=_utc_now)


_current_context: ContextVar[Optional[TraceContext]] = ContextVar(
    "bkn_trace_context", default=None
)
_current_operation_headers: ContextVar[dict[str, str]] = ContextVar(
    "bkn_trace_operation_headers", default={}
)


OPENINFERENCE_REDACTION_DEFAULTS = {
    "OPENINFERENCE_HIDE_INPUTS": "true",
    "OPENINFERENCE_HIDE_OUTPUTS": "true",
    "OPENINFERENCE_HIDE_INPUT_MESSAGES": "true",
    "OPENINFERENCE_HIDE_OUTPUT_MESSAGES": "true",
    "OPENINFERENCE_HIDE_INPUT_TEXT": "true",
    "OPENINFERENCE_HIDE_OUTPUT_TEXT": "true",
    "OPENINFERENCE_HIDE_LLM_INVOCATION_PARAMETERS": "true",
    "OPENINFERENCE_HIDE_LLM_TOOLS": "true",
    "OPENINFERENCE_HIDE_PROMPTS": "true",
}


def configure_openinference_redaction() -> None:
    """Default LangChain/OpenInference spans to hash-only BKN Trace safety."""
    for key, value in OPENINFERENCE_REDACTION_DEFAULTS.items():
        os.environ.setdefault(key, value)


def setup_otel(app) -> None:
    """OTel GenAI 链路：openinference LangChain instrumentation（不自研埋点层）
    + FastAPI 入口 span，OTLP HTTP 导出到平台 otelcol-contrib。失败降级为不埋点，不影响服务。"""
    global _tracer
    if os.getenv("OTEL_ENABLED", "true").lower() != "true":
        return
    try:
        configure_openinference_redaction()
        from openinference.instrumentation.langchain import LangChainInstrumentor
        from opentelemetry import trace
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import (
            OTLPSpanExporter,
        )
        from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor

        endpoint = os.getenv(
            "OTEL_EXPORTER_OTLP_ENDPOINT", "http://otelcol-contrib:4318"
        )
        provider = TracerProvider(
            resource=Resource.create({"service.name": "bkn-agent"})
        )
        provider.add_span_processor(
            BatchSpanProcessor(
                OTLPSpanExporter(endpoint=f"{endpoint.rstrip('/')}/v1/traces")
            )
        )
        trace.set_tracer_provider(provider)
        LangChainInstrumentor().instrument(tracer_provider=provider)
        FastAPIInstrumentor.instrument_app(app, excluded_urls="/api/v1/health")
        _tracer = trace.get_tracer("bkn-agent")
        logger.info("OTel enabled, exporting to %s", endpoint)
    except Exception as e:
        logger.warning("OTel setup failed, tracing disabled: %s", e)


def _new_trace_id() -> str:
    return uuid.uuid4().hex


def _new_span_id() -> str:
    return uuid.uuid4().hex[:16]


def _new_request_id() -> str:
    return f"req_{uuid.uuid4().hex}"


def _valid_request_id(value: str | None) -> bool:
    return bool(value and _REQUEST_ID_RE.match(value))


def _valid_correlation_id(value: str | None) -> bool:
    return bool(value and re.fullmatch(r"[0-9A-Za-z_.:-]{1,128}", value))


def parse_traceparent(value: str | None) -> tuple[Optional[str], Optional[str]]:
    if not value:
        return None, None
    match = _TRACEPARENT_RE.match(value.strip().lower())
    if not match:
        return None, None
    trace_id, span_id = match.groups()
    if trace_id == "0" * 32 or span_id == "0" * 16:
        return None, None
    return trace_id, span_id


def format_traceparent(trace_id: str, span_id: str, flags: str = "01") -> str:
    return f"00-{trace_id}-{span_id}-{flags}"


def _active_otel_trace_identity() -> tuple[Optional[str], Optional[str]]:
    try:
        from opentelemetry import trace

        span_context = trace.get_current_span().get_span_context()
        if not span_context.is_valid:
            return None, None
        return f"{span_context.trace_id:032x}", f"{span_context.span_id:016x}"
    except Exception:
        return None, None


def build_context(headers) -> TraceContext:
    """Build BKN Trace phase-one context from inbound request headers.

    `traceparent` carries W3C trace identity. `bkn-request-id` is the OpenBKN
    business correlation key and is intentionally separate from baggage.
    """
    incoming_traceparent = headers.get("traceparent")
    trace_id, span_id = parse_traceparent(incoming_traceparent)
    entry_boundary = "external" if trace_id else "internal"
    if not trace_id:
        trace_id, span_id = _active_otel_trace_identity()
    trace_id = trace_id or _new_trace_id()
    request_id = headers.get(REQUEST_ID_HEADER) or headers.get(LEGACY_REQUEST_ID_HEADER)
    if not _valid_request_id(request_id):
        request_id = _new_request_id()
    traceparent = (
        format_traceparent(trace_id, span_id)
        if span_id
        else format_traceparent(trace_id, _new_span_id())
    )
    tenant_id = (headers.get("x-tenant-id") or "").strip() or None
    business_domain = (headers.get("x-business-domain") or "").strip() or None
    account_id = (headers.get("x-account-id") or "").strip() or None
    account_type = (headers.get("x-account-type") or "").strip() or None
    conversation_id = (headers.get(CONVERSATION_ID_HEADER) or "").strip() or None
    if not _valid_correlation_id(conversation_id):
        conversation_id = None
    observed_at = (
        headers.get(EVENT_OBSERVED_AT_HEADER)
        or headers.get(LEGACY_OBSERVED_AT_HEADER)
        or ""
    ).strip()
    if observed_at:
        try:
            datetime.fromisoformat(observed_at.replace("Z", "+00:00"))
        except ValueError:
            observed_at = ""
    return TraceContext(
        trace_id=trace_id,
        request_id=request_id,
        traceparent=traceparent,
        entry_boundary=entry_boundary,
        upstream_span_id=span_id,
        conversation_id=conversation_id,
        tenant_id=tenant_id,
        business_domain=business_domain,
        account_id=account_id,
        account_type=account_type,
        observed_at=observed_at or _utc_now(),
    )


def set_context(ctx: TraceContext):
    return _current_context.set(ctx)


def reset_context(token) -> None:
    _current_context.reset(token)


def current_context() -> Optional[TraceContext]:
    return _current_context.get()


def context_from_request(request) -> Optional[TraceContext]:
    return getattr(getattr(request, "state", None), "bkn_trace_context", None)


def current_trace_id(ctx: Optional[TraceContext] = None) -> str:
    ctx = ctx or current_context()
    return ctx.trace_id if ctx else _new_trace_id()


def response_headers(ctx: Optional[TraceContext] = None) -> dict[str, str]:
    ctx = ctx or current_context()
    if not ctx:
        return {}
    headers = {
        TRACE_ID_HEADER: ctx.trace_id,
        REQUEST_ID_HEADER: ctx.request_id,
        LEGACY_REQUEST_ID_HEADER: ctx.request_id,
        "traceparent": ctx.traceparent,
    }
    if ctx.conversation_id:
        headers[CONVERSATION_ID_HEADER] = ctx.conversation_id
    return headers


def outbound_headers(ctx: Optional[TraceContext] = None) -> dict[str, str]:
    """Trace context headers for downstream HTTP/toolbox/MCP calls."""
    ctx = ctx or current_context()
    business = (
        {"x-business-domain": ctx.business_domain}
        if ctx and ctx.business_domain
        else {}
    )
    tenant = {"x-tenant-id": ctx.tenant_id} if ctx and ctx.tenant_id else {}
    identity = {}
    if ctx and ctx.account_id and ctx.account_type:
        identity = {
            "x-account-id": ctx.account_id,
            "x-account-type": ctx.account_type,
        }
    replay = {EVENT_OBSERVED_AT_HEADER: ctx.observed_at} if ctx else {}
    return {
        **response_headers(ctx),
        **tenant,
        **business,
        **identity,
        **replay,
        **_current_operation_headers.get(),
    }


def set_operation_headers(headers: dict[str, str]):
    return _current_operation_headers.set(dict(headers))


def reset_operation_headers(token) -> None:
    _current_operation_headers.reset(token)


def enrich_error(content: dict) -> dict:
    enriched = dict(content)
    enriched.setdefault("trace_id", current_trace_id())
    return enriched


def context_attributes() -> dict:
    ctx = current_context()
    attrs = {
        "bkn.module.name": MODULE_NAME,
        "bkn.trace.schema.version": TRACE_SCHEMA_VERSION,
    }
    if ctx:
        attrs.update(
            {
                "bkn.request.id": ctx.request_id,
                "bkn.conversation.id": ctx.conversation_id,
                "bkn.tenant.id": ctx.tenant_id,
                "bkn.trace.entry_boundary": ctx.entry_boundary,
                "trace_id": ctx.trace_id,
                "traceparent": ctx.traceparent,
            }
        )
    return attrs


def _normalized_attributes(attributes: dict) -> dict:
    normalized = dict(attributes)
    aliases = {
        "agent.id": "bkn.agent.id",
        "thread.id": "bkn.thread.id",
        "task.id": "bkn.task.id",
        "prompt.source": "bkn.prompt.source",
        "prompt.version": "bkn.prompt.version",
    }
    for old, new in aliases.items():
        if old in normalized and new not in normalized:
            normalized[new] = normalized[old]
    for key, value in context_attributes().items():
        normalized.setdefault(key, value)
    return {k: v for k, v in normalized.items() if v is not None}


def span(name: str, attributes: dict):
    """业务外层 span（agent.chat / agent.task），挂 agent_id/thread_id/task_id 等属性。"""
    if _tracer is None:
        from contextlib import nullcontext

        return nullcontext()
    return _tracer.start_as_current_span(
        name, attributes=_normalized_attributes(attributes)
    )
