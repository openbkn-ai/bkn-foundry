"""Error contract for the OpenAI-compatible /v1/chat/completions endpoint.

Clients such as @ai-sdk/openai-compatible, openai-python, and LangChain parse
responses as ``union(chunkSchema, errorSchema)``: the top level must contain
either ``choices`` or ``error``. The Model Factory envelope
``{code, description, detail, solution, link}`` matches neither shape and can
cause clients to expose the raw body through a TypeValidationError (#620).

Every failure path, including SSE frames and JSON bodies, must therefore use
``{"error": {"message", "type", "param", "code"}}``. Preserve a compliant
upstream error object instead of wrapping it again.
"""

import json

DEFAULT_ERROR_TYPE = "server_error"

# Maximum safe length for a plain-text upstream body used as a message.
_MAX_PLAIN_MESSAGE = 500

# Retry transient upstream failures; other 4xx failures are not retryable.
RETRYABLE_STATUS = (429, 502, 503, 504)

# Pass through only 4xx statuses that describe a problem with the caller's
# request. Upstream 401/403/404 statuses describe this service's provider
# credentials or routing, not the caller's identity. Normalize them to 502 so
# they cannot be confused with this service's own authentication and permission
# responses.
_PASSTHROUGH_STATUS = (400, 408, 413, 422, 429)

# Provider authentication and routing failures are dependency failures to callers.
_DEPENDENCY_AUTH_STATUS = (401, 403, 404)

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
    """Map an upstream status to an OpenAI error type; None means no connection."""
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
    """Map an upstream status to the status returned by this service.

    Do not pass through provider 401/403/404 responses. A caller would interpret
    them as its own credential or permission failure, while an operator must fix
    the provider credential. Normalize them to 502 and retain the diagnostic
    category in ``error.type``.
    """
    if status is None:
        return 502
    if status in _PASSTHROUGH_STATUS:
        return status
    if status in _DEPENDENCY_AUTH_STATUS:
        return 502
    if 400 <= status < 500:
        return 400
    if status >= 500:
        return 503
    return 502


def is_retryable(status):
    return status in RETRYABLE_STATUS


def build_error(message, *, error_type=DEFAULT_ERROR_TYPE,
                code=None, param=None):
    """Build an OpenAI-compatible error object."""
    return {
        "error": {
            "message": message if isinstance(message, str) else str(message),
            "type": error_type,
            "param": param,
            "code": code,
        }
    }


def _loads(payload):
    """Decode a str, bytes, or dict upstream body; return None when it is not JSON."""
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


def _localized_owned_message(code):
    from app.commons.locale import platform_error_message

    return platform_error_message(code)


def from_upstream(payload, status=None, fallback=None):
    """Normalize an upstream error body to the OpenAI error shape.

    Preserve an existing ``{"error": {...}}`` object after filling required
    fields. Wrapping it again would require callers to parse JSON twice.
    """
    fallback = fallback or _localized_owned_message("ModelFactory.Stream.ModelConnectionFailed")
    error_type = error_type_for_status(status)
    data = _loads(payload)

    if data is None:
        text = payload.strip() if isinstance(payload, str) else ""
        # A non-JSON body may be an HTML gateway page or a stack trace.
        if not text or text.startswith("<") or len(text) > _MAX_PLAIN_MESSAGE:
            text = fallback
        return build_error(text, error_type=error_type)

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

    # Unknown upstream bodies may contain echoed requests, internal IDs, or
    # gateway node names. Return a fixed localized message and log the raw body
    # only at the call site.
    return build_error(fallback, error_type=error_type, code=data.get("code"))


def from_envelope(envelope, status):
    """Convert a Model Factory envelope to the OpenAI error shape.

    Preserve the stable machine-readable code. Console-specific fields such as
    ``solution`` and ``link`` are intentionally omitted.
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
    return build_error(message or _localized_owned_message("ModelFactory.Stream.ModelConnectionFailed"),
                       error_type=error_type_for_status(status),
                       code=envelope.get("code"))


def from_exception(exc, message=None):
    """Build a safe localized error for an upstream timeout, DNS, or TLS failure."""
    return build_error(message or _localized_owned_message("ModelFactory.Stream.ModelConnectionFailed"),
                       error_type="api_connection_error")


def is_error(payload):
    """Return whether a decoded response uses the OpenAI error shape."""
    return isinstance(payload, dict) and isinstance(payload.get("error"), dict)


# OpenAI error objects do not carry HTTP status. These private keys transport
# status metadata from the aiohttp layer to the controller and are removed
# before serialization.
_HTTP_STATUS_KEY = "_http_status"
_RETRY_AFTER_KEY = "_retry_after"


# Emit Retry-After only for transient failures. A normalized 502 represents a
# provider credential or routing problem and retrying cannot resolve it.
_RETRY_AFTER_STATUS = (429, 503)


def with_http_status(error_body, upstream_status, retry_after=None):
    status = http_status_for(upstream_status)
    error_body[_HTTP_STATUS_KEY] = status
    if retry_after is not None and status in _RETRY_AFTER_STATUS:
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


def public_copy(payload):
    """Copy a payload without private transport metadata."""
    if not isinstance(payload, dict):
        return payload
    return {k: v for k, v in payload.items()
            if k not in (_HTTP_STATUS_KEY, _RETRY_AFTER_KEY)}


def error_frame(error_body):
    """Serialize an error object as an SSE data payload."""
    return json.dumps(error_body, ensure_ascii=False)


def is_error_frame(chunk):
    """Return whether an SSE chunk contains an OpenAI error object."""
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
    """Use upstream Retry-After or a conservative default for transient failures."""
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
