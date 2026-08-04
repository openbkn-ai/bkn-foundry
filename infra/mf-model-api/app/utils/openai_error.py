"""OpenAI 兼容面（/v1/chat/completions）的错误契约。

该路由对外声明为 OpenAI 兼容，客户端（@ai-sdk/openai-compatible、openai-python、
LangChain）解析响应时用的是 ``union(chunkSchema, errorSchema)``：要么顶层有
``choices``，要么顶层有 ``error``。模型工厂自家的 envelope
（``{code, description, detail, solution, link}``）两边都不匹配，客户端会抛
TypeValidationError 并把整个原始 body 冒给终端用户（#620）。

所以这个面上所有失败出口——SSE 帧和 JSON body——都必须是
``{"error": {"message", "type", "param", "code"}}``；上游本来就给了合规 error
体的，原样透传，不再套一层。
"""

import json

DEFAULT_ERROR_TYPE = "server_error"

# 上游这些状态码属于瞬态，值得退避重试；其余（4xx 参数/鉴权类）重试没有意义
RETRYABLE_STATUS = (429, 502, 503, 504)

# 上游状态码 -> 本服务对外状态码。4xx 语义由上游决定，原样透传；
# 5xx 一律收敛成 503（对调用方而言就是「网关后面的模型暂时不可用」）
_PASSTHROUGH_STATUS = (400, 401, 403, 404, 408, 413, 422, 429)

_TYPE_BY_STATUS = {
    400: "invalid_request_error",
    401: "authentication_error",
    403: "permission_error",
    404: "not_found_error",
    408: "timeout_error",
    413: "invalid_request_error",
    422: "invalid_request_error",
    429: "rate_limit_exceeded",
    500: "server_error",
    502: "service_unavailable_error",
    503: "service_unavailable_error",
    504: "timeout_error",
}


def error_type_for_status(status):
    """上游状态码推 OpenAI error type；status 为空表示压根没连上。"""
    if status is None:
        return "api_connection_error"
    if status in _TYPE_BY_STATUS:
        return _TYPE_BY_STATUS[status]
    if 400 <= status < 500:
        return "invalid_request_error"
    if status >= 500:
        return "server_error"
    return DEFAULT_ERROR_TYPE


def http_status_for(status):
    """上游状态码映射成本服务的响应状态码。"""
    if status is None:
        return 502
    if status in _PASSTHROUGH_STATUS:
        return status
    if 400 <= status < 500:
        return 400
    if status >= 500:
        return 503
    return 502


def is_retryable(status):
    return status in RETRYABLE_STATUS


def build_error(message, *, error_type=DEFAULT_ERROR_TYPE,
                code=None, param=None):
    """造一个 OpenAI 形状的错误体。"""
    return {
        "error": {
            "message": message if isinstance(message, str) else str(message),
            "type": error_type,
            "param": param,
            "code": code,
        }
    }


def _loads(payload):
    """上游 body 可能是 str/bytes/dict，统一成 dict；解不出来返回 None。"""
    if isinstance(payload, dict):
        return payload
    if isinstance(payload, (bytes, bytearray)):
        try:
            payload = payload.decode("utf-8", errors="replace")
        except Exception:
            return None
    if not isinstance(payload, str):
        return None
    try:
        parsed = json.loads(payload)
    except (TypeError, ValueError):
        return None
    return parsed if isinstance(parsed, dict) else None


