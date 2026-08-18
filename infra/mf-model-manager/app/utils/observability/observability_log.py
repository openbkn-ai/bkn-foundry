# -*- coding:utf-8 -*-

"""
Observability logging module.

The AnyRobot (AR) log exporter and tlogging.SamplerLogger have been removed in
favor of the standard logging library. Public methods such as info, error, warn,
debug, and fatal remain unchanged.
"""
import inspect
import logging
from typing import Optional
from opentelemetry import context

from app.utils.observability.observability_setting import LogSetting, ServerInfo


class _StdLogger:
    """Stdlib replacement for SamplerLogger that preserves the used interfaces."""

    def __init__(self, name: str = "mf-model"):
        self._log = logging.getLogger(name)
        self._log.setLevel(logging.DEBUG)
        if not self._log.handlers:
            handler = logging.StreamHandler()
            handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
            self._log.addHandler(handler)

    def info(self, message: str = "", ctx=None, **_):
        self._log.info(message)

    def warning(self, message: str = "", ctx=None, **_):
        self._log.warning(message)

    warn = warning  # Preserve the legacy method name.

    def error(self, message: str = "", ctx=None, **_):
        self._log.error(message)

    def debug(self, message: str = "", ctx=None, **_):
        self._log.debug(message)

    def fatal(self, message: str = "", ctx=None, **_):
        self._log.critical(message)

    def set_exporters(self, *args, **kwargs):
        return None

    def shutdown(self, *args, **kwargs):
        return None


# Global logger.
logger: Optional[_StdLogger] = None


def get_caller_info() -> str:
    """Return the caller's file name, line number, and function name."""
    frame = inspect.stack()[2]
    return f"{frame.filename}:{frame.lineno}:{frame.function}"


def _ensure_logger() -> _StdLogger:
    global logger
    if logger is None:
        logger = _StdLogger()
    return logger


def info(msg: str, ctx: Optional[context.Context] = None) -> None:
    _ensure_logger().info(message=f"{get_caller_info()}: {msg}", ctx=ctx)


def error(msg: str, ctx: Optional[context.Context] = None) -> None:
    _ensure_logger().error(message=f"{get_caller_info()}: {msg}", ctx=ctx)


def warn(msg: str, ctx: Optional[context.Context] = None) -> None:
    _ensure_logger().warning(message=f"{get_caller_info()}: {msg}", ctx=ctx)


def debug(msg: str, ctx: Optional[context.Context] = None) -> None:
    _ensure_logger().debug(message=f"{get_caller_info()}: {msg}", ctx=ctx)


def fatal(msg: str, ctx: Optional[context.Context] = None) -> None:
    _ensure_logger().fatal(message=f"{get_caller_info()}: {msg}", ctx=ctx)
    exit(1)


def init_log_provider(server_info: ServerInfo, setting: LogSetting) -> None:
    """Initialize stdlib logging after removal of the AR exporter."""
    global logger
    logger = _StdLogger(server_info.server_name or "mf-model")


def get_logger():
    return _ensure_logger()


def shutdown_log_provider(*args, **kwargs):
    global logger
    if logger is not None:
        logger.shutdown()
