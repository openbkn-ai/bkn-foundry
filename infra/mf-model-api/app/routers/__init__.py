from app.routers import llm_router, small_model_router, private_route
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi import Request

from app.logs.stand_log import StandLogger
from app.utils import openai_error

api_version_public_v1 = "/api/mf-model-api/v1"
api_version_private_v1 = "/api/private/mf-model-api/v1"
api_version_health = "/api/v1"

# Errors on OpenAI-compatible routes must use {"error": {...}}. Small-model
# and model-management endpoints retain the Model Factory envelope; applying
# the OpenAI shape globally would break their public contract (#637).
_OPENAI_COMPAT_SUFFIXES = ("/chat/completions",)


def _is_openai_compat(path):
    return any(path.endswith(suffix) for suffix in _OPENAI_COMPAT_SUFFIXES)


def router_init(app):
    app.include_router(
        llm_router.health_route,
        prefix=api_version_health,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )
    app.include_router(
        llm_router.llm_route,
        prefix=api_version_public_v1,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )

    app.include_router(
        small_model_router.small_model_router,
        prefix=api_version_public_v1,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )
    app.include_router(
        private_route.private_route,
        prefix=api_version_private_v1,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )

    @app.exception_handler(RequestValidationError)
    async def exception_handler(request: Request, exc: RequestValidationError):
        # Log only loc/type/msg. Pydantic v1 omits input values, but v2 includes
        # them; logging the complete error list could expose request data (#636).
        StandLogger.warn("request validation failed: %s" % [
            {k: e.get(k) for k in ("loc", "type", "msg")} for e in exc.errors()])
        # Report only the first error because detail is contractually a string.
        for error in exc.errors():
            paramName = ' '.join(map(str, error["loc"][1:]))
            if error["type"] == "value_error.missing":
                content = {"code": "ModelFactory.Router.ParamError.ParamMissing",
                           "description": "Required parameter is missing.",
                           "detail": f"missing parameters: {paramName}",
                           "solution": "Provide the required parameter and try again.",
                           "link": ""}
            else:
                content = {"code": "ModelFactory.Router.ParamError.FormatError",
                           "description": "Request parameter is invalid.",
                           "detail": f"{error.get('msg', '')}",
                           "solution": "Check that the input matches the API documentation.",
                           "link": ""}
            # Use the OpenAI error shape on compatible routes while retaining
            # the stable business code in error.code.
            if _is_openai_compat(request.url.path):
                return JSONResponse(
                    status_code=400,
                    content=openai_error.from_envelope(content, 400))
            return JSONResponse(status_code=400, content=content)

    app.add_exception_handler(RequestValidationError, exception_handler)
