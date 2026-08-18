"""#637: framework-layer validation errors on the OpenAI-compatible surface must also use OpenAI error bodies. Controller errors for `/chat/completions` were already unified to `{"error": {...}}` in #620, but request bodies rejected by pydantic go through the `RequestValidationError` handler and previously still returned the Model Factory envelope. That gave one endpoint two contracts and forced integrations to implement two parsers. The handler is shared across the whole service: small-model and model-management endpoints are not compatibility surfaces and must keep their envelope unchanged; a global conversion would break their public contracts."""
import json

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.routers import router_init


@pytest.fixture
def client():
    app = FastAPI()
    router_init(app)
    return TestClient(app)


def _body(response):
    return json.loads(response.content.decode("utf-8"))


CHAT_PATHS = [
    "/api/mf-model-api/v1/chat/completions",
    "/api/private/mf-model-api/v1/chat/completions",
]


class TestOpenAICompatFace:
    @pytest.mark.parametrize("path", CHAT_PATHS)
    def test_type_error_returns_openai_shape(self, client, path):
        """Test test type error returns openai shape."""
        response = client.post(path, json={
            "model": "x", "stream": "yes",
            "messages": [{"role": "user", "content": "hi"}]})

        assert response.status_code == 400
        body = _body(response)
        assert set(body) == {"error"}
        assert set(body["error"]) == {"message", "type", "param", "code"}
        assert "Boolean" in body["error"]["message"]
        assert body["error"]["type"] == "invalid_request_error"

    @pytest.mark.parametrize("path", CHAT_PATHS)
    def test_missing_field_returns_openai_shape(self, client, path):
        response = client.post(path, json={"model": "x"})

        assert response.status_code == 400
        body = _body(response)
        assert set(body) == {"error"}
        assert "messages" in body["error"]["message"]

    def test_business_code_survives_in_error_code(self, client):
        """Test test business code survives in error code."""
        response = client.post(CHAT_PATHS[0], json={"model": "x"})
        assert _body(response)["error"]["code"] == \
            "ModelFactory.Router.ParamError.ParamMissing"

    def test_no_envelope_fields_leak(self, client):
        """Test test no envelope fields leak."""
        raw = client.post(CHAT_PATHS[0], json={
            "model": "x", "stream": "yes",
            "messages": [{"role": "user", "content": "hi"}]}).content.decode()
        for field in ("solution", "link", "description"):
            assert f'"{field}"' not in raw


class TestOtherEndpointsUnchanged:
    """Tests for test other endpoints unchanged."""

    OTHER_PATHS = [
        "/api/mf-model-api/v1/small-model/embeddings",
        "/api/private/mf-model-api/v1/small-model/embeddings",
    ]

    @pytest.mark.parametrize("path", OTHER_PATHS)
    def test_small_model_keeps_envelope(self, client, path):
        response = client.post(path, json={})
        if response.status_code == 404:
            pytest.skip(f"{path} 未注册在本服务")
        assert response.status_code == 400
        body = _body(response)
        assert "error" not in body
        assert set(body) >= {"code", "description", "detail", "solution", "link"}
        assert body["code"].startswith("ModelFactory.Router.ParamError")


class TestPathDispatch:
    @pytest.mark.parametrize("path,expected", [
        ("/api/mf-model-api/v1/chat/completions", True),
        ("/api/private/mf-model-api/v1/chat/completions", True),
        ("/api/mf-model-api/v1/small-model/embeddings", False),
        ("/api/mf-model-api/v1/small-model/rerank", False),
        ("/api/v1/health/ready", False),
        ("", False),
    ])
    def test_only_chat_completions_is_compat_face(self, path, expected):
        from app.routers import _is_openai_compat
        assert _is_openai_compat(path) is expected
