import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

from app.models import ErrorEnvelope
from app import auth, evidence, observability
from app.commons import locale
from app.commons.i18n import build_error_content
from app.observability import setup_otel
from app.routers import agents, chat, impex, prompts, tasks, threads

logger = logging.getLogger("bkn-agent")

API_PREFIX = "/api/bkn-agent/v1"
VERSION = (Path(__file__).resolve().parent.parent / "VERSION").read_text().strip()

# 契约冻结在 docs/api/bkn-agent.yaml（#212）；改 API 先改 spec，再跑
# scripts/export_openapi.py 重新导出——test_contract.py 强制两者一致。
_ERRORS = {
    "4XX": {
        "model": ErrorEnvelope,
        "description": (
            "Platform error envelope (400 parameters / 401 identity / 404 not found / 409 conflict). "
            "The human-readable fields follow the request Accept-Language; code stays stable."
        ),
    }
}


async def _recover_stale_tasks() -> None:
    """启动兜底：把上次进程遗留的 pending/running 任务标 failed（见 dao.recover_stale_tasks）。
    DB 不可用时只告警不阻断启动。"""
    from app import dao
    from app.db import SessionLocal

    try:
        async with SessionLocal() as session:
            n = await dao.recover_stale_tasks(session)
        if n:
            logger.warning("[BknAgent] 启动回收 %s 个悬挂任务（重启中断→failed）", n)
    except Exception as e:
        logger.warning("[BknAgent] 启动回收悬挂任务失败（不阻断启动）：%s", e)


@asynccontextmanager
async def _lifespan(app: FastAPI):
    await _recover_stale_tasks()
    try:
        yield
    finally:
        await evidence.drain_pending()


app = FastAPI(title="bkn-agent", version=VERSION, docs_url=None, redoc_url=None, lifespan=_lifespan)
setup_otel(app)
app.include_router(agents.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(chat.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(tasks.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(prompts.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(threads.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(impex.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)


def _apply_language_headers(response, path: str, effective_locale: str) -> None:
    """Declare the response language only where the body actually carries it.

    Error envelopes and the SSE stream (its error events are localized) get
    Content-Language; a purely machine-readable success body does not. Business
    API responses are authenticated, so they also stay out of shared caches.
    """
    if not locale.is_business_api_path(path):
        return
    response.headers["Cache-Control"] = locale.merge_cache_control(
        response.headers.get("Cache-Control", "")
    )
    is_stream = response.headers.get("content-type", "").startswith("text/event-stream")
    if response.status_code >= 400 or is_stream:
        response.headers["Content-Language"] = effective_locale


@app.middleware("http")
async def bkn_trace_context_middleware(request: Request, call_next):
    ctx = observability.build_context(request.headers)
    request.state.bkn_trace_context = ctx
    token = observability.set_context(ctx)
    # 令牌在这里收，不在 get_account 里收：FastAPI 把 **同步** 依赖丢进线程池跑，
    # 在那里 set 的 ContextVar 回不到请求协程，caller_token() 永远是 None
    # （VM 实测踩到：工具没挂，模型改口编了个工具调用当答案）。中间件与端点同
    # 上下文链，这里 set 才可见。顺带覆盖不走 get_account 的路由。
    auth_token = auth.set_caller_token(request.headers.get("authorization"))
    # Freeze the locale here for the same reason the caller token is frozen
    # here: this is the outermost point that shares a context with both the
    # endpoint and the exception handlers that render the error envelope.
    effective_locale = locale.resolve_accept_language(
        request.headers.get(locale.ACCEPT_LANGUAGE_HEADER)
    )
    locale_token = locale.set_effective_locale(effective_locale)
    try:
        response = await call_next(request)
    finally:
        locale.reset_effective_locale(locale_token)
        auth.reset_caller_token(auth_token)
        observability.reset_context(token)
    _apply_language_headers(response, request.url.path, effective_locale)
    for key, value in {
        observability.TRACE_ID_HEADER: ctx.trace_id,
        observability.REQUEST_ID_HEADER: ctx.request_id,
        observability.LEGACY_REQUEST_ID_HEADER: ctx.request_id,
        "traceparent": ctx.traceparent,
    }.items():
        response.headers[key] = value
    return response


@app.get("/api/v1/health")
async def health():
    return {"status": "ok"}


@app.exception_handler(RequestValidationError)
async def validation_handler(request: Request, exc: RequestValidationError):
    detail = "; ".join(
        f"{'.'.join(str(p) for p in e['loc'][1:])}: {e['msg']}" for e in exc.errors()
    )
    content = build_error_content("BknAgent.ParamError.FormatError")
    # The field-level detail is machine text produced by pydantic; it names
    # request fields and is not translated.
    content["detail"] = detail
    content["trace_id"] = observability.current_trace_id()
    return JSONResponse(
        status_code=400,
        content=content,
        headers=observability.response_headers(),
    )


@app.exception_handler(StarletteHTTPException)
async def http_exception_handler(request: Request, exc: StarletteHTTPException):
    """业务错误（err()/not_found/bad_request 抛的 HTTPException）契约是**顶层扁平**
    ErrorEnvelope。Starlette 默认会包成 {"detail": ...}，与 docs/api/bkn-agent.yaml
    漂移、SDK 解析错位——这里直接把 detail 作为 body 返回。非 dict 的 detail
    （如 404/405 路由默认串）补齐成封套。"""
    detail = exc.detail
    if isinstance(detail, dict) and "code" in detail:
        content = observability.enrich_error(detail)
    else:
        content = build_error_content(
            "BknAgent.Http.Unexpected", code=f"BknAgent.Http.{exc.status_code}"
        )
        # Starlette's own reason phrase ("Not Found") is framework machine text,
        # so it stays in detail while description carries the localized message.
        if detail:
            content["detail"] = str(detail)
        content["trace_id"] = observability.current_trace_id()
    headers = dict(getattr(exc, "headers", None) or {})
    headers.update(observability.response_headers())
    return JSONResponse(status_code=exc.status_code, content=content, headers=headers)


@app.exception_handler(Exception)
async def unhandled_handler(request: Request, exc: Exception):
    """任何未预期异常也走平台错误封套。

    /chat 的组装阶段（工具装载、下游连接）会抛非 HTTPException（如显式 toolbox
    引用拉取失败的 RuntimeError），没有这层兜底就是裸 text/plain 500，破坏冻结
    契约里「4XX/5XX 一律 ErrorEnvelope」的约定，SDK 侧解析直接崩。
    """
    logger.exception("[BknAgent] unhandled error on %s %s", request.method, request.url.path)
    ctx = observability.context_from_request(request)
    content = build_error_content("BknAgent.Internal.Unexpected")
    # The exception type and message are internal diagnostics, kept in English
    # so they stay greppable against the logs; they are not a translated field.
    content["detail"] = f"{type(exc).__name__}: {exc}"
    content["trace_id"] = observability.current_trace_id(ctx)
    return JSONResponse(
        status_code=500,
        content=content,
        headers=observability.response_headers(ctx),
    )
