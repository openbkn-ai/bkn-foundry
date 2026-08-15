# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import json
from contextvars import ContextVar, Token
from copy import deepcopy
from typing import Any, Callable, Dict, Optional, Tuple

DEFAULT_LOCALE = "zh-CN"
ENGLISH_LOCALE = "en-US"
SUPPORTED_LOCALES = (DEFAULT_LOCALE, ENGLISH_LOCALE)

_effective_locale: ContextVar[str] = ContextVar("effective_locale", default=DEFAULT_LOCALE)


def resolve_accept_language(header: str) -> str:
    """Resolve Accept-Language using the platform P0 language rules."""
    ranges = _parse_accept_language(header)
    if not ranges:
        return DEFAULT_LOCALE

    explicit = {language: candidate for language, candidate in ranges.items() if language != "*"}
    wildcard = ranges.get("*")
    candidates = []
    for language in SUPPORTED_LOCALES:
        candidate = explicit.get(language)
        if candidate is not None and candidate[0] > 0:
            candidates.append((candidate[0], candidate[1], language))
        elif candidate is None and wildcard is not None and wildcard[0] > 0:
            candidates.append((wildcard[0], wildcard[1], language))
    if candidates:
        candidates.sort(key=lambda item: (-item[0], item[1]))
        return candidates[0][2]

    for language in SUPPORTED_LOCALES:
        candidate = explicit.get(language, wildcard)
        if candidate is None or candidate[0] > 0:
            return language
    return DEFAULT_LOCALE


def get_effective_locale() -> str:
    return _effective_locale.get()


