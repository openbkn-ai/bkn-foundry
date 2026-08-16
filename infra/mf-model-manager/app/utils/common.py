# -*- coding:utf-8 -*-
# @Author: Cerfly.xie
# @Email: Cerfly.xie@aishu.cn
# @CreatDate: 2023/5/22 09:36

import inspect
import os
from typing import Tuple

from app.commons.locale import resolve_accept_language

cur_pwd = os.getcwd()


def GetCallerInfo() -> Tuple[str, int]:
    """ Return the caller's project-relative path and line number. """
    caller_frame = inspect.stack()[2]
    caller_filename = caller_frame.filename.split(cur_pwd)[-1][1:]
    caller_lineno = caller_frame.lineno
    return caller_filename, caller_lineno


def IsInPod() -> bool:
    return 'KUBERNETES_SERVICE_HOST' in os.environ and 'KUBERNETES_SERVICE_PORT' in os.environ


# Number of failures that opens the circuit breaker.
failureThreshold = 10


def GetFailureThreshold() -> int:
    return failureThreshold


def SetFailureThreshold(time: int):
    global failureThreshold
    failureThreshold = time


# Retry interval in seconds after the circuit breaker opens.
recoveryTimeout = 5


def GetRecoveryTimeout() -> int:
    return recoveryTimeout


def SetRecoveryTimeout(time: int):
    global recoveryTimeout
    recoveryTimeout = time


async def get_user_info(request, **kwargs):
    headers = request.headers
    userId = headers.get('x-account-id', "")
    role = headers.get('x-account-type', "")
    language = getattr(request.state, "effective_locale", None)
    if language is None:
        language = resolve_accept_language(headers.get('accept-language', ""))
    return userId, language, role

async def validate_required_params(params_dict, required_params):
    """
    Validate required parameters.

    Args:
        params_dict: Request parameters.
        required_params: Required parameter names.
    Returns:
        Missing parameter names.
    """
    missing_params = []
    for param in required_params:
        if param not in params_dict:
            missing_params.append(param)
    return missing_params
