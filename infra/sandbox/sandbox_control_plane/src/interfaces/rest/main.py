"""
FastAPI application

Entry point of the sandbox control plane FastAPI application.
"""

import time
from contextlib import asynccontextmanager
from typing import AsyncGenerator

from fastapi import FastAPI, Request, status
from fastapi.responses import JSONResponse
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.gzip import GZipMiddleware

# Configure logging FIRST before any other imports
from src.infrastructure.config.settings import get_settings
from src.infrastructure.logging import configure_logging, get_logger
from src.shared import locale
from src.shared.i18n import message

# Initialize logging with settings
_settings = get_settings()
configure_logging(
    log_level=_settings.log_level,
    log_format=_settings.log_format,
)

# Now get logger
logger = get_logger(__name__)

# Import routes after logging is configured
from src.interfaces.rest.api.v1 import (
    sessions,
    executions,
    templates,
    health,
    files,
    internal,
)
from src.interfaces.rest.schemas.response import HealthResponse

# When the application started
_start_time = time.time()


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """
    Application lifespan management

    Handles what happens on start-up and on shutdown.
    """
    # Start-up
    logger.info("Starting Sandbox Control Plane")

    # Wire up dependency injection
    from src.infrastructure.dependencies import initialize_dependencies, get_storage_service

    initialize_dependencies(app)
    logger.info("Dependencies initialized")

    # Initialize S3 storage, making sure the bucket exists
    try:
        storage_service = get_storage_service()
        await storage_service.initialize()
    except Exception as e:
        logger.warning(f"S3 storage initialization failed (continuing): {e}")

    # Initialize the database and create the tables
    from src.infrastructure.persistence.database import db_manager
    from src.infrastructure.config.settings import get_settings

    await db_manager.upgrade_legacy_database_name()
    logger.info("Legacy database upgrade check completed")

    await db_manager.initialize()
    logger.info("Database initialized")
    await db_manager.run_startup_schema_migrations()
    logger.info("Startup schema migrations completed")

    # Whether tables and seed data are created automatically depends on the environment
    from src.infrastructure.config.settings import get_settings

    settings = get_settings()
    if settings.environment in ("development", "staging"):
        from src.infrastructure.persistence.seed.seeder import seed_default_data

        # Create the tables
        await db_manager.create_tables()
        logger.info("Database tables created")

        # Seed the default data
        seed_stats = await seed_default_data(force=False)
        logger.info(
            "Default data initialized",
            runtime_nodes=seed_stats["runtime_nodes"],
            templates=seed_stats["templates"],
        )

    # ============= State sync at start-up =============
    from src.infrastructure.dependencies import get_state_sync_service

    state_sync_service = get_state_sync_service()
    try:
        sync_stats = await state_sync_service.sync_on_startup()
        logger.info(
            "Startup state sync completed",
            total=sync_stats.get("total", 0),
            healthy=sync_stats.get("healthy", 0),
            unhealthy=sync_stats.get("unhealthy", 0),
            recovered=sync_stats.get("recovered", 0),
            failed=sync_stats.get("failed", 0),
        )
        if sync_stats.get("errors"):
            logger.warning("State sync had errors", errors=sync_stats["errors"])
    except Exception as e:
        logger.error("Failed to perform startup state sync", error=str(e), exc_info=True)

    # ============= Start the background task manager =============
    from src.infrastructure.background_tasks import BackgroundTaskManager
    from src.infrastructure.dependencies import get_state_sync_service

    background_task_manager = BackgroundTaskManager()

    # Register the periodic health check, every 30 seconds
    state_sync_svc = get_state_sync_service()
    background_task_manager.register_task(
        name="health_check",
        func=state_sync_svc.periodic_health_check,
        interval_seconds=30,
        initial_delay_seconds=30,  # delay the first run by 30 seconds
    )

    # Register session cleanup, every 5 minutes
    from src.application.services.session_cleanup_service import SessionCleanupService
    from src.infrastructure.dependencies import get_docker_scheduler_service, get_storage_service
    from src.infrastructure.persistence.repositories.sql_session_repository import (
        SqlSessionRepository,
    )
    from src.infrastructure.persistence.database import db_manager

    async def session_cleanup_task():
        """Session cleanup task; builds a fresh repository on every run"""
        async with db_manager.get_session() as session:
            session_repo = SqlSessionRepository(session)
            scheduler = get_docker_scheduler_service(
                runtime_node_repo=None,
                template_repo=None,
            )
            storage_service = get_storage_service()
            cleanup_svc = SessionCleanupService(
                session_repo=session_repo,
                scheduler=scheduler,
                idle_timeout_minutes=settings.idle_threshold_minutes,
                max_lifetime_hours=settings.max_lifetime_hours,
                storage_service=storage_service,
            )
            return await cleanup_svc.cleanup_idle_sessions()

    background_task_manager.register_task(
        name="session_cleanup",
        func=session_cleanup_task,
        interval_seconds=300,  # 5 minutes
        initial_delay_seconds=60,  # delay the first run by 1 minute
    )

    # Register session creation timeout detection, every 5 minutes
    from src.application.services.session_stuck_creating_service import SessionStuckCreatingService

    async def stuck_creating_check_task():
        """Session creation timeout task; builds a fresh repository on every run"""
        async with db_manager.get_session() as session:
            session_repo = SqlSessionRepository(session)
            stuck_creating_svc = SessionStuckCreatingService(
                session_repo=session_repo,
                creating_timeout_seconds=settings.creating_timeout_seconds,
            )
            return await stuck_creating_svc.check_and_mark_stuck_sessions()

    background_task_manager.register_task(
        name="stuck_creating_check",
        func=stuck_creating_check_task,
        interval_seconds=300,  # 5 minutes, matching the cleanup task
        initial_delay_seconds=60,  # delay the first run by 1 minute
    )

    # Start every background task
    await background_task_manager.start_all()
    logger.info(f"Background tasks started: {background_task_manager.task_count} tasks")

    # Keep the background task manager on app.state, for shutdown
    app.state.background_task_manager = background_task_manager

    yield

    # Shutdown
    logger.info("Shutting down Sandbox Control Plane")

    # Stop every background task
    if hasattr(app.state, "background_task_manager"):
        await app.state.background_task_manager.stop_all()
        logger.info("Background tasks stopped")

    # Clean up the dependencies, closing the database connections
    from src.infrastructure.dependencies import cleanup_dependencies

    await cleanup_dependencies(app)
    await db_manager.close()


