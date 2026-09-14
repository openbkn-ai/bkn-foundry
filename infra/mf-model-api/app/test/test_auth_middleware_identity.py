# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Regression tests for public-surface identity-header spoofing.

The auth middleware appends the verified identity to ``request.scope['headers']``.
Because ``Headers.get`` returns the first match, a client-supplied
``x-account-id`` placed ahead of the appended one would otherwise be read by the
handlers, letting any authenticated caller (including a ``bak_`` AppKey holder)
impersonate an arbitrary account. The middleware must therefore drop
client-supplied identity headers before injecting the verified one.
"""

import unittest
from unittest import mock

from starlette.datastructures import Headers

from app.utils.app_utils import _set_trusted_identity, auth_middleware


class _Request:
    """Minimal Request stand-in exercising the code paths the middleware uses."""

    def __init__(self, path="/api/mf-model-api/v1/chat/completions", raw_headers=None, dict_headers=None):
        self.url = mock.Mock()
        self.url.path = path
        self.scope = {"headers": list(raw_headers or [])}
        self.headers = dict(dict_headers or {})


def _first_identity(request):
    """Read x-account-id/type the way the handlers do: first match wins."""
    headers = Headers(raw=request.scope["headers"])
    return headers.get("x-account-id"), headers.get("x-account-type")


class TestSetTrustedIdentity(unittest.TestCase):
    def test_strips_client_supplied_identity(self):
        raw = [
            (b"x-account-id", b"266c6a42-6131-4d62-8f39-853e7093701c"),  # spoofed admin id
            (b"x-account-type", b"app"),
            (b"content-type", b"application/json"),
        ]
        request = _Request(raw_headers=raw)

        _set_trusted_identity(request, "real-sub-123", "user")

        account_id, account_type = _first_identity(request)
        self.assertEqual(account_id, "real-sub-123")
        self.assertEqual(account_type, "user")
        ids = [v for (n, v) in request.scope["headers"] if n == b"x-account-id"]
        types = [v for (n, v) in request.scope["headers"] if n == b"x-account-type"]
        self.assertEqual(ids, [b"real-sub-123"])
        self.assertEqual(types, [b"user"])
        self.assertIn((b"content-type", b"application/json"), request.scope["headers"])


class TestAuthMiddlewareIdentity(unittest.IsolatedAsyncioTestCase):
    async def test_appkey_branch_drops_spoofed_header(self):
        # A bak_ AppKey holder forges the admin id in the request headers.
        request = _Request(
            raw_headers=[(b"x-account-id", b"266c6a42-6131-4d62-8f39-853e7093701c")],
            dict_headers={"Authorization": "Bearer bak_kid_secret"},
        )

        async def call_next(_req):
            return "ok"

        with mock.patch("app.utils.app_utils._verify_app_key",
                        new=mock.AsyncMock(return_value=("real-sub-123", "user"))):
            result = await auth_middleware(request, call_next)

        self.assertEqual(result, "ok")
        self.assertEqual(_first_identity(request), ("real-sub-123", "user"))

    @staticmethod
    def _hydra_session(payload_json):
        response = mock.AsyncMock()
        response.status = 200
        response.text = mock.AsyncMock(return_value=payload_json)
        post_cm = mock.AsyncMock()
        post_cm.__aenter__ = mock.AsyncMock(return_value=response)
        post_cm.__aexit__ = mock.AsyncMock(return_value=None)
        session = mock.AsyncMock()
        session.post = mock.Mock(return_value=post_cm)
        session_cm = mock.AsyncMock()
        session_cm.__aenter__ = mock.AsyncMock(return_value=session)
        session_cm.__aexit__ = mock.AsyncMock(return_value=None)
        return session_cm

    async def test_hydra_branch_drops_spoofed_header(self):
        request = _Request(
            raw_headers=[(b"x-account-id", b"266c6a42-6131-4d62-8f39-853e7093701c")],
            dict_headers={"Authorization": "Bearer valid_token"},
        )

        async def call_next(_req):
            return "ok"

        session_cm = self._hydra_session('{"active": true, "sub": "real-sub-123", "client_id": "cli"}')
        with mock.patch("app.utils.app_utils.aiohttp.ClientSession", return_value=session_cm):
            result = await auth_middleware(request, call_next)

        self.assertEqual(result, "ok")
        self.assertEqual(_first_identity(request), ("real-sub-123", "user"))


if __name__ == "__main__":
    unittest.main()
