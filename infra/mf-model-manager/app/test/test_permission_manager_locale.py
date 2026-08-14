# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest
from unittest import mock

from app.core.config import base_config
from app.commons.locale import reset_effective_locale, set_effective_locale
from app.utils.permission_manager import PermissionManager


class _Response:
    def __init__(self, payload):
        self.status = 200
        self._payload = payload

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def json(self):
        return self._payload


class _Session:
    def __init__(self, payload):
        self.payload = payload
        self.calls = []

    def post(self, url, json=None, headers=None):
        self.calls.append({"url": url, "json": json, "headers": headers})
        return _Response(self.payload)


class TestPermissionManagerLocale(unittest.IsolatedAsyncioTestCase):
    async def test_authorization_request_uses_frozen_locale(self):
        session = _Session({"result": True})
        manager = PermissionManager()
        manager.get_session = mock.AsyncMock(return_value=session)
        token = set_effective_locale("en-US")
        try:
            with mock.patch.object(base_config, "AUTH_ENABLED", True):
                allowed = await manager.check_single_permission(
                    user_id="user-1",
                    resource_id="model-1",
                    operations="display",
                    resource_type="large_model",
                    role="user",
                )
        finally:
            reset_effective_locale(token)

        self.assertTrue(allowed)
        self.assertEqual(session.calls[0]["headers"]["Accept-Language"], "en-US")

    async def test_bkn_safe_authz_request_uses_frozen_locale(self):
        session = _Session({"allowed": True})
        with mock.patch.dict(
                "os.environ",
                {"AUTHZ_PROVIDER": "bkn-safe", "BKN_SAFE_URL": "http://bkn-safe"},
                clear=False):
            manager = PermissionManager()
        manager.get_session = mock.AsyncMock(return_value=session)
        token = set_effective_locale("en-US")
        try:
            with mock.patch.object(base_config, "AUTH_ENABLED", True):
                allowed = await manager.check_single_permission(
                    user_id="user-1",
                    resource_id="model-1",
                    operations="display",
                    resource_type="large_model",
                    role="user",
                )
        finally:
            reset_effective_locale(token)

        self.assertTrue(allowed)
        self.assertEqual(session.calls[0]["headers"]["Accept-Language"], "en-US")


if __name__ == "__main__":
    unittest.main()
