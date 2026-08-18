# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Request-scoped language negotiation for bkn-agent.

The platform P0 contract is HTTP ``Accept-Language`` only; the retired private
``X-Language`` header is never read here. The effective locale is frozen once
per request by the middleware in ``app/main.py`` and every localized string is
resolved against that value.
"""

from contextvars import ContextVar, Token
from typing import Dict, List, Optional, Tuple

DEFAULT_LOCALE = "zh-CN"
ENGLISH_LOCALE = "en-US"
SUPPORTED_LOCALES = (DEFAULT_LOCALE, ENGLISH_LOCALE)

ACCEPT_LANGUAGE_HEADER = "accept-language"
CONTENT_LANGUAGE_HEADER = "content-language"

_effective_locale: ContextVar[str] = ContextVar("effective_locale", default=DEFAULT_LOCALE)


def resolve_accept_language(header: Optional[str]) -> str:
    """Resolve an Accept-Language header using the platform P0 rules.

    Follows RFC 9110 for the header syntax and RFC 4647 lookup for matching:
    ``q=0`` rejects a candidate outright, malformed entries are ignored rather
    than failing the request, and ``*`` only selects among supported locales.
    ``zh-TW`` / ``zh-Hant`` / ``zh-HK`` are deliberately not matched to
    ``zh-CN``; they fall through to the service default like any other
    unsupported tag.
    """
    ranges = _parse_accept_language(header or "")
    if not ranges:
        return DEFAULT_LOCALE

    explicit = {locale: value for locale, value in ranges.items() if locale != "*"}
    wildcard = ranges.get("*")

    candidates: List[Tuple[float, int, str]] = []
    for locale in SUPPORTED_LOCALES:
        candidate = explicit.get(locale)
        if candidate is not None:
            if candidate[0] > 0:
                candidates.append((candidate[0], candidate[1], locale))
        elif wildcard is not None and wildcard[0] > 0:
            candidates.append((wildcard[0], wildcard[1], locale))

    if candidates:
        candidates.sort(key=lambda item: (-item[0], item[1]))
        return candidates[0][2]

    # Every supported locale was either absent or explicitly rejected. Prefer the
    # service default, but never return a locale the client rejected with q=0.
    for locale in SUPPORTED_LOCALES:
        candidate = explicit.get(locale, wildcard)
        if candidate is None or candidate[0] > 0:
            return locale
    return DEFAULT_LOCALE


def get_effective_locale() -> str:
    """Return the locale frozen for the current request."""
    return _effective_locale.get()


def set_effective_locale(locale: str) -> Token:
    return _effective_locale.set(locale if locale in SUPPORTED_LOCALES else DEFAULT_LOCALE)


def reset_effective_locale(token: Token) -> None:
    _effective_locale.reset(token)


def internal_request_headers(headers: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    """Build downstream headers carrying a single normalized Accept-Language.

    The original multi-candidate client header is deliberately not forwarded: a
    downstream service must resolve the same locale this request already froze.
    """
    result = dict(headers or {})
    result["Accept-Language"] = get_effective_locale()
    return result


def is_business_api_path(path: str) -> bool:
    return path.startswith("/api/bkn-agent/v1/")


def merge_cache_control(value: str) -> str:
    """Keep an authenticated response out of shared caches without clobbering it."""
    directives = [directive.strip() for directive in value.split(",") if directive.strip()]
    result: List[str] = []
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
        if not language or quality is None:
            continue
        normalized = _normalize_language(language)
        if normalized is None:
            continue
        previous = merged.get(normalized)
        if previous is None or quality > previous[0]:
            merged[normalized] = (quality, position)
    return merged


def _parse_quality(params: List[str]) -> Optional[float]:
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