def create_app() -> FastAPI:
    """
    Create the FastAPI application

    A factory, which keeps it testable and configurable.
    """
    app = FastAPI(
        title="Sandbox Control Plane",
        description="Code sandbox management platform API",
        version="2.1.0",
        docs_url="/docs",
        redoc_url="/redoc",
        openapi_url="/openapi.json",
        lifespan=lifespan,
    )

    # Configure CORS
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],  # production should name the actual origins
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # Add Gzip compression
    app.add_middleware(GZipMiddleware, minimum_size=1000)

    # Register the exception handlers
    _register_exception_handlers(app)

    # Register the middleware
    _register_middleware(app)

    # Register the routes
    _register_routes(app)

    return app


def _apply_language_headers(response, path: str, effective_locale: str) -> None:
    """Declare the response language only where the body actually carries it.

    Error envelopes get Content-Language; a purely machine-readable success body
    does not. Business API responses are authenticated, so they also stay out of
    shared caches.
    """
    if not locale.is_business_api_path(path):
        return
    response.headers["Cache-Control"] = locale.merge_cache_control(
        response.headers.get("Cache-Control", "")
    )
    if response.status_code >= 400:
        response.headers["Content-Language"] = effective_locale


def _register_exception_handlers(app: FastAPI) -> None:
    """Register the exception handlers"""
    from src.shared.errors.domain import NotFoundError, ValidationError

    @app.exception_handler(NotFoundError)
    async def not_found_exception_handler(request: Request, exc: NotFoundError) -> JSONResponse:
        """404 handling"""
        logger.warning(
            "Resource not found",
            path=request.url.path,
            method=request.method,
            error=str(exc),
        )
        return JSONResponse(
            status_code=status.HTTP_404_NOT_FOUND,
            content={
                "error": "Not Found",
                "message": exc.message,
                "detail": str(exc),
            },
        )

    @app.exception_handler(ValidationError)
    async def validation_exception_handler(request: Request, exc: ValidationError) -> JSONResponse:
        """409 Conflict handling"""
        logger.warning(
            "Validation error (conflict)",
            path=request.url.path,
            method=request.method,
            error=str(exc),
        )
        return JSONResponse(
            status_code=status.HTTP_409_CONFLICT,
            content={
                "error": "Conflict",
                "message": exc.message,
                "detail": str(exc),
            },
        )

    @app.exception_handler(Exception)
    async def global_exception_handler(request: Request, exc: Exception) -> JSONResponse:
        """Global exception handling"""
        logger.error(
            "Unhandled exception",
            path=request.url.path,
            method=request.method,
            error=str(exc),
            exc_info=exc,
        )
        # This handler runs in ServerErrorMiddleware, outside the locale
        # middleware, so the ContextVar has already been reset by the time the
        # exception reaches here; read the locale off the request scope instead.
        effective_locale = getattr(request.state, "effective_locale", None) or (
            locale.resolve_accept_language(request.headers.get(locale.ACCEPT_LANGUAGE_HEADER))
        )
        response = JSONResponse(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            content={
                "error": "Internal Server Error",
                "message": message("Sandbox.Internal.Unexpected", locale=effective_locale),
                # The exception text is an internal diagnostic and stays as it is.
                "detail": str(exc) if app.debug else None,
            },
        )
        # The exception escaped the locale middleware, so its header pass never ran.
        _apply_language_headers(response, request.url.path, effective_locale)
        return response


