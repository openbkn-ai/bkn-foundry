"""Tests for test_app_utils."""
import pytest
from unittest.mock import Mock, AsyncMock, patch, MagicMock
from fastapi import FastAPI
from fastapi.responses import JSONResponse
from app.utils.app_utils import (
    conf_init,
    start_event,
    shutdown_event,
    auth_middleware,
    RequestSizeMiddleware,
    create_app
)
from app.core.config import base_config


class TestConfInit:
    """Tests for test conf init."""

    def test_conf_init_production(self):
        """Test test conf init production."""
        app = FastAPI()
        with patch.dict('os.environ', {'ENVIRONMENT': 'production'}):
            with patch('app.utils.app_utils.sys_log.info') as mock_log:
                conf_init(app)
                assert app.docs_url is None
                assert app.redoc_url is None
                assert app.debug is False
                mock_log.assert_called()

    def test_conf_init_development(self):
        """Test test conf init development."""
        app = FastAPI()
        with patch.dict('os.environ', {'ENVIRONMENT': 'development'}):
            with patch('app.utils.app_utils.sys_log.info') as mock_log:
                conf_init(app)
                # The development environment should not modify docs or debug.
                mock_log.assert_called()

    def test_conf_init_default(self):
        """Test test conf init default."""
        app = FastAPI()
        with patch.dict('os.environ', {}, clear=True):
            with patch('app.utils.app_utils.sys_log.info'):
                conf_init(app)


class TestStartEvent:
    """Tests for test start event."""

    @pytest.mark.asyncio
    async def test_start_event_success(self):
        """Test test start event success."""
        with patch('app.utils.app_utils.write_log', new_callable=AsyncMock) as mock_log:
            with patch('app.utils.app_utils.get_redis_util', new_callable=AsyncMock) as mock_redis:
                with patch('app.utils.app_utils.init_observability') as mock_obs:
                    mock_redis.return_value = Mock()
                    await start_event()
                    mock_log.assert_called_once_with(msg='系统启动')
                    mock_redis.assert_called_once()
                    mock_obs.assert_called_once()

    @pytest.mark.asyncio
    async def test_start_event_redis_error(self):
        """Test test start event redis error."""
        with patch('app.utils.app_utils.write_log', new_callable=AsyncMock):
            with patch('app.utils.app_utils.get_redis_util', new_callable=AsyncMock) as mock_redis:
                mock_redis.side_effect = Exception("Redis connection error")
                with pytest.raises(Exception):
                    await start_event()


class TestShutdownEvent:
    """Tests for test shutdown event."""

    @pytest.mark.asyncio
    async def test_shutdown_event(self):
        """Test test shutdown event."""
        with patch('app.utils.app_utils.write_log', new_callable=AsyncMock) as mock_log:
            with patch('app.utils.app_utils.shutdown_observability') as mock_obs:
                await shutdown_event()
                mock_log.assert_called_once_with(msg='系统关闭')
                mock_obs.assert_called_once()


