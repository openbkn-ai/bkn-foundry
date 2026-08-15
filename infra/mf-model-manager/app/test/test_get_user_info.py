# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import os
import unittest
from unittest import mock

from app.commons import get_user_info
from app.commons.locale import reset_effective_locale, set_effective_locale


class _FakeResp:
    def __init__(self, status, body):
        self.status = status
        self._body = body

    async def __aenter__(self):
        return self

    async def __aexit__(self, *a):
        return False

    async def text(self):
        return self._body


class _FakeSession:
    """Captures the posted url/json and returns a canned response."""

    captured = {}

    def __init__(self, status, body):
        self._status = status
        self._body = body

    async def __aenter__(self):
        return self

    async def __aexit__(self, *a):
        return False

    def post(self, url, json=None, headers=None):
        _FakeSession.captured = {"url": url, "json": json, "headers": headers}
        return _FakeResp(self._status, self._body)


def _patch(session):
    return mock.patch.object(get_user_info.aiohttp, "ClientSession", return_value=session)


_NAMES_BODY = (
    '{"user_names":[{"id":"u1","name":"Alice"}],'
    '"app_names":[{"id":"a2","name":"AppSvc"}],'
    '"contactor_names":[],"department_names":[],"group_names":[]}'
)


class TestGetUsernameByIdsBknSafe(unittest.IsolatedAsyncioTestCase):
    async def test_bkn_safe_merges_user_and_app_names(self):
        token = set_effective_locale("en-US")
        try:
            with mock.patch.object(get_user_info.base_config, "DEBUG", False), \
                    _patch(_FakeSession(200, _NAMES_BODY)), \
                    mock.patch.dict(os.environ, {"DIRECTORY_PROVIDER": "bkn-safe",
                                                 "BKN_SAFE_URL": "http://safe:8080"}):
                out = await get_user_info.get_username_by_ids(["u1", "a2"])
        finally:
            reset_effective_locale(token)

        self.assertEqual(out, {"u1": "Alice", "a2": "AppSvc"})
        self.assertEqual(_FakeSession.captured["url"],
                         "http://safe:8080/api/safe/v1/directory/names")
        # ids are sent as BOTH user_ids and app_ids (app accounts are User rows)
        self.assertEqual(_FakeSession.captured["json"],
                         {"user_ids": ["u1", "a2"], "app_ids": ["u1", "a2"]})
        self.assertEqual(_FakeSession.captured["headers"]["Accept-Language"], "en-US")

    async def test_empty_ids_short_circuit(self):
        with mock.patch.object(get_user_info.base_config, "DEBUG", False):
            self.assertEqual(await get_user_info.get_username_by_ids([]), {})

    async def test_bkn_safe_non_200_raises(self):
        with mock.patch.object(get_user_info.base_config, "DEBUG", False), \
                _patch(_FakeSession(500, "")), \
                mock.patch.dict(os.environ, {"DIRECTORY_PROVIDER": "bkn-safe",
                                             "BKN_SAFE_URL": "http://safe:8080"}):
            with self.assertRaises(Exception):
                await get_user_info.get_username_by_ids(["u1"])

    async def test_user_management_uses_the_frozen_locale(self):
        token = set_effective_locale("en-US")
        try:
            with (
                    mock.patch.object(get_user_info.base_config, "DEBUG", False),
                    _patch(_FakeSession(200, '[{"id":"u1","name":"Alice"}]')),
                    mock.patch.dict(os.environ, {"DIRECTORY_PROVIDER": ""}, clear=False),
            ):
                out = await get_user_info.get_username_by_ids(["u1"])
        finally:
            reset_effective_locale(token)

        self.assertEqual(out, {"u1": "Alice"})
        self.assertEqual(
            _FakeSession.captured["headers"]["Accept-Language"],
            "en-US",
        )


if __name__ == "__main__":
    unittest.main()