def _register_routes(app: FastAPI) -> None:
    """Register the routes"""

    # Register every API route
    app.include_router(health.router, prefix="/api/v1")
    app.include_router(sessions.router, prefix="/api/v1")
    app.include_router(executions.router, prefix="/api/v1")
    app.include_router(templates.router, prefix="/api/v1")
    app.include_router(files.router, prefix="/api/v1")
    app.include_router(internal.router, prefix="/api/v1")  # internal API

    # Root endpoint
    @app.get("/", tags=["root"])
    async def root() -> dict:
        """Root endpoint"""
        return {
            "name": "Sandbox Control Plane",
            "version": "2.1.0",
            "status": "operational",
            "features": [
                "session_management",
                "code_execution",
                "template_management",
                "file_operations",
                "state_sync",
            ],
            "documentation": {
                "swagger": "/docs",
                "redoc": "/redoc",
                "openapi": "/openapi.json",
            },
        }


def _register_middleware(app: FastAPI) -> None:
    """Register the middleware"""

    @app.middleware("http")
    async def locale_middleware(request: Request, call_next):
        """Freeze the request locale before anything renders a message.

        The effective locale is also parked on the request scope: the catch-all
        Exception handler runs in ServerErrorMiddleware, outside this
        middleware, so by the time it renders a 500 the ContextVar below has
        already been reset.
        """
        effective_locale = locale.resolve_accept_language(
            request.headers.get(locale.ACCEPT_LANGUAGE_HEADER)
        )
        request.state.effective_locale = effective_locale
        token = locale.set_effective_locale(effective_locale)
        try:
            response = await call_next(request)
        finally:
            locale.reset_effective_locale(token)
        _apply_language_headers(response, request.url.path, effective_locale)
        return response

    # Add request logging middleware first (wraps all other middleware)
    from src.interfaces.rest.middleware import RequestLoggingMiddleware

    app.add_middleware(RequestLoggingMiddleware)


# Create the application instance
app = create_app()


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "sandbox_control_plane.src.interfaces.rest.main:app",
        host="0.0.0.0",
        port=8000,
        reload=True,
        log_level="info",
    )
