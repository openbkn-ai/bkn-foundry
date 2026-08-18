# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Locale resource lookup for bkn-agent error envelopes.

Resource problems degrade, they never abort: a missing key falls back to the
generic internal error and a broken template falls back to the untemplated
text. A translation gap must not turn into a 500 or a failed startup.
"""

import logging
from copy import deepcopy
from typing import Any, Dict, Optional

from app.commons.locale import DEFAULT_LOCALE, ENGLISH_LOCALE, get_effective_locale
from . import en_us, zh_cn

logger = logging.getLogger("bkn-agent")

_CATALOGS = {
    DEFAULT_LOCALE: zh_cn,
    ENGLISH_LOCALE: en_us,
}

FALLBACK_MESSAGE_KEY = "BknAgent.Internal.Unexpected"


def lookup_error_message(message_key: str, locale: str) -> Optional[Dict[str, Any]]:
    catalog = _CATALOGS.get(locale, zh_cn)
    message = catalog.error_messages.get(message_key)
    if message is None and catalog is not zh_cn:
        message = zh_cn.error_messages.get(message_key)
    return deepcopy(message) if message else None


def resource_name(resource: str, locale: str) -> str:
    catalog = _CATALOGS.get(locale, zh_cn)
    return catalog.resource_names.get(resource) or zh_cn.resource_names.get(resource) or resource


def build_error_content(
    message_key: str,
    /,
    *,
    locale: Optional[str] = None,
    code: Optional[str] = None,
    **params: Any,
) -> Dict[str, str]:
    """Render one error envelope in the effective locale.

    The machine-readable ``code`` comes from the catalog entry (or an explicit
    override) and never varies by locale; only the human-readable fields do.
    """
    effective = locale or get_effective_locale()
    message = lookup_error_message(message_key, effective)
    resolved_code = code
    if message is None:
        # A raise site named a key the catalog does not have. Borrow the generic
        # wording so the caller still gets readable text, but keep the requested
        # key as the code: relabelling, say, a 400 as an internal error would
        # corrupt the machine contract to paper over a missing translation.
        logger.warning("[BknAgent] missing locale entry %s for %s", message_key, effective)
        message = lookup_error_message(FALLBACK_MESSAGE_KEY, effective) or {}
        resolved_code = code or message_key

    return {
        "code": resolved_code or message.get("code") or message_key,
        "description": _render(message, "description", params, message_key),
        "detail": _render(message, "detail", params, message_key),
        "solution": message.get("solution", ""),
        "link": "",
    }


def localized_message(message_key: str, /, **params) -> str:
    """Render one standalone message (a warning, not an error envelope).

    Import warnings travel in the response body as plain strings, so they need
    the same catalog treatment as errors without carrying a public error code.
    """
    return build_error_content(message_key, **params)["detail"]


def _render(message: Dict[str, Any], field: str, params: Dict[str, Any], message_key: str) -> str:
    """Format a *_template field, falling back to the plain field on any gap."""
    plain = message.get(field, "")
    template = message.get(f"{field}_template")
    if not template or not params:
        return plain
    try:
        return template.format(**params)
    except (IndexError, KeyError, ValueError) as error:
        logger.warning(
            "[BknAgent] cannot render %s.%s_template: %s", message_key, field, error
        )
        return plain
