from typing import Any, Dict
from fastapi import status
from fastapi.responses import JSONResponse

from app.commons.i18n import lookup_error_message
from app.commons.locale import DEFAULT_LOCALE, localized_error_content


def error_response(
        status_code: int,
        code: str,
        detail: str,
        solution: str = "",
        link: str = "",
        language: str = DEFAULT_LOCALE
) -> JSONResponse:
    """Handle errors response
    Args:
        status_code: HTTP status code
        code: Error code
        detail: Error detail message
        solution: solution suggestion
        link: Reference link
        lang: Language code (en/zh)
    Returns:
        JSONResponse: FastAPI response object with errors structure
    """
    fallback = lookup_error_message("ModelFactory.InternalError", language) or {}
    error_content: Dict[str, str] = {
        "code": code,
        "description": fallback.get("description", ""),
        "solution": solution or fallback.get("solution", ""),
        "detail": detail,
        "link": link
    }
    message = lookup_error_message(code, language)
    if message:
        error_content.update({key: message[key] for key in ("description", "solution") if key in message})
    error_content, _ = localized_error_content(error_content, language)

    return JSONResponse(
        status_code=status_code,
        content=error_content,
        headers={"Content-Language": language},
    )


def correct_response(http_code: int = status.HTTP_200_OK, data: Any = None, ) -> JSONResponse:
    """Handle normal response
    Args:
        data: Response data
        http_code: HTTP status code
    Returns:
        JSONResponse: FastAPI response object
    """
    return JSONResponse(
        status_code=http_code,
        content=data
    )
