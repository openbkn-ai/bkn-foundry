"""#637：OpenAI 兼容面上的框架层校验错误也要走 OpenAI 错误体。

`/chat/completions` 的控制器错误在 #620 已经统一成 `{"error": {...}}`，但请求体
在 pydantic 层就被打回的那类走的是 `RequestValidationError` 处理器，之前仍返回
模型工厂 envelope——同一个端点两种契约，对接方得写两套解析。

同时这个处理器是**全服务共用**的：小模型、模型管理等端点不是兼容面，必须保持
envelope 不变，一刀切会破掉它们的对外契约。
"""
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
        """stream 传字符串：pydantic 打回，公开面与 S2S 面都要是 OpenAI 形状"""
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
        """业务码不能丢，只是从顶层挪进 error.code"""
        response = client.post(CHAT_PATHS[0], json={"model": "x"})
        assert _body(response)["error"]["code"] == \
            "ModelFactory.Router.ParamError.ParamMissing"

    def test_no_envelope_fields_leak(self, client):
        """solution / link 只对模型工厂控制台有意义，不进兼容面"""
        raw = client.post(CHAT_PATHS[0], json={
            "model": "x", "stream": "yes",
            "messages": [{"role": "user", "content": "hi"}]}).content.decode()
        for field in ("solution", "link", "description"):
            assert f'"{field}"' not in raw


class TestOtherEndpointsUnchanged:
    """处理器全服务共用，非兼容面必须原样保留 envelope"""

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
