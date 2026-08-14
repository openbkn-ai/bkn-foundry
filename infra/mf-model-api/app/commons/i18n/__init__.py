from copy import deepcopy
from typing import Any, Dict, Optional
from . import en_us, zh_cn


def lookup_error_message(code: str, lang: str) -> Optional[Dict[str, Any]]:
    error_messages = {
        "en-us": en_us.error_messages,
        "zh-cn": zh_cn.error_messages,
    }
    return error_messages.get((lang or "").lower(), {}).get(code)


async def get_error_message(code: str, lang: str) -> Dict[str, Any]:
    """Compatibility helper for legacy controllers that await an error envelope."""
    message = lookup_error_message(code, lang)
    if message:
        return deepcopy(message)
    return {"code": code, "description": "", "detail": "", "solution": "", "link": ""}
