# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest
from unittest import mock

from app.core.config import base_config
from app.utils.permission_manager import PermissionManager


class _Response:
    def __init__(self, payload, status=200):
        self.status = status
        self.payload = payload

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def json(self):
        return self.payload


class _Session:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    def post(self, url, json=None, headers=None):
        self.calls.append({"url": url, "json": json, "headers": headers})
        return self.responses.pop(0)


class TestPermissionManagerAuthz(unittest.IsolatedAsyncioTestCase):
    def manager(self, session):
        with mock.patch.dict(
                "os.environ",
                {"AUTHZ_PROVIDER": "bkn-safe", "BKN_SAFE_URL": "http://bkn-safe"},
                clear=False):
            manager = PermissionManager()
        manager.get_session = mock.AsyncMock(return_value=session)
        return manager

    async def test_runtime_check_uses_effective_scope_and_string_resource_id(self):
        session = _Session([_Response({"allowed": False})])
        manager = self.manager(session)
        with mock.patch.object(base_config, "AUTH_ENABLED", True):
            allowed = await manager.check_single_permission(
                "user-1", 42, "execute", "small_model", "user")

        self.assertFalse(allowed)
        self.assertEqual(session.calls[0]["json"], {
            "accessor_id": "user-1",
            "resource": {"type": "small_model", "id": "42"},
            "operation": "execute",
            "evaluation_scope": "effective",
        })

    async def test_small_model_selector_uses_one_effective_batch_filter(self):
        session = _Session([_Response({"resources": [{
            "resource_type": "small_model", "resource_id": "42", "operations": ["display"],
        }]})])
        manager = self.manager(session)
        with mock.patch.object(base_config, "AUTH_ENABLED", True), \
                mock.patch("app.utils.permission_manager.small_model_dao.get_all_ids",
                           return_value=[{"f_model_id": 42}, {"f_model_id": 84}]):
            result = await manager.get_permission_ids(
                "user-1", "display", "small_model", "小模型", "user")

        self.assertEqual(result, [42])
        self.assertEqual(len(session.calls), 1)
        self.assertEqual(session.calls[0]["url"], "http://bkn-safe/api/safe/v1/authz/resource-filter")
        self.assertEqual(session.calls[0]["json"]["resources"], [
            {"type": "small_model", "id": "42"},
            {"type": "small_model", "id": "84"},
        ])
        self.assertEqual(session.calls[0]["json"]["evaluation_scope"], "effective")

    async def test_invalid_safe_check_response_fails_closed(self):
        manager = self.manager(_Session([_Response({"decision": "allow"})]))
        with mock.patch.object(base_config, "AUTH_ENABLED", True):
            allowed = await manager.check_single_permission(
                "user-1", "model-1", "execute", "small_model", "user")
        self.assertFalse(allowed)

    async def test_invalid_batch_filter_response_fails_closed(self):
        manager = self.manager(_Session([_Response({"result": []})]))
        with mock.patch.object(base_config, "AUTH_ENABLED", True), \
                mock.patch("app.utils.permission_manager.small_model_dao.get_all_ids",
                           return_value=[{"f_model_id": 42}]), \
                self.assertRaises(RuntimeError):
            await manager.get_permission_ids(
                "user-1", "display", "small_model", "小模型", "user")


if __name__ == "__main__":
    unittest.main()
