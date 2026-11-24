# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Resolve actual LLM cache paths without contacting a provider."""
import ast
import asyncio
import json
import time
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest
from starlette.responses import JSONResponse


@pytest.mark.parametrize("identity", ["default", "name", "id"])
@pytest.mark.parametrize("cached", [None, "invalid", "valid"])
def test_lookup_cache_ttl_and_hit_behavior(identity, cached):
    row = dict(f_model_id="id-A", f_model_series="openai", f_max_model_len=8, f_quota=False)
    cache = SimpleNamespace(get_str=AsyncMock(return_value=None if cached is None else
                            json.dumps([row]) if cached == "valid" else "invalid"), set_str=AsyncMock())
    dao = Mock()
    dao.get_data_from_default_model.return_value = [row]
    dao.get_data_from_model_list_by_name_id.return_value = [row]
    namespace = dict(redis_util=cache, llm_model_dao=dao, time=time, StandLogger=Mock(),
                     permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=False)),
                     NotPermissionError={}, envelope_error_response=lambda body, code: JSONResponse(body, code))
    source = Path(__file__).parents[1] / "controller/llm_controller.py"
    nodes = [node for node in ast.parse(source.read_text()).body
             if isinstance(node, ast.AsyncFunctionDef) and node.name == "used_model_openai"]
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(source), "exec"), namespace)
    request = dict(model="A" if identity == "name" else "", model_id="id-A" if identity == "id" else "")
    response = asyncio.run(namespace["used_model_openai"](request, "user", "en-US", "test", private=False))
    assert response.status_code == 403  # Stop before provider execution.
    key = "dip:model-api:llm:" + {"default": "default_model_3ed523", "name": "A", "id": "id-A"}[identity] + ":list"
    cache.get_str.assert_awaited_once_with(key)
    if cached == "valid":
        assert not dao.mock_calls
        cache.set_str.assert_not_awaited()
    else:
        cache.set_str.assert_awaited_once_with(key=key, value=json.dumps([row]),
                                              expire=60 if identity == "default" else 86400)
        if identity == "default":
            dao.get_data_from_default_model.assert_called_once_with()
        else:
            dao.get_data_from_model_list_by_name_id.assert_called_once_with(request["model"], request["model_id"])
