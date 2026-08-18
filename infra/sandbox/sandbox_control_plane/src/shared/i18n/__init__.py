# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Locale resource lookup for client-facing messages.

Resource problems degrade, they never abort: an unknown key falls back to the
key itself and a broken template falls back to the untemplated wording. A
translation gap must not turn into a 500 or a failed startup.
"""

import logging
from typing import Any, Dict, Optional

from src.shared.locale import (
    AMERICAN_ENGLISH,
    DEFAULT_LOCALE,
    SIMPLIFIED_CHINESE,
    get_effective_locale,
)
from . import en_us, zh_cn

logger = logging.getLogger(__name__)

_CATALOGS = {
    SIMPLIFIED_CHINESE: zh_cn,
    AMERICAN_ENGLISH: en_us,
}
_FALLBACK_CATALOG = _CATALOGS[DEFAULT_LOCALE]


def lookup(message_key: str, locale: str) -> Optional[Dict[str, Any]]:
    catalog = _CATALOGS.get(locale, _FALLBACK_CATALOG)
    entry = catalog.error_messages.get(message_key)
    if entry is None and catalog is not _FALLBACK_CATALOG:
        entry = _FALLBACK_CATALOG.error_messages.get(message_key)
    return dict(entry) if entry else None


def message(message_key: str, /, locale: Optional[str] = None, **params: Any) -> str:
    """Render one client-facing message in the effective locale.

    Rendering happens at raise time rather than in a response-body rewrite,
    because the domain errors are re-wrapped as ``str(e)`` on their way through
    the REST layer; the text has to be final by then.
    """
    effective = locale or get_effective_locale()
    entry = lookup(message_key, effective)
    if entry is None:
        logger.warning("missing locale entry %s for %s", message_key, effective)
        return message_key

    plain = entry.get("message", "")
    template = entry.get("message_template")
    if not template:
        return plain
    try:
        return template.format(**params)
    except (IndexError, KeyError, ValueError) as error:
        logger.warning("cannot render %s.message_template: %s", message_key, error)
        return plain
