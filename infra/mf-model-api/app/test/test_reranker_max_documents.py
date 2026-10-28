# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

"""Execute the real rerank handler with isolated cache, DAO and provider clients."""
import ast
import asyncio
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest
from starlette.responses import JSONResponse

APP = Path(__file__).parents[1]


def load(path, names, dependencies, strip_methods=False):
    nodes = [node for node in ast.parse((APP / path).read_text()).body
             if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
    if strip_methods:
        for node in nodes:
            for method in node.body:
                if isinstance(method, ast.FunctionDef):
                    method.decorator_list = []
    namespace = {"__name__": __name__, **dependencies}
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(APP / path), "exec"), namespace)
    return SimpleNamespace(**namespace)


@pytest.mark.parametrize("identity", ["name", "id", "default"])
@pytest.mark.parametrize("cached", [False, True, "legacy"])
@pytest.mark.parametrize("limit,count,status", [(8, 8, 200), (8, 9, 400), (None, 9, 200)])
def test_limit_enforced_on_every_lookup_and_cache_path(identity, cached, limit, count, status):
    row = dict(f_model_id="1234567890123456789", f_model_name="reranker", f_model_type="reranker",
               f_model_config='{}', f_adapter=False, f_adapter_code=None, f_max_documents=limit)
    old = {key: value for key, value in row.items() if key != "f_max_documents"}
    redis = SimpleNamespace(get_str=AsyncMock(return_value=str([old if cached == "legacy" else row]) if cached else None),
                            set_str=AsyncMock())
    provider = SimpleNamespace(reranker=AsyncMock(return_value={"results": [{"index": 0}], "usage": {}}))
    loader = Mock(return_value=[row])
    module = load("controller/small_model_controller.py", {"reranker_model_used"},
                  dict(redis_util=redis, json=json, JSONResponse=JSONResponse, _load_small_model=loader,
                       RERANKER_MODEL_TYPE="reranker", _model_cache_ttl=lambda default: 60 if default else 86400,
                       InnerClient=Mock(return_value=provider), StandLogger=Mock(), get_logger=lambda: None,
                       ModelFactory_ExternalSmallModel_Used_InvalidParameter={"code": "ModelFactory.ExternalSmallModel.Used.InvalidParameter"},
                       ModelFactory_ExternalSmallModel_UnknownError={"code": "Unknown"},
                       permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=True))))
    request = SimpleNamespace(model="reranker" if identity == "name" else "",
                              model_id=row["f_model_id"] if identity == "id" else "",
                              query="query", documents=["document"] * count)
    response = asyncio.run(module.reranker_model_used(request, "user", "en-US", "admin", "test", private=False))
    if status == 400:
        assert response.status_code == 400
        body = json.loads(response.body)
        assert body["code"] == "ModelFactory.ExternalSmallModel.Used.InvalidParameter"
        assert "max_documents=8" in body["detail"]
        module.InnerClient.assert_not_called()
        provider.reranker.assert_not_awaited()
    else:
        assert response == {"results": [{"index": 0}], "usage": {}}
        provider.reranker.assert_awaited_once_with(query="query", documents=request.documents)
    assert loader.call_count == (0 if cached is True else 1)
    if cached == "legacy":
        redis.set_str.assert_awaited_once()
        assert "f_max_documents" in redis.set_str.call_args.kwargs["value"]


def test_dao_selects_limits_for_id_name_and_default():
    dao = load("dao/small_model_dao.py", {"SmallModelDao"}, {}, strip_methods=True).SmallModelDao()
    cursor = Mock()
    for name, args in (("get_model_info_by_id", ["id"]), ("get_model_info_by_name_id", ["name", ""]),
                       ("get_model_info_by_name_id", ["", "id"]), ("get_default_by_type", ["reranker"])):
        getattr(dao, name)(*args, None, cursor)
        assert "f_max_documents" in cursor.execute.call_args.args[0]