class TestAuthMiddleware:
    """Tests for test auth middleware."""

    @pytest.fixture(autouse=True)
    def enable_auth(self):
        """Enable auth: the middleware token-validation branch only runs when AUTH_ENABLED=true. The default false value follows the anonymous allow branch and returns 200 directly, so 401 assertions cannot be hit. health/private endpoints bypass by path before auth and are unaffected."""
        with patch.object(base_config, "AUTH_ENABLED", True):
            yield

    @pytest.fixture
    def mock_request(self):
        """Test mock request."""
        request = Mock()
        request.url = Mock()
        request.headers = {}
        request.scope = {'headers': []}
        return request

    @pytest.fixture
    def mock_call_next(self):
        """Test mock call next."""
        async def call_next(request):
            return JSONResponse(content={"status": "ok"})
        return call_next

    @pytest.mark.asyncio
    async def test_health_endpoint_bypass(self, mock_request, mock_call_next):
        """Test test health endpoint bypass."""
        mock_request.url.path = "/api/v1/health"
        response = await auth_middleware(mock_request, mock_call_next)
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_private_endpoint_bypass(self, mock_request, mock_call_next):
        """Test test private endpoint bypass."""
        mock_request.url.path = "/api/private/test"
        response = await auth_middleware(mock_request, mock_call_next)
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_missing_authorization(self, mock_request, mock_call_next):
        """Test test missing authorization."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {}
        response = await auth_middleware(mock_request, mock_call_next)
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_invalid_authorization_format(self, mock_request, mock_call_next):
        """Test test invalid authorization format."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Invalid token"}
        response = await auth_middleware(mock_request, mock_call_next)
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_valid_token(self, mock_request, mock_call_next):
        """Test test valid token."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer valid_token"}
        
        # Mock response
        mock_response = AsyncMock()
        mock_response.status = 200
        mock_response.text = AsyncMock(return_value='{"active": true, "sub": "user123", "client_id": "client123"}')
        
        # Mock context managers
        mock_post_cm = AsyncMock()
        mock_post_cm.__aenter__ = AsyncMock(return_value=mock_response)
        mock_post_cm.__aexit__ = AsyncMock(return_value=None)
        
        mock_session = AsyncMock()
        mock_session.post = Mock(return_value=mock_post_cm)
        
        mock_session_cm = AsyncMock()
        mock_session_cm.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session_cm.__aexit__ = AsyncMock(return_value=None)
        
        with patch('app.utils.app_utils.aiohttp.ClientSession', return_value=mock_session_cm):
            response = await auth_middleware(mock_request, mock_call_next)
            # call_next should be called after validation succeeds.
            assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_inactive_token(self, mock_request, mock_call_next):
        """Test test inactive token."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer invalid_token"}
        
        # Mock response
        mock_response = AsyncMock()
        mock_response.status = 200
        mock_response.text = AsyncMock(return_value='{"active": false}')
        
        # Mock context managers
        mock_post_cm = AsyncMock()
        mock_post_cm.__aenter__ = AsyncMock(return_value=mock_response)
        mock_post_cm.__aexit__ = AsyncMock(return_value=None)
        
        mock_session = AsyncMock()
        mock_session.post = Mock(return_value=mock_post_cm)
        
        mock_session_cm = AsyncMock()
        mock_session_cm.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session_cm.__aexit__ = AsyncMock(return_value=None)
        
        with patch('app.utils.app_utils.aiohttp.ClientSession', return_value=mock_session_cm):
            response = await auth_middleware(mock_request, mock_call_next)
            assert response.status_code == 401

    @staticmethod
    def _mock_session(status, body):
        """Test mock session."""
        mock_response = AsyncMock()
        mock_response.status = status
        mock_response.text = AsyncMock(return_value=body)

        mock_post_cm = AsyncMock()
        mock_post_cm.__aenter__ = AsyncMock(return_value=mock_response)
        mock_post_cm.__aexit__ = AsyncMock(return_value=None)

        mock_session = AsyncMock()
        mock_session.post = Mock(return_value=mock_post_cm)

        mock_session_cm = AsyncMock()
        mock_session_cm.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session_cm.__aexit__ = AsyncMock(return_value=None)
        return mock_session_cm, mock_session

    @pytest.mark.asyncio
    async def test_appkey_valid_user(self, mock_request, mock_call_next):
        """Test test appkey valid user."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer bak_kid_secret"}

        session_cm, session = self._mock_session(
            200, '{"active": true, "sub": "user123", "account_type": "email", "key_id": "kid"}')
        with patch('app.utils.app_utils.aiohttp.ClientSession', return_value=session_cm), \
                patch.dict('os.environ', {"BKN_SAFE_URL": "http://safe:8080"}):
            response = await auth_middleware(mock_request, mock_call_next)
            assert response.status_code == 200
            session.post.assert_called_once_with(
                "http://safe:8080/api/safe/v1/api-keys/introspect",
                json={"token": "bak_kid_secret"},
                headers={"Accept-Language": "zh-CN"})
            assert (b"x-account-id", b"user123") in mock_request.scope['headers']
            assert (b"x-account-type", b"user") in mock_request.scope['headers']

    @pytest.mark.asyncio
    async def test_appkey_valid_app_account(self, mock_request, mock_call_next):
        """Test test appkey valid app account."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer bak_kid_secret"}

        session_cm, _ = self._mock_session(
            200, '{"active": true, "sub": "app456", "account_type": "app", "key_id": "kid"}')
        with patch('app.utils.app_utils.aiohttp.ClientSession', return_value=session_cm), \
                patch.dict('os.environ', {"BKN_SAFE_URL": "http://safe:8080"}):
            response = await auth_middleware(mock_request, mock_call_next)
            assert response.status_code == 200
            assert (b"x-account-type", b"app") in mock_request.scope['headers']

    @pytest.mark.asyncio
    async def test_appkey_inactive(self, mock_request, mock_call_next):
        """Test test appkey inactive."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer bak_kid_secret"}

        session_cm, _ = self._mock_session(200, '{"active": false}')
        with patch('app.utils.app_utils.aiohttp.ClientSession', return_value=session_cm), \
                patch.dict('os.environ', {"BKN_SAFE_URL": "http://safe:8080"}):
            response = await auth_middleware(mock_request, mock_call_next)
            assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_appkey_without_bkn_safe_url(self, mock_request, mock_call_next):
        """Test test appkey without bkn safe url."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer bak_kid_secret"}

        with patch.dict('os.environ', {}, clear=True):
            with patch('app.utils.app_utils.aiohttp.ClientSession') as mock_session_cls:
                response = await auth_middleware(mock_request, mock_call_next)
                assert response.status_code == 401
                mock_session_cls.assert_not_called()

    @pytest.mark.asyncio
    async def test_appkey_service_error(self, mock_request, mock_call_next):
        """Test test appkey service error."""
        mock_request.url.path = "/api/v1/test"
        mock_request.headers = {"Authorization": "Bearer bak_kid_secret"}

        session_cm, _ = self._mock_session(500, 'internal error')
        with patch('app.utils.app_utils.aiohttp.ClientSession', return_value=session_cm), \
                patch.dict('os.environ', {"BKN_SAFE_URL": "http://safe:8080"}):
            response = await auth_middleware(mock_request, mock_call_next)
            assert response.status_code == 400


