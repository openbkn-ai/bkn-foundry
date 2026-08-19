"""Unit tests for main."""
import pytest
from unittest.mock import Mock, AsyncMock, patch, MagicMock
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from starlette.testclient import TestClient

from src.interfaces.rest.main import (
    create_app,
    _register_exception_handlers,
    _register_routes,
    _register_middleware,
)
from src.shared.errors.domain import NotFoundError, ValidationError


class TestCreateApp:
    """Tests for TestCreateApp."""

    def test_create_app_returns_fastapi_instance(self):
        """Test create app returns fastapi instance."""
        with patch('src.interfaces.rest.main.lifespan'):
            app = create_app()

            assert isinstance(app, FastAPI)
            assert app.title == "Sandbox Control Plane"

    def test_create_app_has_correct_metadata(self):
        """Test create app has correct metadata."""
        with patch('src.interfaces.rest.main.lifespan'):
            app = create_app()

            assert app.title == "Sandbox Control Plane"
            assert app.version == "2.1.0"
            assert app.docs_url == "/docs"
            assert app.redoc_url == "/redoc"
            assert app.openapi_url == "/openapi.json"

    def test_create_app_registers_cors_middleware(self):
        """Test create app registers CORS middleware."""
        with patch('src.interfaces.rest.main.lifespan'):
            app = create_app()

            # Check that CORS middleware is added
            cors_found = False
            for middleware in app.user_middleware:
                if 'CORSMiddleware' in str(middleware):
                    cors_found = True
                    break
            assert cors_found

    def test_create_app_registers_gzip_middleware(self):
        """Test create app registers Gzip middleware."""
        with patch('src.interfaces.rest.main.lifespan'):
            app = create_app()

            # Check that Gzip middleware is added
            gzip_found = False
            for middleware in app.user_middleware:
                if 'GZipMiddleware' in str(middleware):
                    gzip_found = True
                    break
            assert gzip_found

    def test_create_app_registers_routes(self):
        """Test create app registers routes."""
        with patch('src.interfaces.rest.main.lifespan'):
            app = create_app()

            # Check that routes are registered
            routes = [route.path for route in app.routes]
            assert "/docs" in routes
            assert "/redoc" in routes
            assert "/openapi.json" in routes

    def test_create_app_has_lifespan(self):
        """Test create app has lifespan."""
        with patch('src.interfaces.rest.main.lifespan') as mock_lifespan:
            app = create_app()

            # The router should have the lifespan set
            assert app.router.lifespan_context is not None


class TestRegisterExceptionHandlers:
    """Tests for TestRegisterExceptionHandlers."""

    @pytest.fixture
    def app(self):
        """Create app."""
        return FastAPI()

    def test_register_exception_handlers_adds_handlers(self, app):
        """Test register exception handlers adds handlers."""
        _register_exception_handlers(app)

        # Check that exception handlers are registered
        assert Exception in app.exception_handlers
        assert NotFoundError in app.exception_handlers
        assert ValidationError in app.exception_handlers

    @pytest.mark.asyncio
    async def test_not_found_error_handler(self, app):
        """Test not found error handler."""
        _register_exception_handlers(app)

        handler = app.exception_handlers[NotFoundError]
        request = MagicMock(spec=Request)
        request.url = MagicMock()
        request.url.path = "/test"
        request.method = "GET"

        exc = NotFoundError("Resource not found")

        with patch('src.interfaces.rest.main.logger'):
            response = await handler(request, exc)

        assert isinstance(response, JSONResponse)
        assert response.status_code == 404

    @pytest.mark.asyncio
    async def test_validation_error_handler(self, app):
        """Test validation error handler."""
        _register_exception_handlers(app)

        handler = app.exception_handlers[ValidationError]
        request = MagicMock(spec=Request)
        request.url = MagicMock()
        request.url.path = "/test"
        request.method = "POST"

        exc = ValidationError("Validation failed")

        with patch('src.interfaces.rest.main.logger'):
            response = await handler(request, exc)

        assert isinstance(response, JSONResponse)
        assert response.status_code == 409

    @pytest.mark.asyncio
    async def test_global_exception_handler(self, app):
        """Test global exception handler."""
        _register_exception_handlers(app)

        handler = app.exception_handlers[Exception]
        request = MagicMock(spec=Request)
        request.url = MagicMock()
        request.url.path = "/test"
        request.method = "GET"

        exc = RuntimeError("Unexpected error")

        with patch('src.interfaces.rest.main.logger'):
            response = await handler(request, exc)

        assert isinstance(response, JSONResponse)
        assert response.status_code == 500


class TestRegisterRoutes:
    """Tests for TestRegisterRoutes."""

    @pytest.fixture
    def app(self):
        """Create app."""
        return FastAPI()

    def test_register_routes_adds_routers(self, app):
        """Test register routes adds routers."""
        _register_routes(app)

        routes = [route.path for route in app.routes]

        # Check API routes
        assert "/api/v1/health" in routes or any("/health" in r for r in routes)
        assert "/" in routes  # Root endpoint

    def test_root_endpoint_exists(self, app):
        """Test root endpoint exists."""
        _register_routes(app)

        # Find root route
        root_route = None
        for route in app.routes:
            if route.path == "/":
                root_route = route
                break

        assert root_route is not None


class TestRegisterMiddleware:
    """Tests for TestRegisterMiddleware."""

    @pytest.fixture
    def app(self):
        """Create app."""
        return FastAPI()

    def test_register_middleware_adds_request_logging(self, app):
        """Test register middleware adds request logging."""
        _register_middleware(app)

        # Check that middleware is added
        middleware_found = False
        for middleware in app.user_middleware:
            if 'RequestLoggingMiddleware' in str(middleware):
                middleware_found = True
                break
        assert middleware_found
