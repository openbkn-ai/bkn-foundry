# -*- coding:utf-8 -*-

"""Batched OTLP trace provider for the internal Model API."""

import os

from app.utils.observability.observability_setting import TraceSetting, ServerInfo

_trace_provider = None


def init_trace_provider(server_info: ServerInfo, setting: TraceSetting) -> None:
    global _trace_provider
    if _trace_provider is not None:
        return
    from opentelemetry import trace
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otelcol-contrib:4318")
    _trace_provider = TracerProvider(resource=Resource.create({
        "service.name": "model-api",
        "deployment.environment": os.getenv("ENVIRONMENT", "production"),
    }))
    _trace_provider.add_span_processor(BatchSpanProcessor(
        OTLPSpanExporter(endpoint=f"{endpoint.rstrip('/')}/v1/traces", timeout=3),
        max_queue_size=setting.trace_max_queue_size,
        max_export_batch_size=setting.max_export_batch_size,
        schedule_delay_millis=1000,
        export_timeout_millis=3000,
    ))
    trace.set_tracer_provider(_trace_provider)


def shutdown_trace_provider() -> None:
    global _trace_provider
    if _trace_provider is not None:
        _trace_provider.shutdown()
        _trace_provider = None
