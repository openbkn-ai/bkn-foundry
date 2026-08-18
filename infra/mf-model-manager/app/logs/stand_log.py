# -*- coding:utf-8 -*-
# @Author  : Cerfly.xie
# @Time    : 2022/12/27 17:30

import time
import logging
from logging.handlers import TimedRotatingFileHandler
import os
import arrow
from fastapi import Request

from app.utils.common import GetCallerInfo, IsInPod

'''
标准日志StandLog类
原 AnyRobot(AR) SamplerLogger 已移除，统一走标准库 logging（控制台 + 文件）。
'''

SYSTEM_LOG = "SystemLog"
BUSINESS_LOG = "BusinessLog"

CREATE = "create"  # Create.
DELETE = "delete"  # Delete.
DOWNLOAD = "download"  # Download.
UPDATE = "update"  # Update.
UPLOAD = "upload"  # Upload.
LOGIN = "login"  # Login.


class Logger(object):
    _info_logger = None

    def stand_log_shutdown(self):
        # AR SamplerLogger has been removed, so there is nothing to close.
        pass

    def __init__(self):
        try:
            print("-----------------------------------INFO级别日志输出工具初始化-----------------------------------")
            self._info_logger = logging.getLogger(__name__)
            self._info_logger.setLevel(logging.DEBUG)

            # Add console output first so logs still reach the console if file handling fails.
            console_handler = logging.StreamHandler()
            console_handler.setFormatter(logging.Formatter(fmt="%(asctime)s %(filename)s %(levelname)s %(message)s"))
            console_handler.setLevel(logging.DEBUG)
            self._info_logger.addHandler(console_handler)

            # File logging is configurable; failure must not affect console logging.
            try:
                default_log_file = (
                    os.path.join(os.environ.get("TEMP", os.getcwd()), "mf_model_factory.log")
                    if os.name == "nt" else "/var/log/mf_model_factory.log"
                )
                log_file = os.environ.get("MF_LOG_FILE", default_log_file)
                log_dir = os.path.dirname(log_file)
                if log_dir and not os.path.exists(log_dir):
                    os.makedirs(log_dir, exist_ok=True)
                file_handler = TimedRotatingFileHandler(log_file, when="D", backupCount=15, encoding='UTF-8')
                file_handler.setFormatter(logging.Formatter(fmt="%(asctime)s %(filename)s %(levelname)s %(message)s"))
                file_handler.setLevel(logging.DEBUG)
                self._info_logger.addHandler(file_handler)
            except Exception as fe:
                print("-----------------------------------file handler init errors :", fe)
        except Exception as e:
            print("-----------------------------------stad_log init errors :", e)

    def __need_print(self, etype):
        # When running in a pod, decide whether system logs should be printed.
        if IsInPod():
            if etype == SYSTEM_LOG:
                need_print = os.getenv("ENABLE_SYSTEM_LOG", "false")
                return need_print == "true"
        return True

    def info(self, log_info, etype=SYSTEM_LOG):
        caller_filename, caller_lineno = GetCallerInfo()
        self._info_logger.info(f"{caller_filename}:{caller_lineno} " + str(log_info))

    def warn(self, log_info, etype=SYSTEM_LOG):
        caller_filename, caller_lineno = GetCallerInfo()
        self._info_logger.warning(f"{caller_filename}:{caller_lineno} " + str(log_info))

    def error(self, log_info, etype=SYSTEM_LOG):
        caller_filename, caller_lineno = GetCallerInfo()
        self._info_logger.error(f"{caller_filename}:{caller_lineno} " + str(log_info))

    def info_log(self, body):
        """Print INFO-level logs for special system logs that do not follow the standard rules."""
        self.info(str(body))

    def debug_log(self, body):
        """Print DEBUG-level logs for special system logs that do not follow the standard rules."""
        if self.__need_print(SYSTEM_LOG):
            caller_filename, caller_lineno = GetCallerInfo()
            self._info_logger.debug(f"{caller_filename}:{caller_lineno} " + str(body))


def get_error_log(message, caller_frame, caller_traceback=""):
    """
        Build the error log payload to print.
        @message: actual content as a string.
        @caller_frame: caller context; use sys._getframe().
        @caller_traceback: caller stack; use traceback.format_exc(). Do not pass it outside an exception handler.
    """
    log_info = {}
    log_info["message"] = message
    log_info["caller"] = caller_frame.f_code.co_filename + ":" + str(caller_frame.f_lineno)
    log_info["stack"] = caller_traceback
    log_info["time"] = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(time.time()))
    return log_info


def get_operation_log(request: Request, operation: str, object_id, target_object: dict, description: str,
                      object_type: str = "kg") -> dict:
    """
        Build the user operation log payload to print.
        @user_name: user name.
        @operation: operation type (CREATE, DELETE, DOWNLOAD, UPDATE, UPLOAD, LOGIN).
        @object_id: target object ID; may also be a list.
        @target_object: operation result object as a dict.
        @description: behavior description. Only pass the concrete action, for example:
            updated knowledge graph {id=3}, result is {name:"Knowledge Graph 2"}.
        @object_type: target object type (knowledge network: kn, knowledge graph: kg,
            data source: ds, lexicon: lexicon, function: function, ontology: otl).
    """
    user_id = request.headers.get("userId")
    user_name = request.headers.get("username")
    agent_type = request.headers.get("User-Agent")
    ip = request.headers.get("X-Forwarded-For")
    agent = {
        "type": agent_type,
        "ip": ip
    }
    operator = {
        "type": "authenticated_user",
        "id": user_id,
        "name": user_name,
        "agent": agent
    }
    object_info = {
        "id": object_id,
        "type": object_type
    }
    now_time = arrow.now().format('YYYY-MM-DD HH:mm:ss')
    description = "用户{id=%s,name=%s}在客户端{ip=%s,type=%s}" % (user_id, user_name, ip, agent_type) + description
    operation_log = {
        "operator": operator,
        "operation": operation,
        "object": object_info,
        "targetObject": target_object,
        "description": description,
        "time": now_time
    }
    return operation_log


StandLogger = Logger()
