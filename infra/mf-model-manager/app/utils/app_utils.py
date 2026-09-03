import asyncio
import json
import aiohttp
from fastapi import FastAPI, Request
from fastapi.openapi.utils import get_openapi
from fastapi.responses import JSONResponse, PlainTextResponse
from starlette.middleware.base import BaseHTTPMiddleware

from app.commons.errors import UnauthorizedError, HydraServiceError
from app.commons.locale import (
    DEFAULT_LOCALE,
    LocaleResponseMiddleware,
    internal_request_headers,
    is_authenticated_public_api_path,
    is_business_api_path,
    localized_error_content,
)
from app.core.config import (base_config, observability_config, server_info,
                            validate_authz_config)
from app.logs import log_init, sys_log
from app.mydb.ConnectUtil import get_redis_util
from app.mydb.pymysql_pool import PymysqlPool
from app.commons.get_user_info import close_directory_session
from app.routers import router_init
from app.utils.comment_utils import write_log
from app.utils.model_monitor import vllm_monitor_task, delete_monitor_data_task, delete_model_quota_data_task
from app.utils.observability.observability import init_observability, shutdown_observability
from app.utils.operation_audit import operation_audit_middleware

from apscheduler.schedulers.background import BackgroundScheduler
import pytz


def conf_init(app):
    import os
    environment = os.getenv('ENVIRONMENT', 'development')
    sys_log.info(msg=f'Start app with {environment} environment')
    if environment == 'production':
        app.docs_url = None
        app.redoc_url = None
        app.debug = False


def start_scheduler():
    scheduler = BackgroundScheduler(timezone=pytz.utc)
    scheduler.add_job(lambda: asyncio.run(vllm_monitor_task()), 'cron', minute='0,10,20,30,40,50', timezone=pytz.utc)
    scheduler.add_job(lambda: asyncio.run(delete_monitor_data_task()), 'cron', hour=4, minute=0, timezone=pytz.utc)
    scheduler.add_job(lambda: asyncio.run(delete_model_quota_data_task()), 'cron', day=2, hour=3, minute=0, timezone=pytz.utc)
    scheduler.start()


async def start_event():
    await write_log(msg='系统启动')
    # Initialize required infrastructure when the application starts.
    try:
        await get_redis_util()
    except Exception as e:
        raise e
    # Initialize observability integrations.
    init_observability(server_info, observability_config)
    start_scheduler()


async def shutdown_event():
    await write_log(msg='系统关闭')
    await close_directory_session()
    PymysqlPool.close_pool()
    # Shut down observability integrations.
    shutdown_observability()


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
        hydra_url = f"http://{base_config.OAUTHADMINHOST}:{base_config.OAUTHADMINPORT}/admin/oauth2/introspect"
        async with aiohttp.ClientSession() as session:
            try:
                payload = {"token": token}
                async with session.post(
                    hydra_url,
                    data=payload,
                    headers=internal_request_headers(),
                ) as response:
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
        if content_length and int(content_length) > 10 * 1024 * 1024:  # 10 MB limit.
            return JSONResponse(
                status_code=413,
                content={"detail": "Payload too large"}
            )
        return await call_next(request)


async def unhandled_exception_handler(request: Request, exc: Exception):
    if not is_business_api_path(request.url.path):
        return PlainTextResponse("Internal Server Error", status_code=500)

    locale = getattr(request.state, "effective_locale", DEFAULT_LOCALE)
    content, _ = localized_error_content(
        {
            "code": "ModelFactory.InternalError",
            "description": "Request failed.",
            "detail": "The service encountered an internal error.",
            "solution": "Retry later or contact an administrator.",
            "link": "",
        },
        locale,
    )
    headers = {"Content-Language": locale}
    headers["Cache-Control"] = "private, no-cache"
    return JSONResponse(status_code=500, content=content, headers=headers)


def configure_locale_openapi(app: FastAPI) -> None:
    """Expose the locale and cache response contract in generated OpenAPI."""
    def custom_openapi():
        if app.openapi_schema:
            return app.openapi_schema

        schema = get_openapi(
            title=app.title,
            version=app.version,
            description=app.description,
            routes=app.routes,
        )
        components = schema.setdefault("components", {})
        parameters = components.setdefault("parameters", {})
        parameters["AcceptLanguage"] = {
            "name": "Accept-Language",
            "in": "header",
            "required": False,
            "description": "Preferred response language. Supports zh-CN and en-US.",
            "schema": {"type": "string", "example": "en-US"},
        }
        headers = components.setdefault("headers", {})
        headers["ContentLanguage"] = {
            "description": "Language used for a localized error response.",
            "schema": {"type": "string", "enum": ["zh-CN", "en-US"]},
        }
        headers["PrivateNoCache"] = {
            "description": "Authenticated response cache policy.",
            "schema": {"type": "string", "example": "private, no-cache"},
        }

        for path, methods in schema.get("paths", {}).items():
            for operation in methods.values():
                if not isinstance(operation, dict):
                    continue
                operation.setdefault("parameters", []).append(
                    {"$ref": "#/components/parameters/AcceptLanguage"}
                )
                responses = operation.setdefault("responses", {})
                if is_authenticated_public_api_path(path):
                    responses.setdefault("401", {"description": "Authentication failed"})
                for status, response in responses.items():
                    if not isinstance(response, dict):
                        continue
                    response_headers = response.setdefault("headers", {})
                    if is_business_api_path(path):
                        response_headers["Cache-Control"] = {
                            "$ref": "#/components/headers/PrivateNoCache"
                        }
                    if (
                            is_business_api_path(path)
                            and str(status).isdigit()
                            and int(status) >= 400):
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
    # Starlette runs the most recently added middleware first.  Authentication
    # must therefore wrap audit collection so audit reads the verified actor
    # injected by auth_middleware after the handler returns.
    app.add_middleware(BaseHTTPMiddleware, dispatch=operation_audit_middleware)
    app.add_middleware(BaseHTTPMiddleware, dispatch=auth_middleware)
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
    configure_locale_openapi(app)
    return app
