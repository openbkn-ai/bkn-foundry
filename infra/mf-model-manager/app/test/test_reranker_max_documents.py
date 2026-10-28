# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

"""Regression for #2399, isolating real handlers from provider SDK/bootstrap imports."""
import ast
import asyncio
import datetime
import json
import re
from contextlib import contextmanager
from pathlib import Path
from types import SimpleNamespace
from typing import Optional
from unittest.mock import AsyncMock, Mock

import pytest
from fastapi.exceptions import RequestValidationError
from pydantic import BaseModel, Field, StrictStr, StrictBool, StrictInt, conint, root_validator, validator
from starlette.responses import JSONResponse

APP = Path(__file__).parents[1]


def load(path, names, dependencies, strip_methods=False):
    nodes = [node for node in ast.parse((APP / path).read_text()).body
             if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
    if strip_methods:
        for node in nodes:
            for method in node.body:
                if isinstance(method, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    method.decorator_list = []
    namespace = {"__name__": __name__, **dependencies}
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(APP / path), "exec"), namespace)
    return SimpleNamespace(**namespace)


SCHEMAS = load("interfaces/logics.py", {"AddExternalSmallModel", "EditExternalSmallModel", "TestSmallModel"}, globals())
DTO = load("interfaces/dbaccess.py", {"AddExternalSmallModelInfo"}, globals()).AddExternalSmallModelInfo


def payload(model_type="reranker"):
    return dict(model_name="reranker", model_type=model_type, batch_size=8, max_tokens=128,
                embedding_dim=768 if model_type == "embedding" else None,
                model_config={"api_url": "https://provider.invalid", "api_model": "test"})


@pytest.mark.parametrize("schema_name", ["AddExternalSmallModel", "EditExternalSmallModel", "TestSmallModel"])
@pytest.mark.parametrize("value", [0, -1, 1.5, True, "8", 2147483648])
def test_invalid_limits_are_rejected(schema_name, value):
    data = payload()
    if schema_name == "EditExternalSmallModel":
        data["model_id"] = "1234567890123456789"
    with pytest.raises(ValueError):
        getattr(SCHEMAS, schema_name)(**data, max_documents=value)


@pytest.mark.parametrize("schema_name", ["AddExternalSmallModel", "EditExternalSmallModel", "TestSmallModel"])
def test_valid_and_legacy_requests(schema_name):
    data = payload()
    if schema_name == "EditExternalSmallModel":
        data["model_id"] = "1234567890123456789"
    schema = getattr(SCHEMAS, schema_name)
    for value in (None, 1, 8, 2147483647):
        assert schema(**data, max_documents=value).dict()["max_documents"] == value
    assert schema(**data).max_documents is None
    adapter_data = {key: value for key, value in data.items() if key != "model_config"}
    assert schema(**adapter_data, adapter=True, adapter_code="adapter", max_documents=8).max_documents == 8
    data.update(model_type="embedding", embedding_dim=768)
    with pytest.raises(ValueError, match="only supported for reranker"):
        schema(**data, max_documents=8)
    assert schema(**data).embedding_dim == 768


def controller(dao):
    return load("controller/small_model_controller.py",
                {"add_model", "edit_model", "get_info", "get_info_by_name", "get_info_list", "get_default_model"},
                dict(logics=SCHEMAS, dbaccess=SimpleNamespace(AddExternalSmallModelInfo=DTO),
                     asyncio=SimpleNamespace(to_thread=AsyncMock(side_effect=lambda fn, *args: fn(*args))),
                     json=json, re=re, JSONResponse=JSONResponse,
                     worker=SimpleNamespace(get_id=lambda: 1234567890123456789),
                     small_model_dao=dao, StandLogger=Mock(),
                     permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=True),
                         add_permission=AsyncMock(return_value=True), get_permission_ids=AsyncMock(return_value=["id"])),
                     get_username_by_ids=AsyncMock(return_value={}), get_userid_by_search=AsyncMock(return_value=[]),
                     set_operation_audit_target=Mock(),
                     redis_util=SimpleNamespace(delete_str=AsyncMock()), is_credential_digest=lambda *args: False,
                     ModelFactory_MyPymysqlPool_Connection_ConnectError_Error={"description": "DB error"},
                     ModelFactory_ExternalSmallModel_UnknownError={"code": "Unknown"}))


