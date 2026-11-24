"""Exercise the private model lookup contract without service bootstrap."""

import ast
import asyncio
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

from fastapi import APIRouter, FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.testclient import TestClient


APP = Path(__file__).parents[1]
NOT_FOUND = {"code": "ModelFactory.ExternalSmallModel.GetInfo.IdNotExist", "description": "missing"}


def load(path, name, namespace, predicate=lambda node: True):
    nodes = [node for node in ast.parse((APP / path).read_text()).body
             if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
             and node.name == name and predicate(node)]
    assert len(nodes) == 1
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(APP / path), "exec"), namespace)


def test_private_lookup_status_contract():
    dao = SimpleNamespace(get_model_info_by_id=Mock(return_value=[]))
    permission = SimpleNamespace(check_single_permission=AsyncMock(return_value=True))
    context = dict(JSONResponse=JSONResponse, small_model_dao=dao, permission_manager=permission,
                   StandLogger=Mock(), ModelFactory_ExternalSmallModel_GetInfo_IdNotExist_Error=NOT_FOUND,
                   ModelFactory_MyPymysqlPool_Connection_ConnectError_Error={"code": "database"},
                   ModelFactory_ExternalSmallModel_UnknownError={"code": "unknown"},
                   NotPermissionError={"code": "denied"})
    load("controller/small_model_controller.py", "get_info", context)
    private = APIRouter()
    route_context = dict(private_route=private, Request=Request,
                         get_user_info=AsyncMock(side_effect=lambda request: (
                             request.headers.get("x-account-id", ""), "en", request.headers.get("x-account-type", ""))),
                         small_model_controller=SimpleNamespace(get_info=context["get_info"]))
    load("routers/private_route.py", "get_info", route_context,
         lambda node: any(isinstance(d, ast.Call) and d.args and isinstance(d.args[0], ast.Constant)
                          and d.args[0].value == "/small-model/get" for d in node.decorator_list))
    empty = APIRouter()
    router_context = dict(Request=Request, RequestValidationError=RequestValidationError, JSONResponse=JSONResponse,
                          llm_router=SimpleNamespace(health_route=empty, llm_route=empty),
                          open_api_json=SimpleNamespace(open_json_router=empty),
                          small_model_router=SimpleNamespace(small_model_router=empty),
                          private_route=SimpleNamespace(private_route=private),
                          prompt_router=SimpleNamespace(prompt_route=empty),
                          model_quota_router=SimpleNamespace(model_quota_router=empty),
                          api_version_health="/health", api_version_public_v1="/public",
                          api_version_private_v1="/private")
    load("routers/__init__.py", "router_init", router_context)
    app = FastAPI()
    router_context["router_init"](app)
    client = TestClient(app)

    response = client.get("/private/small-model/get", params={"model_id": "missing"},
                          headers={"x-account-id": "app", "x-account-type": "app"})
    assert response.status_code == 404
    assert response.json() == NOT_FOUND
    permission.check_single_permission.assert_not_awaited()

    public_response = asyncio.run(context["get_info"]("missing", "user", "user"))
    assert public_response.status_code == 400

    response = client.get("/private/small-model/get")
    assert response.status_code == 400
    assert response.json()["code"] == "ModelFactory.Router.ParamError.ParamMissing"

    dao.get_model_info_by_id.return_value = [{}]
    response = client.get("/private/small-model/get", params={"model_id": "broken"},
                          headers={"x-account-type": "app"})
    assert response.status_code == 400
    assert response.json()["code"] == "unknown"

    permission.check_single_permission.return_value = False
    dao.get_model_info_by_id.reset_mock()
    response = client.get("/private/small-model/get", params={"model_id": "missing"},
                          headers={"x-account-id": "user", "x-account-type": "user"})
    assert response.status_code == 403
    dao.get_model_info_by_id.assert_not_called()
