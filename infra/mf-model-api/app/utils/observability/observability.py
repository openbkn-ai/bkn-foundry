
# -*- coding:utf-8 -*-

from app.utils.observability.observability_setting import ServerInfo, ObservabilitySetting
from app.utils.observability.observability_log import init_log_provider, shutdown_log_provider
from app.utils.observability.observability_trace import init_trace_provider

def init_observability(server_info: ServerInfo, setting: ObservabilitySetting):

    """Initialize the observability components."""
    if setting.log.log_enabled:
        init_log_provider(server_info, setting.log)

    if setting.trace.trace_enabled:
        init_trace_provider(server_info, setting.trace)
        
    # if setting.metric.metric_enabled:
        # pass
        # init_meter_provider(server_info, setting.metric)


def shutdown_observability() -> None:
    """Shut down the observability components."""
    shutdown_log_provider()
