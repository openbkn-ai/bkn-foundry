# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Run the deletion handlers with an in-memory cache and isolated infrastructure."""
import ast
import asyncio
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest
from fastapi.exceptions import RequestValidationError
from starlette.responses import JSONResponse

DEFAULT_KEY = "dip:model-api:llm:default_model_3ed523:list"


@pytest.mark.parametrize("by_name", [False, True])
@pytest.mark.parametrize("names", [["A"], ["A", "B"], ["B"]])
@pytest.mark.parametrize("cache_fails", [False, True])
def test_delete_invalidates_all_lookup_keys_after_commit(by_name, names, cache_fails):
    rows = [{"f_model_id": "id-" + name, "f_model_name": name} for name in ("A", "B")]
    keys = {DEFAULT_KEY, *(f"dip:model-api:llm:{value}:list"
                           for name in ("A", "B") for value in (name, "id-" + name))}
    committed = []

    def delete(ids):
        committed.extend(ids)

    async def invalidate(deleted_keys):
        assert committed == ["id-" + name for name in names]
        if cache_fails:
            raise RuntimeError("cache unavailable")
        keys.difference_update(deleted_keys)

    cache = SimpleNamespace(delete_str=AsyncMock(side_effect=invalidate))
    namespace = dict(
        redis_util=None, get_redis_util=AsyncMock(return_value=cache),
        llm_model_dao=SimpleNamespace(get_all_model_list=lambda: rows, delete_model_by_id=Mock(side_effect=delete)),
        model_quota_dao=Mock(), quota_config_cache_tree=Mock(),
        permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=True),
                                           delete_permission=AsyncMock()),
        set_operation_audit_target=Mock(), StandLogger=Mock(), JSONResponse=JSONResponse,
        RequestValidationError=RequestValidationError, LLMRemoveError={},
    )
    source = Path(__file__).parents[1] / "controller/llm_controller.py"
    functions = {"remove_model", "remove_model_by_name", "invalidate_deleted_llm_cache"}
    nodes = [node for node in ast.parse(source.read_text()).body
             if isinstance(node, ast.AsyncFunctionDef) and node.name in functions]
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(source), "exec"), namespace)
    payload = {"model_names": names} if by_name else {"model_ids": ["id-" + name for name in names]}
    handler = namespace["remove_model_by_name" if by_name else "remove_model"]
    response = asyncio.run(handler(payload, "user", "zh-CN"))
    assert response.status_code == 200
    expected = {DEFAULT_KEY, *(f"dip:model-api:llm:{value}:list"
                               for name in names for value in (name, "id-" + name))}
    assert set(cache.delete_str.call_args.args[0]) == expected
    if cache_fails:
        namespace["StandLogger"].error.assert_called_once()
        assert "deletion committed" in namespace["StandLogger"].error.call_args.args[0]
    else:
        assert not expected & keys
        namespace["StandLogger"].error.assert_not_called()
    if not by_name:
        namespace["quota_config_cache_tree"].delete_batch.assert_called_once()
