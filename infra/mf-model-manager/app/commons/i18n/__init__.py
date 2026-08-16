from copy import deepcopy
from typing import Optional

from app.commons.locale import DEFAULT_LOCALE, ENGLISH_LOCALE
from . import en_us, zh_cn


def lookup_error_message(code: str, locale: str) -> Optional[dict]:
    error_messages = {
        ENGLISH_LOCALE: en_us.error_messages,
        DEFAULT_LOCALE: zh_cn.error_messages,
    }
    message = error_messages.get(locale, zh_cn.error_messages).get(code)
    return deepcopy(message) if message else None


async def get_error_message(code: str, lang: str) -> dict:
    """Compatibility wrapper for existing async controller call sites."""
    message = lookup_error_message(code, lang)
    if message:
        message.pop("detail_template", None)
        message.pop("detail_template_plural", None)
        return {**message, "code": code, "link": message.get("link", "")}
    fallback = lookup_error_message("ModelFactory.InternalError", lang) or {}
    return {"code": code, **fallback, "link": ""}