def row():
    return dict(f_model_id="1234567890123456789", f_model_name="reranker", f_model_type="reranker",
                f_model_config=json.dumps(payload()["model_config"]), f_adapter=False, f_adapter_code=None,
                f_batch_size=8, f_max_tokens=128, f_embedding_dim=None, f_default=1,
                f_create_by="user", f_update_by="user", f_create_time=datetime.datetime.now(),
                f_update_time=datetime.datetime.now(), f_max_documents=8)


def test_create_edit_and_all_read_paths_round_trip():
    saved = row()
    dao = Mock()
    dao.name_check.return_value = []
    dao.find_model_by_duplicate_config.return_value = None
    dao.add_model_with_default.return_value = True
    dao.get_model_info_by_id.return_value = [saved]
    dao.get_model_info_by_name.return_value = [saved]
    dao.get_model_info_list.return_value = [saved]
    dao.get_model_info_total.return_value = [saved]
    dao.get_default_by_type.return_value = [saved]
    module = controller(dao)
    response = asyncio.run(module.add_model(SCHEMAS.AddExternalSmallModel(**payload(), max_documents=8),
                                           "user", "en-US", "admin"))
    assert response.status_code == 200
    assert dao.add_model_with_default.call_args.args[0].max_documents == 8
    for extra, expected in (({}, 8), ({"max_documents": 16}, 16), ({"max_documents": None}, None)):
        request = SCHEMAS.EditExternalSmallModel(**payload(), model_id=saved["f_model_id"], **extra)
        response = asyncio.run(module.edit_model(request, "user", "en-US", "admin"))
        assert response.status_code == 200
        assert dao.edit_model_info.call_args.args[0].max_documents == expected
    cleared = module.redis_util.delete_str.call_args.args[0]
    assert set(cleared) == {f"dip:model-api:small-model:{key}:list" for key in
                            ("reranker", saved["f_model_id"], "default:reranker")}
    responses = [
        asyncio.run(module.get_info(saved["f_model_id"], "user", "admin")),
        asyncio.run(module.get_info_by_name("reranker")),
        asyncio.run(module.get_default_model("reranker", trusted_internal=True)),
        asyncio.run(module.get_info_list("desc", "create_time", 1, 10, "", "reranker", "", "user", "admin")),
    ]
    for response in responses:
        assert response.status_code == 200
        body = json.loads(response.body)
        if "data" in body:
            body = body["data"][0]
        assert body["max_documents"] == 8
        assert body["batch_size"] == 8
        assert body["max_tokens"] == 128


def test_dao_writes_reads_and_duplicate_identity_include_limit():
    cursor = Mock()
    @contextmanager
    def transaction(*args):
        yield cursor
    dao = load("dao/small_model_dao.py", {"SmallModelDao"},
               dict(AddExternalSmallModelInfo=DTO, datetime=datetime, json=json,
                    advisory_lock_transaction=transaction, para_dict={"create_time": "f_create_time"}),
               strip_methods=True).SmallModelDao()
    config = DTO(**payload(), model_id="1234567890123456789", max_documents=8)
    for write in (lambda: dao.add_model_info(config, "user", None, cursor),
                  lambda: dao.edit_model_info(config, "user", None, cursor),
                  lambda: dao.add_model_with_default(config, "user", True)):
        cursor.fetchall.return_value = []
        write()
        sql, args = cursor.execute.call_args.args
        assert sql.count("%s") == len(args)
        if "insert" in sql:
            columns = sql[sql.index("(") + 1:sql.index(")")].replace("\n", "").split(",")
            assert dict(zip(map(str.strip, columns), args))["f_max_documents"] == 8
        else:
            assert "f_max_documents = %s" in sql
            assert args[-2] == 8
    for name, args in (("get_model_info_by_id", [config.model_id]), ("get_model_info_by_name", ["reranker"]),
                       ("get_default_by_type", ["reranker"]),
                       ("get_model_info_list", [1, 10, "desc", "create_time", "", "", "", []])):
        getattr(dao, name)(*args, None, cursor)
        assert "f_max_documents" in cursor.execute.call_args.args[0]
    stored = row()
    stored["f_max_documents"] = 16
    cursor.fetchall.return_value = [stored]
    assert dao.find_model_by_duplicate_config(config, None, cursor) is None
    stored["f_max_documents"] = 8
    assert dao.find_model_by_duplicate_config(config, None, cursor)["id"] == config.model_id
