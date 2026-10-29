# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Exercise real draft/save handlers without database, provider SDKs or bootstrap."""
import ast
import asyncio
import json
import math
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest
from fastapi.exceptions import RequestValidationError
from pydantic import BaseModel, Field, StrictStr, StrictBool, StrictInt, conint, root_validator, validator
from starlette.responses import JSONResponse
from typing import Optional

APP = Path(__file__).parents[1]
MODEL_ID = "1234567890123456789"
VALID = '''async def main(texts):
    return {"object": "list", "model": "test", "usage": {},
            "data": [{"embedding": [0.1, 0.2, 0.3, 0.4]} for text in texts]}
'''
INVALID = "async def main(texts):\n    return {"


def load(path, names, namespace):
    nodes = [node for node in ast.parse((APP / path).read_text()).body
             if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(APP / path), "exec"), namespace)
    return SimpleNamespace(**namespace)


SCHEMAS = load("interfaces/logics.py", {"TestSmallModel", "AddExternalSmallModel", "EditExternalSmallModel"},
               dict(globals()))
DTO = load("interfaces/dbaccess.py", {"AddExternalSmallModelInfo"}, dict(globals())).AddExternalSmallModelInfo
CLIENT = load("utils/external_small_model_utils.py", {"InnerClient"}, dict(globals())).InnerClient


def payload(code=VALID, **extra):
    return dict(model_name="Python adapter", model_type="embedding", adapter=True, adapter_code=code,
                batch_size=2, max_tokens=128, embedding_dim=4, **extra)


@pytest.fixture
def controller(monkeypatch):
    monkeypatch.setattr(asyncio, "to_thread", AsyncMock(side_effect=lambda fn, *args: fn(*args)))
    dao = Mock()
    dao.get_model_info_by_id.return_value = [{
        "f_model_id": MODEL_ID, "f_model_name": "Python adapter", "f_model_type": "embedding",
        "f_model_config": '{"api_key": "saved-secret"}', "f_adapter": True,
        "f_adapter_code": VALID, "f_batch_size": 2, "f_embedding_dim": 4,
    }]
    dao.name_check.return_value = []
    dao.find_model_by_duplicate_config.return_value = None
    dao.add_model_with_default.return_value = False
    namespace = dict(globals(), logics=SCHEMAS, dbaccess=SimpleNamespace(AddExternalSmallModelInfo=DTO),
                     InnerClient=CLIENT, small_model_dao=dao, StandLogger=Mock(),
                     worker=SimpleNamespace(get_id=lambda: MODEL_ID),
                     permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=True),
                         add_permission=AsyncMock(return_value=True)),
                     get_username_by_ids=AsyncMock(return_value={}), set_operation_audit_target=Mock(),
                     redis_util=SimpleNamespace(delete_str=AsyncMock()),
                     is_credential_digest=lambda candidate, saved: candidate == "digest",
                     MAX_EMBEDDING_TEST_BATCH_SIZE=128, EMBEDDING_TEST_TEXT="adapter test")
    tree = ast.parse((APP / "controller/small_model_controller.py").read_text())
    for node in ast.walk(tree):
        if isinstance(node, ast.Name) and node.id.startswith("ModelFactory_"):
            namespace[node.id] = {"code": node.id, "description": "Validation failed"}
    return load("controller/small_model_controller.py",
                {"test_model", "_validate_adapter_for_save", "add_model", "edit_model",
                 "_embedding_test_texts", "EmbeddingTestBatchSizeLimitError"}, namespace)


@pytest.mark.parametrize("saved,draft,status", [(VALID, INVALID, 400), (INVALID, VALID, 200)])
def test_edit_tests_current_draft_independently_of_key_change(controller, saved, draft, status):
    controller.small_model_dao.get_model_info_by_id.return_value[0]["f_adapter_code"] = saved
    request = SCHEMAS.TestSmallModel(**payload(draft, model_id=MODEL_ID, change=False))
    assert asyncio.run(controller.test_model(request, "user", "en-US", "admin")).status_code == status


def test_id_only_still_tests_saved_model(controller):
    request = SCHEMAS.TestSmallModel(model_id=MODEL_ID)
    assert asyncio.run(controller.test_model(request, "user", "en-US", "admin")).status_code == 200


@pytest.mark.parametrize("code", ["", INVALID, "async def main(texts): raise RuntimeError('broken')",
    "async def main(texts): return {}", "async def main(texts): return None",
    VALID.replace('[0.1, 0.2, 0.3, 0.4]', '[0.1]'),
    VALID.replace('[0.1, 0.2, 0.3, 0.4]', "['bad', 0, 0, 0]"),
    VALID.replace('for text in texts', 'for text in texts[:1]')])