def from_upstream(payload, status=None, fallback="模型服务调用失败"):
    """把上游错误 body 归一成 OpenAI 错误体。

    上游已经是 ``{"error": {...}}`` 的，补齐缺省字段后原样透传——那本来就是
    调用方要的东西，再包一层只会让人 JSON.parse 两次。
    """
    error_type = error_type_for_status(status)
    data = _loads(payload)

    if data is None:
        text = payload if isinstance(payload, str) else None
        message = text.strip() if text and text.strip() else fallback
        return build_error(message, error_type=error_type)

    upstream_error = data.get("error")
    if isinstance(upstream_error, dict) and upstream_error.get("message"):
        return {
            "error": {
                "message": str(upstream_error["message"]),
                "type": upstream_error.get("type") or error_type,
                "param": upstream_error.get("param"),
                "code": upstream_error.get("code", data.get("code")),
            }
        }
    if isinstance(upstream_error, str) and upstream_error.strip():
        return build_error(
            upstream_error, error_type=error_type,
            code=data.get("code") or data.get("error_code"))

    for key in ("message", "detail", "error_msg", "description"):
        value = data.get(key)
        if isinstance(value, str) and value.strip():
            return build_error(
                value, error_type=error_type,
                code=data.get("code") or data.get("error_code"))

    return build_error(
        json.dumps(data, ensure_ascii=False) if data else fallback,
        error_type=error_type, code=data.get("code"))


def from_envelope(envelope, status):
    """把模型工厂自家的 envelope 翻成 OpenAI 错误体。

    原来的 ``code``（如 ``ModelFactory.ExternalSmallModel.Used.NameNotExist``）
    落到 OpenAI 的 ``code`` 字段上，机器可读的身份不丢；``solution``/``link``
    这类只对模型工厂控制台有意义的字段不进兼容面。
    """
    if not isinstance(envelope, dict):
        return build_error(str(envelope),
                           error_type=error_type_for_status(status))
    message = ""
    for key in ("detail", "description"):
        value = envelope.get(key)
        if isinstance(value, str) and value.strip():
            message = value
            break
    return build_error(message or "模型服务调用失败",
                       error_type=error_type_for_status(status),
                       code=envelope.get("code"))


def from_exception(exc, message=None):
    """连不上上游（超时/DNS/TLS）时的错误体。"""
    return build_error(message or str(exc) or "模型服务连接失败",
                       error_type="api_connection_error")


def is_error(payload):
    """判断一个已解析的响应体是不是错误体。"""
    return isinstance(payload, dict) and isinstance(payload.get("error"), dict)


# OpenAI 错误体本身不带 HTTP 状态码，而上游状态码只在 llm_utils 的 aiohttp 层可见。
# 这两个私有键是把它带到 controller 出口的通道，序列化成响应前一定被 pop 掉。
_HTTP_STATUS_KEY = "_http_status"
_RETRY_AFTER_KEY = "_retry_after"


def with_http_status(error_body, upstream_status, retry_after=None):
    error_body[_HTTP_STATUS_KEY] = http_status_for(upstream_status)
    if retry_after is not None:
        error_body[_RETRY_AFTER_KEY] = retry_after
    return error_body


def pop_http_status(error_body, default=502):
    if not isinstance(error_body, dict):
        return default
    return error_body.pop(_HTTP_STATUS_KEY, default)


def pop_retry_after(error_body):
    if not isinstance(error_body, dict):
        return None
    return error_body.pop(_RETRY_AFTER_KEY, None)


def error_frame(error_body):
    """错误体 -> SSE data 帧载荷。"""
    return json.dumps(error_body, ensure_ascii=False)


def is_error_frame(chunk):
    """SSE 帧是不是错误帧（trace 收尾判定用）。"""
    if isinstance(chunk, (bytes, bytearray)):
        try:
            chunk = chunk.decode("utf-8", errors="ignore")
        except Exception:
            return False
    if isinstance(chunk, dict):
        return is_error(chunk)
    if not isinstance(chunk, str):
        return False
    stripped = chunk.strip()
    if not stripped.startswith("{"):
        return False
    return is_error(_loads(stripped))


def retry_after_seconds(status, response_headers=None):
    """上游给了 Retry-After 就跟随；没给则对限流/不可用类给个保守默认值。"""
    if response_headers:
        raw = (response_headers.get("Retry-After")
               or response_headers.get("retry-after"))
        if raw:
            try:
                value = int(float(str(raw).strip()))
                if value >= 0:
                    return value
            except (TypeError, ValueError):
                pass
    if status in RETRYABLE_STATUS:
        return 5
    return None