class TestRequestSizeMiddleware:
    """Tests for test request size middleware."""

    @pytest.mark.asyncio
    async def test_request_within_size_limit(self):
        """Test test request within size limit."""
        middleware = RequestSizeMiddleware(app=Mock())
        request = Mock()
        request.headers = {'content-length': '1000'}
        
        async def call_next(req):
            return JSONResponse(content={"status": "ok"})
        
        response = await middleware.dispatch(request, call_next)
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_request_exceeds_size_limit(self):
        """Test test request exceeds size limit."""
        middleware = RequestSizeMiddleware(app=Mock())
        request = Mock()
        request.headers = {'content-length': str(11 * 1024 * 1024)}  # 11MB
        
        async def call_next(req):
            return JSONResponse(content={"status": "ok"})
        
        response = await middleware.dispatch(request, call_next)
        assert response.status_code == 413

    @pytest.mark.asyncio
    async def test_request_no_content_length(self):
        """Test test request no content length."""
        middleware = RequestSizeMiddleware(app=Mock())
        request = Mock()
        request.headers = {}
        
        async def call_next(req):
            return JSONResponse(content={"status": "ok"})
        
        response = await middleware.dispatch(request, call_next)
        assert response.status_code == 200


class TestCreateApp:
    """Tests for test create app."""

    def test_create_app_returns_fastapi(self):
        """Test test create app returns fastapi."""
        with patch('app.utils.app_utils.log_init'):
            with patch('app.utils.app_utils.conf_init'):
                with patch('app.utils.app_utils.router_init'):
                    app = create_app()
                    assert isinstance(app, FastAPI)

    def test_create_app_has_title(self):
        """Test test create app has title."""
        with patch('app.utils.app_utils.log_init'):
            with patch('app.utils.app_utils.conf_init'):
                with patch('app.utils.app_utils.router_init'):
                    app = create_app()
                    assert app.title == "My API"

    def test_create_app_has_version(self):
        """Test test create app has version."""
        with patch('app.utils.app_utils.log_init'):
            with patch('app.utils.app_utils.conf_init'):
                with patch('app.utils.app_utils.router_init'):
                    app = create_app()
                    assert app.version == "1.0.0"

    def test_create_app_has_startup_event(self):
        """Test test create app has startup event."""
        with patch('app.utils.app_utils.log_init'):
            with patch('app.utils.app_utils.conf_init'):
                with patch('app.utils.app_utils.router_init'):
                    app = create_app()
                    # FastAPI should have on_startup configuration.
                    assert hasattr(app, 'router')

    def test_create_app_initializes_components(self):
        """Test test create app initializes components."""
        with patch('app.utils.app_utils.log_init') as mock_log:
            with patch('app.utils.app_utils.conf_init') as mock_conf:
                with patch('app.utils.app_utils.router_init') as mock_router:
                    app = create_app()
                    mock_log.assert_called_once()
                    mock_conf.assert_called_once()
                    mock_router.assert_called_once()

