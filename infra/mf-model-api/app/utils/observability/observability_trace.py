# -*- coding:utf-8 -*-

"""
可观测性 Trace 模块。

原 AnyRobot(AR) trace exporter 已移除。当前不安装自定义 TracerProvider，
OpenTelemetry 默认返回 no-op tracer：span 为 non-recording、开销近零。
trace_wrapper / trace_context 的埋点保持可用，后续可接入 OTLP exporter
（例如指向 bkn-trace）而无需改动埋点。
"""

from app.utils.observability.observability_setting import TraceSetting, ServerInfo


def init_trace_provider(server_info: ServerInfo, setting: TraceSetting) -> None:
    """No-op trace provider（AR exporter 已移除）。

    保留函数签名以兼容调用方（应用初始化）。不安装任何 SpanProcessor/Exporter，
    依赖 OTel 默认的 no-op tracer。
    """
    return None
