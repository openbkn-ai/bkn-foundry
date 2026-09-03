import asyncio
import json
import os

import aiohttp
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, PlainTextResponse
from fastapi.openapi.utils import get_openapi
from starlette.middleware.base import BaseHTTPMiddleware

from app.commons.errors import UnauthorizedError, HydraServiceError, BknSafeServiceError
from app.commons.locale import (
    LocaleResponseMiddleware,
    get_effective_locale,
    internal_request_headers,
    is_authenticated_public_api_path,
    is_business_api_path,
    is_openai_compat_path,
    localized_error_content,
)
from app.core.config import (base_config, observability_config, server_info,
                            validate_authz_config)
from app.logs import log_init, sys_log
from app.mydb.ConnectUtil import get_redis_util
from app.routers import router_init
from app.utils.comment_utils import write_log
from app.utils import openai_error
from app.utils.observability.observability import init_observability, shutdown_observability


def conf_init(app):
    import os
    environment = os.getenv('ENVIRONMENT', 'development')
    sys_log.info(msg=f'Start app with {environment} environment')
    if environment == 'production':
        app.docs_url = None
        app.redoc_url = None
        app.debug = False


async def start_event():
    await write_log(msg='系统启动')
    # Initialize required infrastructure when the application starts.
    try:
        await get_redis_util()
    except Exception as e:
        raise e
    # Initialize observability integrations.
    init_observability(server_info, observability_config)


async def shutdown_event():
    await write_log(msg='系统关闭')
    # Shut down observability integrations.
    shutdown_observability()


# Prefix for self-service AppKeys issued by bkn-safe. Keep this aligned with auth.KeyPrefix.
APP_KEY_PREFIX = "bak_"


async def _verify_app_key(token):
    """Validate a bak_ AppKey through bkn-safe's introspection endpoint.

    The endpoint follows OAuth2 introspection semantics and represents every
    validation failure as HTTP 200 with ``active: false``. A valid key returns
    ``(user_id, role)``; an invalid key or missing BKN_SAFE_URL fails closed.
    """
    bkn_safe_url = os.getenv("BKN_SAFE_URL", "")
    if not bkn_safe_url:
        return JSONResponse(
            status_code=401,
            content=UnauthorizedError
        )
    url = f"{bkn_safe_url}/api/safe/v1/api-keys/introspect"
    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(
                    url,
                    json={"token": token},
                    headers=internal_request_headers()) as response:
                if response.status != 200:
                    error_dict = BknSafeServiceError.copy()
                    error_dict["detail"] = await response.text()
                    return JSONResponse(
                        status_code=400,
                        content=error_dict
                    )
                result = json.loads(await response.text())
    except Exception:
        return JSONResponse(
            status_code=400,
            content=BknSafeServiceError
        )
    if not result.get("active", False):
        return JSONResponse(
            status_code=401,
            content=UnauthorizedError
        )
    user_id = result.get("sub", "")
    # Treat account_type=app as an application identity; all other values are users.
    role = "app" if result.get("account_type", "") == "app" else "user"
    return user_id, role


async def auth_middleware(request: Request, call_next):
    path = request.url.path
    if path.startswith("/api/v1/health"):
        pass
    elif path.startswith("/api/private"):
        pass
    elif not base_config.AUTH_ENABLED:
        # With authorization disabled, inject an anonymous identity for audit correlation.
        user_id = request.headers.get("x-account-id", base_config.ANONYMOUS_USER_ID)
        request.scope['headers'].append((b"x-account-id", user_id.encode()))
        request.scope['headers'].append((b"x-account-type", b"user"))
    else:
        auth_header = request.headers.get("Authorization")
        if not auth_header or not auth_header.startswith("Bearer "):
            return JSONResponse(
                status_code=401,
                content=UnauthorizedError
            )
        token = auth_header[7:]
        # Validate bak_ AppKeys with bkn-safe and all other bearer tokens with Hydra.
        if token.startswith(APP_KEY_PREFIX):
            verified = await _verify_app_key(token)
            if isinstance(verified, JSONResponse):
                return verified
            user_id, role = verified
            request.scope['headers'].append((b"x-account-id", user_id.encode()))
            request.scope['headers'].append((b"x-account-type", role.encode()))
            response = await call_next(request)
            return response
        hydra_url = f"http://{base_config.OAUTHADMINHOST}:{base_config.OAUTHADMINPORT}/admin/oauth2/introspect"
        async with aiohttp.ClientSession() as session:
            try:
                payload = {"token": token}
                async with session.post(
                        hydra_url,
                        data=payload,
                        headers=internal_request_headers()) as response:
                    if response.status != 200:
                        error_dict = HydraServiceError.copy()
                        error_dict["detail"] = await response.text()
                        return JSONResponse(
                            status_code=400,
                            content=error_dict
                        )
                    else:
                        res = await response.text()
                        result = json.loads(res)
                        activate = result.get("active", False)
                        user_id = result.get("sub", "")
                        client_id = result.get("client_id", "")
                        role = "user" if client_id != user_id else "app"
                    if activate:
                        request.scope['headers'].append((b"x-account-id", user_id.encode()))
                        request.scope['headers'].append((b"x-account-type", role.encode()))
                    else:
                        return JSONResponse(
                            status_code=401,
                            content=UnauthorizedError
                        )
            except Exception as e:
                return JSONResponse(
                    status_code=400,
                    content=HydraServiceError
                )

    response = await call_next(request)
    return response


class RequestSizeMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        content_length = request.headers.get('content-length')
        if content_length and int(content_length) > 10 * 1024 * 1024:  # 10 MiB limit
            return JSONResponse(
                status_code=413,
                content={"detail": "Payload too large"}
            )
        return await call_next(request)


async def unhandled_exception_handler(request: Request, exc: Exception):
    """Keep the P0 locale/cache contract when FastAPI handles an unexpected 500."""
    if not is_business_api_path(request.url.path):
        return PlainTextResponse("Internal Server Error", status_code=500)
    locale = getattr(request.state, "effective_locale", get_effective_locale())
    content, _ = localized_error_content({
        "code": "ModelFactory.InternalError",
        "description": "Request failed.",
        "detail": "The request could not be completed.",
        "solution": "Retry later or contact an administrator.",
        "link": "",
    }, locale)
    if is_openai_compat_path(request.url.path):
        content = openai_error.from_envelope(content, 500)
    return JSONResponse(
        status_code=500,
        content=content,
        headers={"Content-Language": locale, "Cache-Control": "private, no-cache"},
    )


def install_locale_openapi(app: FastAPI) -> None:
    """Declare the P0 request and response headers on every business operation."""
    def custom_openapi():
        if app.openapi_schema:
            return app.openapi_schema
        schema = get_openapi(title=app.title, version=app.version, description=app.description, routes=app.routes)
        components = schema.setdefault("components", {})
        parameters = components.setdefault("parameters", {})
        headers = components.setdefault("headers", {})
        parameters["AcceptLanguage"] = {
            "name": "Accept-Language",
            "in": "header",
            "required": False,
            "description": "Preferred response language. Supports zh-CN and en-US.",
            "schema": {"type": "string"},
        }
        headers["ContentLanguage"] = {
            "description": "Language used for localized error text.",
            "schema": {"type": "string", "enum": ["zh-CN", "en-US"]},
        }
        headers["PrivateNoCache"] = {
            "description": "Authenticated business responses are private and require revalidation.",
            "schema": {"type": "string", "example": "private, no-cache"},
        }
        for path, item in schema.get("paths", {}).items():
            if not is_business_api_path(path):
                continue
            for operation in item.values():
                if not isinstance(operation, dict):
                    continue
                operation.setdefault("parameters", []).append({"$ref": "#/components/parameters/AcceptLanguage"})
                responses = operation.setdefault("responses", {})
                if is_authenticated_public_api_path(path):
                    responses.setdefault("401", {"description": "Authentication failed"})
                for status, response in responses.items():
                    if not isinstance(response, dict):
                        continue
                    response_headers = response.setdefault("headers", {})
                    response_headers["Cache-Control"] = {
                        "$ref": "#/components/headers/PrivateNoCache"
                    }
                    if str(status).isdigit() and int(status) >= 400:
                        response_headers["Content-Language"] = {
                            "$ref": "#/components/headers/ContentLanguage"
                        }
        app.openapi_schema = schema
        return app.openapi_schema
    app.openapi = custom_openapi


def create_app():
    app = FastAPI(title="My API",
                  description="",
                  version="1.0.0",
                  on_startup=[start_event],
                  on_shutdown=[shutdown_event])

    # Add request body size validation.
    # app.add_middleware(RequestSizeMiddleware)
    # Add authentication middleware.
    app.add_middleware(BaseHTTPMiddleware, dispatch=auth_middleware)
    # Added after auth so this ASGI middleware is outermost: it also decorates auth failures.
    app.add_middleware(LocaleResponseMiddleware)
    app.add_exception_handler(Exception, unhandled_exception_handler)

    # Initialize logging.
    log_init()
    # Reject an authorization backend that cannot be honoured before the
    # service starts answering requests with it.
    validate_authz_config()
    # Load runtime configuration.
    conf_init(app)
    # Register application routes.
    router_init(app)
    install_locale_openapi(app)
    return app
