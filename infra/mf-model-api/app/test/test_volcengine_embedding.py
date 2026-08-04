import asyncio
import json

from app.utils import external_small_model_utils


class _Response:
    def __init__(self, status=200):
        self.status = status

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc, tb):
        return False

    async def text(self):
        if self.status != 200:
            return json.dumps({"error": {"message": "rate limit exceeded"}})
        return json.dumps({
            "object": "list",
            "data": {"object": "embedding", "embedding": [0.1] * 1024},
            "model": "doubao-embedding-vision-251215",
            "usage": {
                "prompt_tokens": 1,
                "total_tokens": 1,
                "prompt_tokens_details": {"text_tokens": 1},
            },
        })


class _Session:
    def __init__(self, status=200):
        self.status = status
        self.post_kwargs = None
        self.post_kwargs_list = []

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc, tb):
        return False

    def post(self, *args, **kwargs):
        self.post_kwargs = kwargs
        self.post_kwargs_list.append(kwargs)
        return _Response(self.status)


def test_volcengine_embedding_uses_multimodal_request_and_normalizes_response(monkeypatch):
    session = _Session()
    monkeypatch.setattr(external_small_model_utils.aiohttp, "ClientSession", lambda **kwargs: session)
    client = external_small_model_utils.InnerClient(
        url="https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal",
        model_name="doubao-embedding-vision-251215",
        api_key="test-key",
        embedding_dim=1024,
    )

    result = asyncio.run(client.embedding(["Hi, who are you?"]))

    assert session.post_kwargs["json"] == {
        "model": "doubao-embedding-vision-251215",
        "input": [{"type": "text", "text": "Hi, who are you?"}],
        "dimensions": 1024,
    }
    assert isinstance(result["data"], list)
    assert len(result["data"][0]["embedding"]) == 1024
    assert result["data"][0]["index"] == 0


def test_volcengine_embedding_batches_texts_as_single_item_requests(monkeypatch):
    session = _Session()
    monkeypatch.setattr(external_small_model_utils.aiohttp, "ClientSession", lambda **kwargs: session)
    client = external_small_model_utils.InnerClient(
        url="https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal",
        model_name="doubao-embedding-vision-251215",
        api_key="test-key",
        embedding_dim=1024,
    )

    result = asyncio.run(client.embedding(["first", "second"]))

    assert [request["json"]["input"] for request in session.post_kwargs_list] == [
        [{"type": "text", "text": "first"}],
        [{"type": "text", "text": "second"}],
    ]
    assert [item["index"] for item in result["data"]] == [0, 1]
    assert result["usage"]["prompt_tokens"] == 2
    assert result["usage"]["total_tokens"] == 2
    assert result["usage"]["prompt_tokens_details"]["text_tokens"] == 2


def test_volcengine_embedding_preserves_upstream_error_status(monkeypatch):
    session = _Session(status=429)
    monkeypatch.setattr(external_small_model_utils.aiohttp, "ClientSession", lambda **kwargs: session)
    client = external_small_model_utils.InnerClient(
        url="https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal",
        model_name="doubao-embedding-vision-251215",
        api_key="test-key",
    )

    try:
        asyncio.run(client.embedding(["rate limited"]))
        assert False, "expected an upstream error"
    except external_small_model_utils.UpstreamModelError as error:
        assert error.status == 429
        assert error.detail == "rate limit exceeded"


def test_volcengine_embedding_uses_configured_dimension():
    client = external_small_model_utils.InnerClient(
        url="https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal",
        model_name="doubao-embedding-vision-250615",
        api_key="test-key",
        embedding_dim=2048,
    )

    assert client._embedding_params(["hello"])["dimensions"] == 2048


def test_volcengine_embedding_omits_unspecified_dimension():
    client = external_small_model_utils.InnerClient(
        url="https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal",
        model_name="ep-20240604000000-abcde",
        api_key="test-key",
    )

    params = client._embedding_params(["hello"])
    assert "dimensions" not in params
    assert params["input"] == [{"type": "text", "text": "hello"}]


def test_non_vision_doubao_embedding_preserves_openai_text_request(monkeypatch):
    session = _Session()
    monkeypatch.setattr(external_small_model_utils.aiohttp, "ClientSession", lambda **kwargs: session)
    client = external_small_model_utils.InnerClient(
        url="https://ark.cn-beijing.volces.com/api/v3/embeddings",
        model_name="doubao-embedding-250615",
        api_key="test-key",
    )

    asyncio.run(client.embedding(["Hi, who are you?"]))

    assert session.post_kwargs["json"] == {
        "model": "doubao-embedding-250615",
        "input": ["Hi, who are you?"],
    }
