# -*- coding:utf-8 -*-

"""
Observability tracing module.

The AnyRobot (AR) trace exporter has been removed. No custom TracerProvider is
installed, so OpenTelemetry returns a no-op tracer by default: spans are not
recorded and add negligible overhead. Instrumentation in trace_wrapper and
trace_context remains available for a future OTLP exporter, such as bkn-trace.
"""

from app.utils.observability.observability_setting import TraceSetting, ServerInfo


def init_trace_provider(server_info: ServerInfo, setting: TraceSetting) -> None:
    """Initialize the no-op trace provider after removal of the AR exporter.

    Preserve the function signature for application initialization callers. No
    SpanProcessor or exporter is installed; this relies on OTel's default no-op tracer.
    """
    return None