@pytest.mark.parametrize("operation", ["add", "edit"])
def test_invalid_adapter_cannot_be_persisted(controller, code, operation):
    data = payload(code)
    if operation == "edit":
        data["model_id"] = MODEL_ID
    schema = SCHEMAS.AddExternalSmallModel if operation == "add" else SCHEMAS.EditExternalSmallModel
    if operation == "add" and not code:
        with pytest.raises(RequestValidationError):
            schema(**data)
    else:
        response = asyncio.run(getattr(controller, operation + "_model")(schema(**data), "user", "en-US", "admin"))
        assert response.status_code == 400
    controller.small_model_dao.add_model_with_default.assert_not_called()
    controller.small_model_dao.edit_model_info.assert_not_called()
    controller.permission_manager.add_permission.assert_not_called()
    controller.redis_util.delete_str.assert_not_called()


@pytest.mark.parametrize("operation", ["add", "edit"])
def test_valid_adapter_can_be_saved_even_when_saved_code_is_broken(controller, operation):
    controller.small_model_dao.get_model_info_by_id.return_value[0]["f_adapter_code"] = INVALID
    data = payload()
    if operation == "edit":
        data["model_id"] = MODEL_ID
    schema = SCHEMAS.AddExternalSmallModel if operation == "add" else SCHEMAS.EditExternalSmallModel
    response = asyncio.run(getattr(controller, operation + "_model")(schema(**data), "user", "en-US", "admin"))
    assert response.status_code == 200
    write = controller.small_model_dao.add_model_with_default if operation == "add" else controller.small_model_dao.edit_model_info
    assert write.call_args.args[0].adapter_code == VALID


@pytest.mark.parametrize("operation", ["add", "edit"])
def test_save_bounds_probe_batch_without_changing_config(controller, operation):
    data = payload()
    data["batch_size"] = 2048
    if operation == "edit":
        data["model_id"] = MODEL_ID
    schema = SCHEMAS.AddExternalSmallModel if operation == "add" else SCHEMAS.EditExternalSmallModel
    response = asyncio.run(getattr(controller, operation + "_model")(schema(**data), "user", "en-US", "admin"))
    assert response.status_code == 200
    write = controller.small_model_dao.add_model_with_default if operation == "add" else controller.small_model_dao.edit_model_info
    assert write.call_args.args[0].batch_size == 2048


def test_embedding_adapter_accepts_runtime_compatible_data_only_response(controller):
    code = '''async def main(texts):
    return {"data": [{"embedding": [0.1, 0.2, 0.3, 0.4]} for text in texts]}
'''
    request = SCHEMAS.TestSmallModel(**payload(code))
    assert asyncio.run(controller.test_model(request, "user", "en-US", "admin")).status_code == 200


def test_test_retains_key_without_mutating_request(controller):
    client = Mock(test_embedding=AsyncMock(return_value={"data": [{"embedding": [0] * 4}] * 2}))
    client.uses_single_input_embedding_requests.return_value = False
    factory = Mock(return_value=client)
    controller.test_model.__globals__["InnerClient"] = factory
    config = {"api_url": "https://example.invalid", "api_model": "test", "api_key": "digest"}
    request = SCHEMAS.TestSmallModel(**{**payload(model_id=MODEL_ID), "adapter": False,
        "adapter_code": None, "model_config": config})
    response = asyncio.run(controller.test_model(request, "user", "en-US", "admin"))
    assert response.status_code == 200
    assert factory.call_args.kwargs["api_key"] == "saved-secret"
    assert request.model_config["api_key"] == "digest"
    assert "saved-secret" not in response.body.decode()


@pytest.mark.parametrize("code,status", [
    ('async def main(query, documents): return {"object":"list", "model":"test", "usage":{}, "results":[{"relevance_score":0.5}]}', 200),
    ('async def main(query, documents): return {"results": []}', 400),
    ('async def main(query, documents): raise RuntimeError("broken")', 400),
])
def test_reranker_draft_validation(controller, code, status):
    data = {**payload(code, model_id=MODEL_ID), "model_type": "reranker", "embedding_dim": None}
    response = asyncio.run(controller.test_model(SCHEMAS.TestSmallModel(**data), "user", "en-US", "admin"))
    assert response.status_code == status


@pytest.mark.parametrize("change,key,expected", [
    (False, "digest", "saved-secret"), (True, "digest", "saved-secret"),
    (True, "replacement", "replacement"),
])
def test_edit_preserves_or_replaces_key_without_exposing_it(controller, change, key, expected):
    request = SCHEMAS.EditExternalSmallModel(
        model_id=MODEL_ID, model_name="HTTP embedding", model_type="embedding",
        model_config={"api_url": "https://example.invalid", "api_model": "test", "api_key": key},
        batch_size=2, max_tokens=128, embedding_dim=4, change=change)
    response = asyncio.run(controller.edit_model(request, "user", "en-US", "admin"))
    assert response.status_code == 200
    config = controller.small_model_dao.edit_model_info.call_args.args[0]
    assert config.model_config["api_key"] == expected
    assert expected not in response.body.decode()
