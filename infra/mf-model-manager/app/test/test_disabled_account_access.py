# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Exercise real request functions with isolated database/provider clients.

Loading only the functions avoids starting model monitors and importing every
provider SDK. This follows the controller isolation used by test_operation_audit.
"""

import ast
import json
import re
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import aiohttp
from fastapi import Body, Query
from starlette.requests import Request
from starlette.responses import JSONResponse


APP = Path(__file__).parents[1]


def functions(path, names, **dependencies):
    module = ast.parse("from __future__ import annotations\n")
    for node in ast.parse((APP / path).read_text()).body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)) and node.name in names:
            node.decorator_list = []
            module.body.append(node)
    exec(compile(module, str(APP / path), "exec"), dependencies)
    return SimpleNamespace(**dependencies)


def response_context(response):
    context = AsyncMock()
    context.__aenter__.return_value = response
    return context


class TestDisabledAccountRequests(unittest.IsolatedAsyncioTestCase):
    async def test_existing_token_requires_current_account_before_handler(self):
        for path in ("/llm/test", "/small-model/test", "/small-model/get_default"):
            for enabled in (False, True, None):
                with self.subTest(path=path, enabled=enabled):
                    permission = SimpleNamespace(is_active_account=AsyncMock(
                        return_value=enabled, side_effect=RuntimeError("unavailable") if enabled is None else None))
                    response = SimpleNamespace(status=200, text=AsyncMock(return_value=json.dumps({
                        "active": True, "sub": "user-1", "client_id": "studio",
                    })))
                    session = SimpleNamespace(post=Mock(return_value=response_context(response)))
                    module = functions("utils/app_utils.py", {"auth_middleware", "_set_trusted_identity"},
                        json=json, JSONResponse=JSONResponse, permission_manager=permission,
                        _IDENTITY_HEADERS=(b"x-account-id", b"x-account-type"),
                        aiohttp=SimpleNamespace(ClientSession=Mock(return_value=response_context(session))),
                        base_config=SimpleNamespace(OAUTHADMINHOST="hydra", OAUTHADMINPORT=4445),
                        internal_request_headers=lambda: {}, UnauthorizedError={"code": "Unauthorized"},
                        AccountStateUnavailableError={"code": "ModelFactory.AccountStateUnavailable"},
                        HydraServiceError={"code": "HydraUnavailable"})
                    request = Request({"type": "http", "path": "/api/mf-model-manager/v1" + path,
                                       "headers": [(b"authorization", b"Bearer old-token")]})
                    handler = AsyncMock(return_value=JSONResponse({"ok": True}))
                    result = await module.auth_middleware(request, handler)
                    self.assertEqual(result.status_code, 503 if enabled is None else (200 if enabled else 401))
                    self.assertEqual(handler.await_count, 1 if enabled else 0)
                    permission.is_active_account.assert_awaited_once_with("user-1")

    async def test_account_lookup_uses_live_state_and_refuses_missing_accounts(self):
        module = functions("utils/permission_manager.py", {"PermissionManager"},
                           bkn_safe_url=lambda: "http://safe", aiohttp=aiohttp,
                           internal_request_headers=lambda headers: headers)
        manager = module.PermissionManager()
        response = SimpleNamespace(status=200, json=AsyncMock())
        session = SimpleNamespace(post=Mock(return_value=response_context(response)))
        manager.get_session = AsyncMock(return_value=session)
        for users, expected in (([{"id": "user-1", "enabled": True}], True),
                                ([{"id": "user-1", "enabled": False}], False),
                                ([], False), ([{"id": "different", "enabled": True}], False)):
            response.json.return_value = {"users": users}
            self.assertEqual(await manager.is_active_account("user-1"), expected)
        self.assertEqual(session.post.call_count, 4)
        self.assertEqual(session.post.call_args.kwargs["json"], {"user_ids": ["user-1"]})
        response.status = 503
        with self.assertRaises(RuntimeError):
            await manager.is_active_account("user-1")

    async def test_default_route_forwards_verified_identity(self):
        controller = SimpleNamespace(get_default_model=AsyncMock(return_value=JSONResponse({})))
        public = functions("routers/small_model_router.py", {"get_default"}, Query=Query,
                           get_user_info=AsyncMock(return_value=("user-1", "en-US", "user")),
                           small_model_controller=controller)
        await public.get_default(Mock(), "embedding")
        controller.get_default_model.assert_awaited_once_with("embedding", "user-1", "user")
        controller.get_default_model.reset_mock()
        private = functions("routers/private_route.py", {"get_default"}, Query=Query,
                            small_model_controller=controller)
        await private.get_default(Mock(), "embedding")
        controller.get_default_model.assert_awaited_once_with("embedding", trusted_internal=True)

    async def test_configuration_tests_check_permission_before_controller(self):
        for small in (False, True):
            for model_id in (None, "saved-model"):
                for allowed in (False, True):
                    with self.subTest(small=small, model_id=model_id, allowed=allowed):
                        check = AsyncMock(return_value=allowed)
                        controller = AsyncMock(return_value=JSONResponse({"status": "ok"}))
                        module = functions("routers/small_model_router.py" if small else "routers/llm_router.py",
                            {"model_used"} if small else {"test_llm_"},
                            Query=Query, Body=Body, JSONResponse=JSONResponse,
                            get_user_info=AsyncMock(return_value=("user-1", "en-US", "user")),
                            permission_manager=SimpleNamespace(check_single_permission=check),
                            small_model_controller=SimpleNamespace(test_model=controller),
                            test_model=controller, NotPermissionError={"code": "Forbidden"})
                        result = (await module.model_used(SimpleNamespace(model_id=model_id), Mock())
                                  if small else await module.test_llm_(Mock(), {"model_id": model_id}))
                        self.assertEqual(result.status_code, 200 if allowed else 403)
                        self.assertEqual(controller.await_count, 1 if allowed else 0)
                        self.assertEqual(check.call_args.kwargs["resource_id"], model_id or "*")
                        self.assertEqual(check.call_args.kwargs["operations"], "execute")

    async def test_default_reads_cover_denial_no_default_and_both_model_types(self):
        for model_type in ("embedding", "reranker"):
            row = {"f_model_id": "default-1", "f_model_name": "Default", "f_model_type": model_type,
                   "f_model_config": '{"api_key": "secret"}', "f_batch_size": 16,
                   "f_max_tokens": 512, "f_embedding_dim": 768}
            for permissions, rows, status in (([False], [row], 403), ([False], [], 403),
                                               ([True, False], [row], 403), ([True], [], 200),
                                               ([True, True], [row], 200)):
                with self.subTest(model_type=model_type, permissions=permissions, rows=bool(rows)):
                    check = AsyncMock(side_effect=permissions)
                    dao = Mock(return_value=rows)
                    module = functions("controller/small_model_controller.py", {"get_default_model"},
                        permission_manager=SimpleNamespace(check_single_permission=check),
                        small_model_dao=SimpleNamespace(get_default_by_type=dao), JSONResponse=JSONResponse,
                        json=json, re=re, NotPermissionError={"code": "Forbidden"},
                        StandLogger=Mock(), ModelFactory_ExternalSmallModel_UnknownError={"code": "Error"})
                    result = await module.get_default_model(model_type, "user-1", "user")
                    self.assertEqual(result.status_code, status)
                    body = json.loads(result.body)
                    if not permissions[0]:
                        dao.assert_not_called()
                    if status == 200 and not rows:
                        self.assertEqual(body, {})
                    if status == 403:
                        self.assertNotIn("model_id", body)
                    self.assertNotIn("secret", result.body.decode())

    async def test_private_default_read_has_explicit_internal_contract(self):
        dao = Mock(return_value=[])
        check = AsyncMock(return_value=False)
        module = functions("controller/small_model_controller.py", {"get_default_model"},
            JSONResponse=JSONResponse, permission_manager=SimpleNamespace(check_single_permission=check),
            small_model_dao=SimpleNamespace(get_default_by_type=dao))
        result = await module.get_default_model("embedding", trusted_internal=True)
        self.assertEqual(result.status_code, 200)
        check.assert_not_awaited()


if __name__ == "__main__":
    unittest.main()
