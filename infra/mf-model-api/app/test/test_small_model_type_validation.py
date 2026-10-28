# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

"""Regression coverage for #2359 using real handlers with isolated dependencies."""
import asyncio
import json
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest
from starlette.responses import JSONResponse

from app.commons import errors
from app.test.test_reranker_max_documents import load
from app.utils.external_small_model_utils import InnerClient, UpstreamModelError


def handler_environment(endpoint, model_type, identity="name", cached=False, permission=True):
    row = dict(f_model_id="123", f_model_name="example", f_model_type=model_type,
               f_model_config='{}', f_adapter=False, f_adapter_code=None,
               f_embedding_dim=None, f_max_documents=None)
    old = {key: value for key, value in row.items()
           if key not in ("f_embedding_dim", "f_max_documents")}
    cache_value = str([old if cached == "legacy" else row]) if cached else None
    redis = SimpleNamespace(get_str=AsyncMock(return_value=cache_value), set_str=AsyncMock())
    embedding = {"object": "list", "data": [{"embedding": [0.1, 0.2]}], "usage": {}}
    rerank = {"object": "rerank", "results": [{"index": 0, "relevance_score": 1}], "usage": {}}
    provider = SimpleNamespace(embedding=AsyncMock(return_value=embedding),
                               reranker=AsyncMock(return_value=rerank))
    dao = SimpleNamespace(get_model_info_by_name_id=Mock(return_value=[row]),
                          get_default_by_type=Mock(return_value=[row]))
    dependencies = {name: getattr(errors, name) for name in dir(errors) if name.startswith("ModelFactory_")}
    dependencies.update(
        redis_util=redis, small_model_dao=dao, json=json, JSONResponse=JSONResponse,
        InnerClient=Mock(return_value=provider), StandLogger=Mock(), get_logger=lambda: None,
        UpstreamModelError=UpstreamModelError, EMBEDDING_MODEL_TYPE="embedding",
        RERANKER_MODEL_TYPE="reranker", LEGACY_DEFAULT_MODEL_NAMES={"reranker": "reranker"},
        _model_cache_ttl=lambda default: 60 if default else 86400,
        NotPermissionError={"code": "Forbidden"},
        permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=permission)))
    module = load("controller/small_model_controller.py",
                  {"embedding_model_used", "reranker_model_used", "_load_small_model",
                   "_model_type_error", "_embedding_response_error", "_model_missing_error"}, dependencies)
    request = SimpleNamespace(model="example" if identity == "name" else "",
                              model_id="123" if identity == "id" else "",
                              input=["text"], query="query", documents=["document"])
    return module, request, provider, dao


def invoke(module, request, endpoint, private=False):
    return asyncio.run(getattr(module, endpoint + "_model_used")(
        request, "user", "en-US", "user", "test", private=private))


@pytest.mark.parametrize("endpoint", ["embedding", "reranker"])
@pytest.mark.parametrize("identity", ["name", "id"])
@pytest.mark.parametrize("cached", [False, True, "legacy"])
@pytest.mark.parametrize("private", [False, True])
@pytest.mark.parametrize("matching", [False, True])
def test_registered_type_checked_on_every_lookup(endpoint, identity, cached, private, matching):
    actual = endpoint if matching else ("reranker" if endpoint == "embedding" else "embedding")
    module, request, provider, dao = handler_environment(endpoint, actual, identity, cached)
    response = invoke(module, request, endpoint, private)
    if matching:
        assert response == getattr(provider, endpoint).return_value
        getattr(provider, endpoint).assert_awaited_once()
    else:
        assert response.status_code == 400
        body = json.loads(response.body)
        assert body["code"] == "ModelFactory.ExternalSmallModel.Used.InvalidParameter"
        assert body["detail"] == f"Model type mismatch: expected {endpoint}, got {actual}."
        module.InnerClient.assert_not_called()
        provider.embedding.assert_not_awaited()
        provider.reranker.assert_not_awaited()
    assert dao.get_model_info_by_name_id.call_count == (0 if cached is True else 1)


@pytest.mark.parametrize("endpoint", ["embedding", "reranker"])
def test_permission_denial_precedes_type_error(endpoint):
    actual = "reranker" if endpoint == "embedding" else "embedding"
    module, request, provider, _ = handler_environment(endpoint, actual, permission=False)
    assert invoke(module, request, endpoint).status_code == 403
    module.InnerClient.assert_not_called()


@pytest.mark.parametrize("cached", [False, True, "legacy"])
def test_default_reranker_also_checks_type(cached):
    module, request, provider, _ = handler_environment("reranker", "embedding", "default", cached)
    assert invoke(module, request, "reranker").status_code == 400
    module.InnerClient.assert_not_called()


def test_legacy_default_name_cannot_select_embedding():
    module, request, provider, dao = handler_environment("reranker", "embedding", "default")
    dao.get_default_by_type.return_value = []
    assert invoke(module, request, "reranker").status_code == 400
    dao.get_model_info_by_name_id.assert_called_once_with("reranker", None)
    module.InnerClient.assert_not_called()


@pytest.mark.parametrize("payload", [
    {"object": "rerank", "results": [], "usage": {"total_tokens": 2}},
    {}, None, [], {"data": []}, {"data": {}}, {"data": [None]},
    {"data": [{"embedding": []}]}, {"data": [{"embedding": "vector"}]},
    {"data": [{"embedding": ["secret-provider-content"]}]},
    {"data": [{"embedding": [True]}]},
])
@pytest.mark.parametrize("adapter", [False, True])
def test_invalid_embedding_output_is_gateway_error(payload, adapter, monkeypatch):
    module, request, provider, _ = handler_environment("embedding", "embedding")
    # Execute the real client paths, isolating only the HTTP request.
    row = module.small_model_dao.get_model_info_by_name_id.return_value[0]
    row["f_adapter"] = adapter
    row["f_adapter_code"] = f"async def main(texts): return {payload!r}" if adapter else None
    module.embedding_model_used.__globals__["InnerClient"] = InnerClient
    post = AsyncMock(return_value=payload)
    monkeypatch.setattr(InnerClient, "_post_embedding", post)
    response = invoke(module, request, "embedding")
    assert response.status_code == 502
    body = json.loads(response.body)
    assert body["code"] == "ModelFactory.ExternalSmallModel.Used.InvalidResponse"
    assert "secret-provider-content" not in response.body.decode()
    if adapter:
        post.assert_not_awaited()
    else:
        post.assert_awaited_once()


def test_valid_embedding_response_preserves_provider_extensions():
    module, request, provider, _ = handler_environment("embedding", "embedding")
    payload = {"data": [{"embedding": [0, 0.5, -1]}], "provider_extension": {"version": 1}}
    provider.embedding.return_value = payload
    assert invoke(module, request, "embedding") is payload
