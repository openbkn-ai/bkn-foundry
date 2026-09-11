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

    async def test_runtime_check_uses_real_resource_operation_and_effective_scope(self):
        session = _Session([_Response({"allowed": True})])
        manager = self.manager(session)
        with mock.patch.object(base_config, "AUTH_ENABLED", True):
            allowed = await manager.check_single_permission(
                "user-1", 1234567890123456789, "execute", "large_model", "user")

        self.assertTrue(allowed)
        self.assertEqual(session.calls[0]["url"], "http://bkn-safe/api/safe/v1/authz/check")
        self.assertEqual(session.calls[0]["json"], {
            "accessor_id": "user-1",
            "resource": {"type": "large_model", "id": "1234567890123456789"},
            "operation": "execute",
            "evaluation_scope": "effective",
        })

    async def test_model_list_uses_one_effective_batch_filter_and_preserves_id_types(self):
        session = _Session([_Response({"resources": [{
            "resource_type": "large_model", "resource_id": "42", "operations": ["display"],
        }]})])
        manager = self.manager(session)

        with mock.patch.object(base_config, "AUTH_ENABLED", True):
            result = await manager.filter_authorized_ids(
                "user-1", "user", [42, "84"], "large_model", "大模型", "display")

        self.assertEqual(result, [42])
        self.assertEqual(len(session.calls), 1)
        self.assertEqual(session.calls[0]["url"], "http://bkn-safe/api/safe/v1/authz/resource-filter")
        self.assertEqual(session.calls[0]["json"]["resources"], [
            {"type": "large_model", "id": "42"},
            {"type": "large_model", "id": "84"},
        ])
        self.assertEqual(session.calls[0]["json"]["visibility_operations"], ["display"])
        self.assertEqual(session.calls[0]["json"]["evaluation_scope"], "effective")

    async def test_invalid_or_failed_safe_response_fails_closed(self):
        for response in [_Response({}, status=503), _Response({"decision": "allow"})]:
            with self.subTest(response=response.payload):
                manager = self.manager(_Session([response]))
                with mock.patch.object(base_config, "AUTH_ENABLED", True):
                    allowed = await manager.check_single_permission(
                        "user-1", "model-1", "execute", "small_model", "user")
                self.assertFalse(allowed)

    async def test_invalid_batch_filter_response_fails_closed(self):
        manager = self.manager(_Session([_Response({"result": []})]))
        with mock.patch.object(base_config, "AUTH_ENABLED", True), \
                self.assertRaises(RuntimeError):
            await manager.filter_authorized_ids(
                "user-1", "user", ["model-1"], "large_model", "大模型", "display")


if __name__ == "__main__":
    unittest.main()
