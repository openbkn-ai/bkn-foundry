import logging
import os

logger = logging.getLogger("bkn-agent.otel")

_tracer = None


def setup_otel(app) -> None:
    """OTel GenAI 链路：openinference LangChain instrumentation（不自研埋点层）
    + FastAPI 入口 span，OTLP HTTP 导出到平台 otelcol-contrib。失败降级为不埋点，不影响服务。"""
    global _tracer
    if os.getenv("OTEL_ENABLED", "true").lower() != "true":
        return
    try:
        from openinference.instrumentation.langchain import LangChainInstrumentor
        from opentelemetry import trace
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
        from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor

        endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otelcol-contrib:4318")
        provider = TracerProvider(resource=Resource.create({"service.name": "bkn-agent"}))
        provider.add_span_processor(
            BatchSpanProcessor(OTLPSpanExporter(endpoint=f"{endpoint.rstrip('/')}/v1/traces"))
        )
        trace.set_tracer_provider(provider)
        LangChainInstrumentor().instrument(tracer_provider=provider)
        FastAPIInstrumentor.instrument_app(app, excluded_urls="/api/v1/health")
        _tracer = trace.get_tracer("bkn-agent")
        logger.info("OTel enabled, exporting to %s", endpoint)
    except Exception as e:
        logger.warning("OTel setup failed, tracing disabled: %s", e)


def span(name: str, attributes: dict):
    """业务外层 span（agent.chat / agent.task），挂 agent_id/thread_id/task_id 等属性。"""
    if _tracer is None:
        from contextlib import nullcontext

        return nullcontext()
    return _tracer.start_as_current_span(
        name, attributes={k: v for k, v in attributes.items() if v is not None}
    )
