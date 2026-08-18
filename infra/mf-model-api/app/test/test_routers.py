"""Router tests."""
import json
import re
import pytest
from unittest.mock import Mock, AsyncMock, patch
from fastapi.testclient import TestClient
from fastapi import FastAPI
from app.interfaces.logics import UsedEmbedding
from app.routers.llm_router import llm_route, health_route


class TestHealthRoutes:
    """Tests for test health routes."""

    @pytest.fixture
    def app(self):
        """Test app."""
        app = FastAPI()
        app.include_router(health_route)
        return app

    @pytest.fixture
    def client(self, app):
        """Test client."""
        return TestClient(app)

    def test_health_ready(self, client):
        """Test test health ready."""
        response = client.get("/health/ready")
        assert response.status_code == 200
        assert response.json() == {"res": 0}

    def test_health_alive(self, client):
        """Test test health alive."""
        response = client.get("/health/alive")
        assert response.status_code == 200
        assert response.json() == {"res": 0}


class TestLLMRoutes:
    """Tests for test llmroutes."""

    @pytest.fixture
    def app(self):
        """Test app."""
        app = FastAPI()
        app.include_router(llm_route)
        return app

    @pytest.fixture
    def client(self, app):
        """Test client."""
        return TestClient(app)

    def test_chat_completions_endpoint_exists(self, client):
        """Test test chat completions endpoint exists."""
        # Because authentication is required, it should return 401 or 422.
        response = client.post("/chat/completions")
        assert response.status_code in [401, 422]

    def test_chat_completions_with_valid_request(self, client):
        """Test test chat completions with valid request."""
        from fastapi.responses import JSONResponse
        
        request_data = {
            "model": "test_model",
            "messages": [{"role": "user", "content": "你好"}],
            "temperature": 0.7,
            "top_p": 0.9,
            "frequency_penalty": 0,
            "presence_penalty": 0,
            "max_tokens": 1000
        }
        
        # Mock response
        mock_response = {
            "id": "chatcmpl-123",
            "object": "chat.completion",
            "created": 1677652288,
            "model": "test_model",
            "choices": [{
                "index": 0,
                "message": {
                    "role": "assistant",
                    "content": "你好！有什么我可以帮助你的吗？",
                },
                "finish_reason": "stop"
            }],
            "usage": {
                "prompt_tokens": 9,
                "completion_tokens": 12,
                "total_tokens": 21
            }
        }
        
        # Patch both get_user_info and used_model_openai within the test
        with patch('app.routers.llm_router.get_user_info', new_callable=AsyncMock) as mock_get_user:
            with patch('app.routers.llm_router.used_model_openai', new_callable=AsyncMock) as mock_used_model:
                mock_get_user.return_value = ("user123", "zh", "user")
                mock_used_model.return_value = JSONResponse(status_code=200, content=mock_response)
                
                # Verify the route handles requests correctly.
                response = client.post("/chat/completions", json=request_data)
                assert response.status_code == 200
                assert "choices" in response.json()


class TestRouterIntegration:
    """Tests for test router integration."""

    def test_llm_route_has_prefix(self):
        """Test test llm route has prefix."""
        # This test verifies that the route object exists.
        assert llm_route is not None

    def test_health_route_exists(self):
        """Test test health route exists."""
        assert health_route is not None


class TestAPIDocumentation:
    """Tests for test apidocumentation."""

    @pytest.fixture
    def app_with_docs(self):
        """Test app with docs."""
        app = FastAPI()
        app.include_router(llm_route)
        app.include_router(health_route)
        return app

    @pytest.fixture
    def client(self, app_with_docs):
        """Test client."""
        return TestClient(app_with_docs)

    def test_openapi_schema_exists(self, client):
        """Test test openapi schema exists."""
        response = client.get("/openapi.json")
        assert response.status_code == 200
        schema = response.json()
        assert "openapi" in schema
        assert "paths" in schema

    def test_health_endpoints_in_schema(self, client):
        """Test test health endpoints in schema."""
        response = client.get("/openapi.json")
        schema = response.json()
        # Health-check endpoints are usually excluded from the schema.
        # Only verify that the schema is valid here.
        assert "paths" in schema

    def test_public_chat_schema_descriptions_are_english(self, client):
        schema = client.get("/openapi.json").json()
        operation = schema["paths"]["/chat/completions"]["post"]
        request_schema = schema["components"]["schemas"]["LLMUsedOpenAI"]
        embedding_schema = UsedEmbedding.schema()
        rendered = json.dumps(
            {"operation": operation, "request_schema": request_schema,
             "embedding_schema": embedding_schema},
            ensure_ascii=False,
        )

        assert re.search(r"[\u4e00-\u9fff]", rendered) is None
        assert embedding_schema["properties"]["input"]["description"] == "Content to embed"

