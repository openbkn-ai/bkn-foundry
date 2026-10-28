# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Exercise the real add route and handlers without service bootstrap or providers."""

import ast
import asyncio
import json
import re
from copy import deepcopy
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import httpx
import pytest
from fastapi import APIRouter, Body, FastAPI, Request, status
from fastapi.exceptions import RequestValidationError
from starlette.responses import JSONResponse

from app.commons.errors import LLMAdd2Error
from app.commons.locale import LocaleResponseMiddleware, error_with_message


APP = Path(__file__).parents[1]
OMITTED = object()
PARAMETER_ERROR = "ModelFactory.ConnectController.LLMAdd.ParameterError"


def load_functions(path, names, namespace):
    tree = ast.parse((APP / path).read_text())
    nodes = [node for node in tree.body
             if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
    assert {node.name for node in nodes} == names
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(APP / path), "exec"), namespace)


@pytest.fixture
def registration():
    dao = Mock()
    dao.get_model_by_name.return_value = []
    dao.find_model_by_unique_config.return_value = None
    dao.add_data_with_default.return_value = False
    permission = SimpleNamespace(
        check_single_permission=AsyncMock(return_value=True),
        add_permission=AsyncMock(return_value=True), delete_permission=AsyncMock(),
    )
    namespace = dict(
        re=re, asyncio=SimpleNamespace(to_thread=AsyncMock(side_effect=lambda fn, *args: fn(*args))),
        json=json, status=status, JSONResponse=JSONResponse,
        RequestValidationError=RequestValidationError, Request=Request, Body=Body,
        llm_route=APIRouter(), llm_model_dao=dao, permission_manager=permission,
        LLMAdd2Error=LLMAdd2Error, error_with_message=error_with_message,
        StandLogger=Mock(), redis_util=None, worker=SimpleNamespace(get_id=Mock(return_value=123)),
        model_quota_dao=Mock(), quota_config_cache_tree=Mock(),
        get_username_by_ids=AsyncMock(return_value={"owner": "Owner"}),
        set_operation_audit_target=Mock(), operation_audit_request=Mock(return_value=None),
    )
    load_functions("utils/common.py", {"validate_required_params", "get_user_info"}, namespace)
    load_functions("utils/param_verify_utils.py", {"llm_add_verify"}, namespace)
    load_functions("controller/llm_controller.py", {"add_model", "can_manage_default_llm"}, namespace)
    load_functions("routers/llm_router.py", {"add_llm"}, namespace)
    app = FastAPI()
    app.include_router(namespace["llm_route"], prefix="/api/mf-model-manager/v1")
    app.add_middleware(LocaleResponseMiddleware)
    body = dict(
        model_name="Issue2356", model_series="openai", model_type="llm",
        max_model_len=4096, quota=False, default=True,
        model_config=dict(api_url="https://provider.invalid/v1", api_key="synthetic-key", api_model="gpt-4o"),
    )
    return SimpleNamespace(app=app, body=body, dao=dao, permission=permission, namespace=namespace)


def post(registration, locale="en-US"):
    async def request():
        async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=registration.app), base_url="http://test") as client:
            return await client.post(
                "/api/mf-model-manager/v1/llm/add", json=registration.body,
                headers={"Accept-Language": locale, "x-account-id": "owner"})
    return asyncio.run(request())


def assert_rejected(registration, response, parameter):
    assert response.status_code == 400
    content = response.json()
    assert content["code"] == PARAMETER_ERROR
    assert parameter in content["detail"]
    assert "_i18n_key" not in content
    registration.dao.add_data_with_default.assert_not_called()
    registration.dao.find_model_by_unique_config.assert_not_called()
    registration.permission.add_permission.assert_not_awaited()
    registration.namespace["worker"].get_id.assert_not_called()
    assert not registration.namespace["model_quota_dao"].mock_calls
    registration.namespace["quota_config_cache_tree"].add.assert_not_called()
    registration.namespace["set_operation_audit_target"].assert_not_called()


@pytest.mark.parametrize("value", [OMITTED, None, "", "   ", "\t\n", "\u2003", 0, False, [], {}])
@pytest.mark.parametrize("locale", ["en-US", "zh-CN"])
def test_invalid_api_model_returns_400_without_writes(registration, value, locale):
    if value is OMITTED:
        del registration.body["model_config"]["api_model"]
    else:
        registration.body["model_config"]["api_model"] = value
    original = deepcopy(registration.body)
    response = post(registration, locale)
    assert_rejected(registration, response, "model_config.api_model")
    assert response.headers["content-language"] == locale
    if locale == "en-US":
        expected = ("Missing required parameter: model_config.api_model" if value is OMITTED
                    else "model_config.api_model must be a non-empty string.")
        assert response.json()["detail"] == expected
    registration.dao.get_model_by_name.assert_not_called()
    assert registration.body == original


@pytest.mark.parametrize("value", [OMITTED, None, [], "bad", 1, False])
def test_invalid_model_config_returns_400(registration, value):
    if value is OMITTED:
        del registration.body["model_config"]
    else:
        registration.body["model_config"] = value
    response = post(registration)
    assert_rejected(registration, response, "model_config")
    registration.dao.get_model_by_name.assert_not_called()


@pytest.mark.parametrize("value", ["a" * 51, "bad\tmodel"])
def test_existing_api_model_format_validation_is_preserved(registration, value):
    registration.body["model_config"]["api_model"] = value
    assert_rejected(registration, post(registration), "model_config.api_model")


def test_valid_registration_reaches_persistence(registration):
    registration.body["default"] = False
    response = post(registration)
    assert response.status_code == 200
    assert response.json() == {"status": "ok", "id": "123", "default": False}
    registration.dao.add_data_with_default.assert_called_once()
    args = registration.dao.add_data_with_default.call_args.args
    assert args[4] == "gpt-4o"
    assert json.loads(args[6]) == registration.body["model_config"]
    assert args[-1] is False
    registration.permission.add_permission.assert_awaited_once()