def internal_request_headers(headers: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    """Build platform-internal request headers from the frozen locale."""
    result = dict(headers or {})
    result["Accept-Language"] = get_effective_locale()
    return result


def set_effective_locale(locale: str) -> Token:
    return _effective_locale.set(locale if locale in SUPPORTED_LOCALES else DEFAULT_LOCALE)


def reset_effective_locale(token: Token) -> None:
    _effective_locale.reset(token)


def is_business_api_path(path: str) -> bool:
    return path.startswith(("/api/mf-model-api/v1/", "/api/private/mf-model-api/v1/"))


def is_authenticated_public_api_path(path: str) -> bool:
    return path.startswith("/api/mf-model-api/v1/")


def is_openai_compat_path(path: str) -> bool:
    return path.endswith("/chat/completions") and is_business_api_path(path)


def platform_error_message(code: str) -> str:
    """Return a localized message for an error owned by mf-model-api."""
    from app.commons.i18n import lookup_error_message

    message = lookup_error_message(code, get_effective_locale())
    if not message:
        return "Request failed." if get_effective_locale() == ENGLISH_LOCALE else "请求失败。"
    return message.get("detail") or message.get("description", "")


def platform_openai_error(code: str, error_type: str) -> Dict[str, Any]:
    """Build an OpenAI-compatible error body for an error owned by this service."""
    from app.utils import openai_error

    return openai_error.build_error(
        platform_error_message(code), error_type=error_type, code=code)


def localized_error_content(
        content: Dict[str, Any], locale: str, status_code: Optional[int] = None) -> Tuple[Dict[str, Any], bool]:
    code = content.get("code")
    if not isinstance(code, str) or not code:
        if status_code in (404, 405) and _is_framework_http_error(content, status_code):
            return _localized_framework_http_error(status_code, locale), True
        return content, False

    localized = deepcopy(content)
    from app.commons.i18n import lookup_error_message

    message = lookup_error_message(code, locale)
    if message:
        for field in ("description", "detail", "solution"):
            if field == "detail":
                localized[field] = _localize_detail(message, content.get(field))
            elif field in message and message[field]:
                localized[field] = message[field]
        return localized, True
    if locale == ENGLISH_LOCALE:
        fallbacks = {
            "description": "Request failed.",
            "detail": "The request could not be completed.",
            "solution": "See the request details or contact an administrator.",
        }
        for field, fallback in fallbacks.items():
            if not localized.get(field) or _contains_chinese(localized[field]):
                localized[field] = fallback
        return localized, True
    # Chinese is the service's existing baseline. Preserve its detailed text but
    # still mark the representation language for clients and private caches.
    return localized, True


def _localized_openai_error(content: Dict[str, Any], locale: str) -> Tuple[Dict[str, Any], bool]:
    error = content.get("error")
    if not isinstance(error, dict):
        return content, False
    code = error.get("code")
    if not isinstance(code, str) or not code:
        return content, False

    from app.commons.i18n import lookup_error_message
    if not lookup_error_message(code, locale):
        return content, False

    envelope = {
        "code": code,
        "description": error.get("message", ""),
        "detail": error.get("message", ""),
    }
    localized, changed = localized_error_content(envelope, locale)
    if not changed:
        return content, False
    result = deepcopy(content)
    result["error"]["message"] = localized.get("detail") or localized["description"]
    return result, True


def _contains_chinese(value: Any) -> bool:
    return isinstance(value, str) and any("\u4e00" <= char <= "\u9fff" for char in value)


def _localize_detail(message: Dict[str, str], detail: Any) -> Any:
    template = message.get("detail_template")
    if template and isinstance(detail, str):
        parameter = _parameter_name_from_detail(detail)
        if parameter:
            return template.format(parameter=parameter)
        return message.get("detail") or detail
    return detail if isinstance(detail, str) and detail else message.get("detail")


def _parameter_name_from_detail(detail: str) -> str:
    text = detail.strip()
    if text.endswith(" 参数缺失"):
        return text[:-len(" 参数缺失")].strip()
    _, separator, value = text.partition(":")
    return value.strip() if separator else ""


def _is_framework_http_error(content: Dict[str, Any], status_code: int) -> bool:
    return content.get("detail") == {404: "Not Found", 405: "Method Not Allowed"}[status_code]


def _localized_framework_http_error(status_code: int, locale: str) -> Dict[str, str]:
    messages = {
        404: {
            "zh-CN": ("资源不存在", "请求的资源不存在。", "请检查资源标识后重试。"),
            "en-US": ("Resource not found.", "The requested resource does not exist.",
                      "Check the resource identifier and try again."),
        },
        405: {
            "zh-CN": ("请求方法不被允许", "当前资源不支持该请求方法。", "请检查请求方法后重试。"),
            "en-US": ("Method not allowed.", "The requested resource does not support this HTTP method.",
                      "Check the request method and try again."),
        },
    }
    description, detail, solution = messages[status_code][locale]
    return {
        "code": f"HTTP_{status_code}",
        "description": description,
        "detail": detail,
        "solution": solution,
        "link": "",
    }


class LocaleResponseMiddleware:
    """Freeze locale and localize completed JSON error responses only."""

    def __init__(self, app: Callable) -> None:
        self.app = app

    async def __call__(self, scope: Dict[str, Any], receive: Callable, send: Callable) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return

        headers = {key.decode("latin-1").lower(): value.decode("latin-1") for key, value in scope["headers"]}
        locale = resolve_accept_language(headers.get("accept-language", ""))
        scope.setdefault("state", {})["effective_locale"] = locale
        token = set_effective_locale(locale)
        response_start: Optional[Dict[str, Any]] = None
        body = bytearray()

        async def send_with_locale(message: Dict[str, Any]) -> None:
            nonlocal response_start
            if message["type"] == "http.response.start":
                response_start = message
                if not _is_json_response(message):
                    self._apply_cache_policy(response_start, scope["path"])
                    await send(response_start)
                    response_start = None
                return
            if message["type"] != "http.response.body" or response_start is None:
                await send(message)
                return

            body.extend(message.get("body", b""))
            if message.get("more_body", False):
                if response_start["status"] < 400 or not is_business_api_path(scope["path"]):
                    self._apply_cache_policy(response_start, scope["path"])
                    await send(response_start)
                    response_start = None
                    await send(message)
                return

            self._apply_cache_policy(response_start, scope["path"])
            payload = self._localize_error_payload(response_start, body, locale, scope["path"])
            if payload is not None:
                body.clear()
                body.extend(payload)
            await send(response_start)
            await send({"type": "http.response.body", "body": bytes(body), "more_body": False})
            response_start = None

        try:
            await self.app(scope, receive, send_with_locale)
        finally:
            reset_effective_locale(token)

    @staticmethod
    def _apply_cache_policy(response_start: Dict[str, Any], path: str) -> None:
        if is_business_api_path(path):
            _set_header(
                response_start["headers"],
                "Cache-Control",
                _merge_cache_control(_get_header(response_start["headers"], "cache-control")),
            )

    @staticmethod
    def _localize_error_payload(
            response_start: Dict[str, Any], body: bytearray, locale: str, path: str) -> Optional[bytes]:
        if response_start["status"] < 400 or not is_business_api_path(path):
            return None
        if "application/json" not in _get_header(response_start["headers"], "content-type").lower():
            return None
        try:
            content = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, ValueError):
            return None
        if not isinstance(content, dict):
            return None
        localized, changed = _localized_openai_error(content, locale)
        if not changed:
            localized, changed = localized_error_content(content, locale, response_start["status"])
        if not changed:
            return None
        if is_openai_compat_path(path) and not isinstance(localized.get("error"), dict):
            from app.utils import openai_error
            localized = openai_error.from_envelope(localized, response_start["status"])
        encoded = json.dumps(localized, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        _set_header(response_start["headers"], "Content-Language", locale)
        _set_header(response_start["headers"], "Content-Length", str(len(encoded)))
        return encoded


def _get_header(headers: list[Tuple[bytes, bytes]], name: str) -> str:
    wanted = name.lower().encode("latin-1")
    for key, value in headers:
        if key.lower() == wanted:
            return value.decode("latin-1")
    return ""


def _is_json_response(response_start: Dict[str, Any]) -> bool:
    return "application/json" in _get_header(response_start["headers"], "content-type").lower()


def _set_header(headers: list[Tuple[bytes, bytes]], name: str, value: str) -> None:
    wanted = name.lower().encode("latin-1")
    encoded = value.encode("latin-1")
    for index, (key, _) in enumerate(headers):
        if key.lower() == wanted:
            headers[index] = (key, encoded)
            return
    headers.append((wanted, encoded))


def _merge_cache_control(value: str) -> str:
    directives = [directive.strip() for directive in value.split(",") if directive.strip()]
    result = []
    names = set()
    for directive in directives:
        name = directive.split("=", 1)[0].strip().lower()
        if name == "public" or name in names:
            continue
        result.append(directive)
        names.add(name)
    if "private" not in names:
        result.append("private")
    if "no-store" not in names and "no-cache" not in names:
        result.append("no-cache")
    return ", ".join(result)


def _parse_accept_language(header: str) -> Dict[str, Tuple[float, int]]:
    merged: Dict[str, Tuple[float, int]] = {}
    for position, raw_item in enumerate(header.split(",")):
        parts = raw_item.split(";")
        language = parts[0].strip()
        quality = _parse_quality(parts[1:])
        normalized = _normalize_language(language) if quality is not None else None
        if normalized is None:
            continue
        previous = merged.get(normalized)
        if previous is None or quality > previous[0]:
            merged[normalized] = (quality, position)
    return merged


def _parse_quality(params: list[str]) -> Optional[float]:
    if not params:
        return 1.0
    if len(params) != 1:
        return None
    name, separator, value = params[0].strip().partition("=")
    if separator != "=" or name.strip().lower() != "q":
        return None
    return _parse_qvalue(value.strip())


def _parse_qvalue(value: str) -> Optional[float]:
    if value in ("0", "1"):
        return float(value)
    if len(value) < 3 or len(value) > 5 or value[0] not in "01" or value[1] != ".":
        return None
    fraction = value[2:]
    if not fraction.isascii() or not fraction.isdigit() or (value[0] == "1" and any(char != "0" for char in fraction)):
        return None
    return float(value)


def _normalize_language(value: str) -> Optional[str]:
    normalized = value.strip().lower()
    if normalized == "zh_cn":
        normalized = "zh-cn"
    if normalized == "*":
        return normalized
    if not _valid_language_range(normalized):
        return None
    if normalized in ("zh", "zh-cn", "zh-hans") or normalized.startswith(("zh-cn-", "zh-hans-")):
        return DEFAULT_LOCALE
    if normalized == "en" or normalized.startswith("en-"):
        return ENGLISH_LOCALE
    return None


def _valid_language_range(value: str) -> bool:
    return bool(value) and not value.startswith("-") and not value.endswith("-") and all(
        1 <= len(subtag) <= 8 and subtag.isascii() and subtag.isalnum()
        for subtag in value.split("-")
    )
