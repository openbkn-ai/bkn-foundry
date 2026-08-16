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
    """Resolve the HTTP Accept-Language header using the platform P0 rules."""
    ranges = _parse_accept_language(header)
    if not ranges:
        return DEFAULT_LOCALE

    explicit = {language: candidate for language, candidate in ranges.items() if language != "*"}
    wildcard = ranges.get("*")
    candidates = []
    for language in SUPPORTED_LOCALES:
        candidate = explicit.get(language)
        if candidate is not None:
            if candidate[0] > 0:
                candidates.append((candidate[0], candidate[1], language))
        elif wildcard is not None and wildcard[0] > 0:
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
    """Build headers for a platform-internal request using the frozen locale."""
    result = dict(headers or {})
    result["Accept-Language"] = get_effective_locale()
    return result


def set_effective_locale(locale: str) -> Token:
    return _effective_locale.set(locale if locale in SUPPORTED_LOCALES else DEFAULT_LOCALE)


def reset_effective_locale(token: Token) -> None:
    _effective_locale.reset(token)


def localized_stream_error(code: str, message_key: Optional[str] = None) -> Dict[str, str]:
    """Build a localized SSE error without changing its existing error code."""
    from app.commons.i18n import lookup_error_message

    locale = get_effective_locale()
    message = lookup_error_message(message_key or code, locale) or \
        lookup_error_message("ModelFactory.Stream.InternalError", locale) or {}
    return {
        "code": code,
        "description": message.get("description", ""),
        "detail": message.get("detail", ""),
        "solution": message.get("solution", ""),
        "link": "",
    }


def localized_error_content(
        content: Dict[str, Any], locale: str, status_code: Optional[int] = None) -> Tuple[Dict[str, Any], bool]:
    """Return a localized error payload without changing its machine contract."""
    code = content.get("code")
    if not isinstance(code, str) or not code:
        if status_code in (404, 405) and _is_framework_http_error(content, status_code):
            return _localized_http_error_content(status_code, locale), True
        return content, False

    localized = deepcopy(content)
    from app.commons.i18n import lookup_error_message

    message = lookup_error_message(code, locale)
    if message:
        for field in ("description", "detail", "solution"):
            if field == "detail":
                localized[field] = _localize_detail(message, content.get(field))
            elif field in message:
                localized[field] = message[field]
    elif locale == ENGLISH_LOCALE:
        fallback_messages = lookup_error_message("ModelFactory.InternalError", locale) or {}
        for field, fallback in fallback_messages.items():
            if field in ("description", "detail", "solution") and (
                    not localized.get(field) or _contains_chinese(localized[field])):
                localized[field] = fallback
    return localized, True


def is_business_api_path(path: str) -> bool:
    return path.startswith(("/api/mf-model-manager/v1/", "/api/private/mf-model-manager/v1/"))


def is_authenticated_public_api_path(path: str) -> bool:
    return path.startswith("/api/mf-model-manager/v1/")


def _localize_detail(message: Dict[str, Any], detail: Any) -> Any:
    template = message.get("detail_template")
    parameter_names = _parameter_names_from_detail(detail)
    if template and parameter_names:
        if _contains_multiple_parameter_names(parameter_names):
            template = message.get("detail_template_plural", template)
        return template.format(parameters=parameter_names)
    return message.get("detail") or detail


def _parameter_names_from_detail(detail: Any) -> str:
    if not isinstance(detail, str):
        return ""

    value = detail.strip()
    _, separator, parameter_names = value.partition(":")
    if separator and parameter_names.strip():
        return parameter_names.strip()

    from app.commons.i18n import lookup_error_message

    suffixes = []
    for code in ("ParamMissing", "ParamTypeError"):
        message = lookup_error_message(code, DEFAULT_LOCALE) or {}
        if message.get("description"):
            suffixes.append(message["description"])
    for suffix in suffixes:
        if value.endswith(suffix):
            return value[:-len(suffix)].strip(" :：")
    return ""


def _contains_multiple_parameter_names(parameter_names: str) -> bool:
    return len([name for name in parameter_names.split(",") if name.strip()]) > 1


def _contains_chinese(value: Any) -> bool:
    return isinstance(value, str) and any("\u4e00" <= char <= "\u9fff" for char in value)


def _localized_http_error_content(status_code: int, locale: str) -> Dict[str, Any]:
    from app.commons.i18n import lookup_error_message

    message_key = {
        404: "ModelFactory.HTTP.NotFound",
        405: "ModelFactory.HTTP.MethodNotAllowed",
    }.get(status_code, "ModelFactory.InternalError")
    message = lookup_error_message(message_key, locale) or {}
    return {
        "code": f"HTTP_{status_code}",
        "description": message.get("description", ""),
        "detail": message.get("detail", ""),
        "solution": message.get("solution", ""),
        "link": "",
    }


class LocaleResponseMiddleware:
    """Freeze locale before auth and decorate completed JSON error responses."""

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
                if response_start["status"] < 400:
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
        if not is_business_api_path(path):
            return
        _set_header(
            response_start["headers"],
            "Cache-Control",
            _merge_cache_control(_get_header(response_start["headers"], "cache-control")),
        )

    @staticmethod
    def _localize_error_payload(
            response_start: Dict[str, Any], body: bytearray, locale: str, path: str) -> Optional[bytes]:
        if response_start["status"] < 400:
            return None
        if not is_business_api_path(path):
            return None
        if "application/json" not in _get_header(response_start["headers"], "content-type").lower():
            return None
        try:
            content = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, ValueError):
            return None
        if not isinstance(content, dict):
            return None
        content = _unwrap_structured_error(content)
        localized, is_localized = localized_error_content(content, locale, response_start["status"])
        if not is_localized:
            return None
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


def _is_framework_http_error(content: Dict[str, Any], status_code: int) -> bool:
    return content.get("detail") == {404: "Not Found", 405: "Method Not Allowed"}[status_code]


def _unwrap_structured_error(content: Dict[str, Any]) -> Dict[str, Any]:
    detail = content.get("detail")
    if not isinstance(detail, dict) or not detail.get("code"):
        return content
    unwrapped = dict(content)
    unwrapped.pop("detail")
    unwrapped.update(detail)
    return unwrapped


def _merge_cache_control(value: str) -> str:
    directives = [directive.strip() for directive in value.split(",") if directive.strip()]
    filtered = []
    names = set()
    for directive in directives:
        name = directive.split("=", 1)[0].strip().lower()
        if name == "public":
            continue
        if name not in names:
            filtered.append(directive)
            names.add(name)
    if "private" not in names:
        filtered.append("private")
    if "no-store" not in names and "no-cache" not in names:
        filtered.append("no-cache")
    return ", ".join(filtered)


def _parse_accept_language(header: str) -> Dict[str, Tuple[float, int]]:
    merged: Dict[str, Tuple[float, int]] = {}
    for position, raw_item in enumerate(header.split(",")):
        parts = raw_item.split(";")
        language = parts[0].strip()
        quality = _parse_quality(parts[1:])
        if not language or quality is None:
            continue
        normalized = _normalize_language(language)
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
    if (
        not fraction.isascii()
        or not fraction.isdigit()
        or (value[0] == "1" and any(digit != "0" for digit in fraction))
    ):
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
    if not value or value.startswith("-") or value.endswith("-"):
        return False
    for subtag in value.split("-"):
        if not 1 <= len(subtag) <= 8 or not subtag.isascii() or not subtag.isalnum():
            return False
    return True
