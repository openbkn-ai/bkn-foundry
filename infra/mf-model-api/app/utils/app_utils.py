import asyncio
import json
import os

import aiohttp
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from starlette.middleware.base import BaseHTTPMiddleware

from app.commons.errors import UnauthorizedError, HydraServiceError, BknSafeServiceError
from app.core.config import base_config, server_info, observability_config
from app.logs import log_init, sys_log
from app.mydb.ConnectUtil import get_redis_util
from app.routers import router_init
from app.utils.comment_utils import write_log
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
    # 在应用启动时调用
    try:
        await get_redis_util()
    except Exception as e:
        raise e
    # 初始化可观测模块
    init_observability(server_info, observability_config)


async def shutdown_event():
    await write_log(msg='系统关闭')
    # 关闭可观测模块
    shutdown_observability()


# 用户自助签发的 AppKey 前缀(bkn-safe 签发),与 bkn-safe auth.KeyPrefix 保持一致
APP_KEY_PREFIX = "bak_"


async def _verify_app_key(token):
    """AppKey(bak_ 前缀)走 bkn-safe 内部校验接口 /api/safe/v1/api-keys/introspect,
    响应形如 OAuth2 introspection:任何失败均为 200 {active:false}。
    校验通过返回 (user_id, role);无效或 BKN_SAFE_URL 未配置(fail-closed)返回错误响应。"""
    bkn_safe_url = os.getenv("BKN_SAFE_URL", "")
    if not bkn_safe_url:
        return JSONResponse(
            status_code=401,
            content=UnauthorizedError
        )
    url = f"{bkn_safe_url}/api/safe/v1/api-keys/introspect"
    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(url, json={"token": token}) as response:
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
    # bkn-safe account_type: "app"=应用账户,其余按用户处理,与 hydra 路径的 role 口径一致
    role = "app" if result.get("account_type", "") == "app" else "user"
    return user_id, role


async def auth_middleware(request: Request, call_next):
    path = request.url.path
    if path.startswith("/api/v1/health"):
        pass
    elif path.startswith("/api/private"):
        pass
    elif not base_config.AUTH_ENABLED:
        # 权限控制关闭：跳过 token 校验，注入匿名用户 ID 保证审计日志有值
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
        # 凭据二选一:bak_ 前缀的 AppKey 交给 bkn-safe 校验,其余 bearer token 走 hydra 内省
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
                async with session.post(hydra_url, data=payload) as response:
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
        if content_length and int(content_length) > 10 * 1024 * 1024:  # 10M限制
            return JSONResponse(
                status_code=413,
                content={"detail": "Payload too large"}
            )
        return await call_next(request)


def create_app():
    app = FastAPI(title="My API",
                  description="",
                  version="1.0.0",
                  on_startup=[start_event],
                  on_shutdown=[shutdown_event])

    # 添加请求体大小检查中间件
    # app.add_middleware(RequestSizeMiddleware)
    # 添加鉴权中间件
    app.add_middleware(BaseHTTPMiddleware, dispatch=auth_middleware)

    # 初始化日志
    log_init()
    # 加载配置
    conf_init(app)
    # 初始化路由配置
    router_init(app)
    return app
