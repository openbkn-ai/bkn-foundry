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

# The contract is frozen in docs/api/bkn-agent.yaml (#212). Change the spec
# first, then re-export with scripts/export_openapi.py; test_contract.py
# enforces that the two agree.
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
    """Startup safety net: mark tasks the previous process left in pending or
    running as failed; see dao.recover_stale_tasks. If the database is
    unavailable this only warns and never blocks startup."""
    from app import dao
    from app.db import SessionLocal

    try:
        async with SessionLocal() as session:
            n = await dao.recover_stale_tasks(session)
        if n:
            logger.warning(
                "[BknAgent] recovered %s dangling task(s) at startup "
                "(interrupted by a restart, marked failed)", n
            )
    except Exception as e:
        logger.warning(
            "[BknAgent] recovering dangling tasks failed; startup continues: %s", e
        )


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
    # The token is captured here rather than in get_account: FastAPI runs a
    # *synchronous* dependency in a thread pool, a ContextVar set there never
    # returns to the request coroutine, and caller_token() would always be None
    # (observed on the VM: no tools were loaded and the model invented a tool
    # call as its answer). The middleware shares a context chain with the
    # endpoint, so setting it here is visible, and it also covers routes that
    # do not go through get_account.
    auth_token = auth.set_caller_token(request.headers.get("authorization"))
    # Freeze the locale here for the same reason the caller token is frozen
    # here: this is the outermost point that shares a context with both the
    # endpoint and the exception handlers that render the error envelope.
    effective_locale = locale.resolve_accept_language(
        request.headers.get(locale.ACCEPT_LANGUAGE_HEADER)
    )
    # Also park it on the request scope. The catch-all Exception handler runs in
    # ServerErrorMiddleware, which sits *outside* this middleware, so by the time
    # it renders the 500 the ContextVar below has already been reset; scope state
    # is the only channel that still carries this request's negotiated locale.
    request.state.effective_locale = effective_locale
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
    """A business error — the HTTPException raised by err/not_found/bad_request
    — is contractually a *flat top-level* ErrorEnvelope. Starlette would wrap it
    as {"detail": ...}, which drifts from docs/api/bkn-agent.yaml and misaligns
    SDK parsing, so the detail is returned as the body directly. A non-dict
    detail, such as the default 404/405 routing string, is completed into an
    envelope."""
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
    """Any unexpected exception also returns the platform error envelope.

    The assembly stage of /chat — tool loading, downstream connections — raises
    non-HTTPException errors, such as the RuntimeError from a failed explicit
    toolbox reference fetch. Without this fallback those would surface as a bare
    text/plain 500, breaking the frozen contract that every 4XX/5XX is an
    ErrorEnvelope and crashing SDK-side parsing.
    """
    logger.exception("[BknAgent] unhandled error on %s %s", request.method, request.url.path)
    ctx = observability.context_from_request(request)
    effective_locale = getattr(request.state, "effective_locale", None) or (
        locale.resolve_accept_language(request.headers.get(locale.ACCEPT_LANGUAGE_HEADER))
    )
    content = build_error_content("BknAgent.Internal.Unexpected", locale=effective_locale)
    # The exception type and message are internal diagnostics, kept in English
    # so they stay greppable against the logs; they are not a translated field.
    content["detail"] = f"{type(exc).__name__}: {exc}"
    content["trace_id"] = observability.current_trace_id(ctx)
    response = JSONResponse(
        status_code=500,
        content=content,
        headers=observability.response_headers(ctx),
    )
    # The exception escaped this middleware, so the normal header pass never ran.
    _apply_language_headers(response, request.url.path, effective_locale)
    return response
