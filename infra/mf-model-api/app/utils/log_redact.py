"""日志脱敏：排障需要的是「打了哪个 url、哪个模型、多大的请求」，不是凭据本身，
也不是用户问了什么。

调用第三方模型时，请求头里带的是**供应商 api_key**，请求体里带的是**用户的完整
对话内容**。这两样一旦整体拼进 error 日志，就顺着日志采集链路离开了本服务——而
日志的读权限模型跟凭据管理、业务数据管理都不是一套（#636）。

所以往日志里放请求上下文时，一律经这里过一道：
``safe_headers()`` 给头脱敏，``request_digest()`` 把请求体压成不含内容的摘要。
"""

_SENSITIVE_HEADERS = (
    "authorization", "api-key", "x-api-key", "apikey",
    "secret-key", "x-secret-key", "cookie", "proxy-authorization",
)

# 掩码保留的前缀长度：够认出「是哪一把 key」，不够拿去用
_KEEP_PREFIX = 4


def mask_secret(value, keep=_KEEP_PREFIX):
    """凭据掩码。保留可辨识的头部，其余抹掉，并保留 Bearer 之类的方案前缀。"""
    if not isinstance(value, str) or not value:
        return "***"
    scheme, _, rest = value.partition(" ")
    if rest and scheme.lower() in ("bearer", "basic", "token"):
        return f"{scheme} {mask_secret(rest, keep)}"
    if len(value) <= keep:
        return "***"
    return f"{value[:keep]}***({len(value)})"


def safe_headers(headers):
    """请求头脱敏副本。敏感项只留掩码，其余原样——排障还得看 Content-Type。"""
    if not isinstance(headers, dict):
        return {}
    return {
        k: (mask_secret(v) if str(k).lower() in _SENSITIVE_HEADERS else v)
        for k, v in headers.items()
    }


def safe_url(url):
    """URL 脱敏：只留 scheme+host+path，query 一律抹掉。

    本服务里 URL 拼凭据是有先例的（百度 oauth 的 `client_id`/`client_secret`、
    `?access_token=`），而 `OtherClient.api_url` 是管理员在 f_model_config 里自由
    填的，无法假设里面没有 key。排障要的是「打的哪个 host、哪个 path」。
    """
    if not isinstance(url, str) or not url:
        return ""
    base, sep, _query = url.partition("?")
    return f"{base}?***" if sep else base


def _message_chars(messages):
    total = 0
    for m in messages:
        if isinstance(m, dict):
            content = m.get("content")
            if isinstance(content, str):
                total += len(content)
    return total


def request_digest(params):
    """请求体摘要：够定位问题，不含任何用户内容。

    刻意不收 ``messages`` 里的 ``content``——那是客户的业务数据。要复现问题用
    这里的 model / 参数 / 规模，配合调用方自己的 trace 即可。
    """
    if not isinstance(params, dict):
        return {}
    messages = params.get("messages")
    messages = messages if isinstance(messages, list) else []
    digest = {
        "model": params.get("model"),
        "stream": params.get("stream"),
        "message_count": len(messages),
        "message_chars": _message_chars(messages),
        "roles": [m.get("role") for m in messages if isinstance(m, dict)],
    }
    for key in ("max_tokens", "temperature", "top_p", "top_k",
                "frequency_penalty", "presence_penalty"):
        if key in params:
            digest[key] = params[key]
    if params.get("tools"):
        digest["tool_count"] = len(params["tools"])
    if params.get("response_format"):
        digest["response_format"] = True
    return digest


def messages_digest(messages):
    """只有 messages 在手时的摘要（controller 侧的异常分支用）。"""
    return request_digest({"messages": messages})
