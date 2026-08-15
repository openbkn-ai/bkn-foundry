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
        return message
    return {
        "code": code,
        "description": "Request failed." if lang == ENGLISH_LOCALE else "请求失败。",
        "detail": "",
        "solution": (
            "See the request details or contact an administrator."
            if lang == ENGLISH_LOCALE
            else "请查看请求详情或联系管理员。"
        ),
        "link": "",
    }
