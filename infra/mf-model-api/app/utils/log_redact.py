"""Log redaction: troubleshooting needs the target URL, model, and request size, not credentials
or the user's question.

When calling third-party models, request headers contain the provider api_key, and the request body contains the
user's full conversation content. If both are written into error logs as-is, they leave this service through the log
collection pipeline, whose read-permission model is separate from credential management and business-data
management (#636).

Therefore, request context written to logs must pass through this module:
``safe_headers()`` redacts headers, and ``request_digest()`` compresses the request body into a content-free digest.
"""

_SENSITIVE_HEADERS = (
    "authorization", "api-key", "x-api-key", "apikey",
    "secret-key", "x-secret-key", "cookie", "proxy-authorization",
)

# Prefix length retained by masking: enough to identify which key it is, not enough to use it.
_KEEP_PREFIX = 4


def mask_secret(value, keep=_KEEP_PREFIX):
    """Mask credentials while preserving identifiable prefixes and schemes such as Bearer."""
    if not isinstance(value, str) or not value:
        return "***"
    scheme, _, rest = value.partition(" ")
    if rest and scheme.lower() in ("bearer", "basic", "token"):
        return f"{scheme} {mask_secret(rest, keep)}"
    if len(value) <= keep:
        return "***"
    return f"{value[:keep]}***({len(value)})"


def safe_headers(headers):
    """Return a redacted header copy. Sensitive values are masked, while other headers remain unchanged."""
    if not isinstance(headers, dict):
        return {}
    return {
        k: (mask_secret(v) if str(k).lower() in _SENSITIVE_HEADERS else v)
        for k, v in headers.items()
    }


def safe_url(url):
    """Redact a URL by keeping only scheme, host, and path, and removing the entire query.

    This service has precedent for credentials in URLs, such as Baidu OAuth `client_id`/`client_secret` and
    `?access_token=`. `OtherClient.api_url` is freely entered by administrators in f_model_config, so it cannot be
    assumed to contain no key. Troubleshooting needs the target host and path.
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
    """Build a request-body digest that is enough for troubleshooting and contains no user content.

    Deliberately exclude ``content`` from ``messages`` because it is customer business data. To reproduce an issue,
    use the model, parameters, and size here together with the caller's own trace.
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
    """Build a digest when only messages are available, used by controller-side exception paths."""
    return request_digest({"messages": messages})
