
# -*- coding:utf-8 -*-

import logging

from app.utils.observability.observability_setting import ServerInfo, ObservabilitySetting
from app.utils.observability.observability_log import init_log_provider, shutdown_log_provider
from app.utils.observability.observability_trace import init_trace_provider, shutdown_trace_provider

def init_observability(server_info: ServerInfo, setting: ObservabilitySetting):

    """Initialize the observability components."""
    if setting.log.log_enabled:
        init_log_provider(server_info, setting.log)

    if setting.trace.trace_enabled:
        try:
            init_trace_provider(server_info, setting.trace)
        except Exception:
            logging.getLogger(__name__).exception(
                "OTLP trace initialization failed; business continues"
            )
        
    # if setting.metric.metric_enabled:
        # pass
        # init_meter_provider(server_info, setting.metric)


def shutdown_observability() -> None:
    """Shut down the observability components."""
    shutdown_log_provider()
    shutdown_trace_provider()
