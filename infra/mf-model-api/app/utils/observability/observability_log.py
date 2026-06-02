# -*- coding:utf-8 -*-

"""
可观测性 Log 模块。

原 AnyRobot(AR) log exporter / tlogging.SamplerLogger 已移除，改用标准库
logging。对外接口（info / error / warn / debug / fatal 等）保持不变。
"""
import inspect
import logging
from typing import Optional
from opentelemetry import context

from app.utils.observability.observability_setting import LogSetting, ServerInfo


class _StdLogger:
    """SamplerLogger 的 stdlib 替身，保留被调用到的接口（message=/ctx= 等）。"""

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

    warn = warning  # 兼容旧调用名

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


# 全局 logger
logger: Optional[_StdLogger] = None


def get_caller_info() -> str:
    """获取调用者信息（文件名、行号、函数名）"""
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
    """初始化日志（AR exporter 已移除，统一走 stdlib logging）。"""
    global logger
    logger = _StdLogger(server_info.server_name or "mf-model")


def get_logger():
    return _ensure_logger()


def shutdown_log_provider(*args, **kwargs):
    global logger
    if logger is not None:
        logger.shutdown()
