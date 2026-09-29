"""Small, batched OTLP pipeline for Sandbox control-plane request facts."""

import logging
import os
import uuid
from contextlib import nullcontext

_provider = None
_trace_provider = None
_tracer = None


def setup() -> None:
    global _provider, _trace_provider, _tracer
    endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otelcol-contrib:4318")
    if _provider is None and os.getenv("OTEL_LOGS_ENABLED", "true").lower() == "true":
        try:
            _setup_logs(endpoint)
        except Exception:
            _provider = None
    if _trace_provider is None and os.getenv("OTEL_TRACES_ENABLED", "true").lower() == "true":
        try:
            _setup_traces(endpoint)
        except Exception:
            _trace_provider = None
            _tracer = None


def _setup_logs(endpoint: str) -> None:
    global _provider
    from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
    from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
    from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
    from opentelemetry.sdk.resources import Resource

    _provider = LoggerProvider(
        resource=Resource.create(
            {
                "service.name": "sandbox-control-plane",
                "deployment.environment": os.getenv("ENVIRONMENT", "production"),
            }
        )
    )
    _provider.add_log_record_processor(
        BatchLogRecordProcessor(
            OTLPLogExporter(endpoint=f"{endpoint.rstrip('/')}/v1/logs", timeout=3),
            max_queue_size=2048,
            max_export_batch_size=512,
            schedule_delay_millis=1000,
            export_timeout_millis=3000,
        )
    )
    target = logging.getLogger("sandbox.telemetry")
    target.addHandler(LoggingHandler(level=logging.INFO, logger_provider=_provider))
    target.setLevel(logging.INFO)
    target.propagate = False


def _setup_traces(endpoint: str) -> None:
    global _trace_provider, _tracer
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    _trace_provider = TracerProvider(
        resource=Resource.create(
            {
                "service.name": "sandbox-control-plane",
                "deployment.environment": os.getenv("ENVIRONMENT", "production"),
            }
        )
    )
    _trace_provider.add_span_processor(
        BatchSpanProcessor(
            OTLPSpanExporter(endpoint=f"{endpoint.rstrip('/')}/v1/traces", timeout=3),
            max_queue_size=2048,
            max_export_batch_size=512,
            schedule_delay_millis=1000,
            export_timeout_millis=3000,
        )
    )
    _tracer = _trace_provider.get_tracer("sandbox-control-plane/http")


def build_http_log_attributes(method: str, route: str, status: int) -> dict:
    source_log_id = str(uuid.uuid4())
    outcome = "denied" if status in (401, 403) else ("success" if status < 400 else "failure")
    return {
        "schema_version": "1.0.0",
        "log_id": source_log_id,
        "source_log_id": source_log_id,
        "source_id": "sandbox",
        "log_category": "runtime.system",
        "event_name": "http.request.completed",
        "outcome": outcome,
        "safe_summary": f"{method} {route} completed with HTTP {status}",
        "http.request.method": method,
        "http.route": route,
        "http.response.status_code": status,
    }


def emit_http_request_log(method: str, route: str, status: int) -> None:
    if _provider is None:
        return
    try:
        logging.getLogger("sandbox.telemetry").info(
            "http.request.completed", extra=build_http_log_attributes(method, route, status)
        )
    except Exception:
        pass


def build_http_span_attributes(method: str, route: str, status: int) -> dict:
    return {
        "http.request.method": method,
        "http.route": route,
        "http.response.status_code": status,
    }


def start_http_request_span():
    if _tracer is None:
        return nullcontext(None)
    from opentelemetry.trace import SpanKind

    return _tracer.start_as_current_span("HTTP request", kind=SpanKind.SERVER)


def finish_http_request_span(span, method: str, route: str, status: int) -> None:
    if span is None:
        return
    span.update_name(f"{method} {route}")
    span.set_attributes(build_http_span_attributes(method, route, status))
    if status >= 500:
        from opentelemetry.trace.status import Status, StatusCode

        span.set_status(Status(StatusCode.ERROR))


def shutdown() -> None:
    global _provider, _trace_provider, _tracer
    if _provider is not None:
        _provider.shutdown()
        _provider = None
    if _trace_provider is not None:
        _trace_provider.shutdown()
        _trace_provider = None
    _tracer = None
