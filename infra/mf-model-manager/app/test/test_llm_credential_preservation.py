"""Run complete controller functions without importing provider SDKs or monitors."""

import ast
import hashlib
import hmac
import json
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

from starlette.responses import JSONResponse


APP = Path(__file__).parents[1]
OMITTED = object()


def load_functions(path, names, dependencies):
    tree = ast.parse((APP / path).read_text())
    functions = [node for node in tree.body
                 if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
    assert {node.name for node in functions} == set(names)
    exec(compile(ast.Module(body=functions, type_ignores=[]), str(APP / path), "exec"), dependencies)


class TestLlmCredentialPreservation(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.original = {
            "api_url": "https://provider.invalid/v1/chat/completions",
            "api_model": "test-model",
            "api_key": "synthetic-original",
            "api_type": "openai",
        }
        self.row = {
            "f_model_id": "owned-model",
            "f_model_name": "original-name",
            "f_model_series": "others",
            "f_model_type": "llm",
            "f_quota": 1,
            "f_model_config": json.dumps(self.original),
        }
        self.dao = SimpleNamespace(
            get_all_model_list=Mock(return_value=[self.row]),
            get_data_from_model_list_by_id=Mock(return_value=[self.row]),
            edit_model=Mock(),
        )
        self.quota = SimpleNamespace(delete_model_quota_by_model_id=Mock())
        self.provider = AsyncMock(return_value=JSONResponse({"status": "ok"}))
        self.env = dict(
            json=json, hashlib=hashlib, hmac=hmac, _CREDENTIAL_DIGEST_KEY="",
            JSONResponse=JSONResponse, llm_model_dao=self.dao,
            model_quota_dao=self.quota,
            permission_manager=SimpleNamespace(check_single_permission=AsyncMock(return_value=True)),
            redis_util=SimpleNamespace(delete_str=AsyncMock()),
            llm_edit_verify=Mock(return_value=False), llm_test_verify=Mock(return_value=False),
            llm_test=self.provider, set_operation_audit_target=Mock(), StandLogger=Mock(),
            DataBaseError={"code": "DatabaseError"}, LLMTestError={"code": "TestError"},
            ModelFactory_ModelController_TestModel_Error_Error={"code": "ProviderError"},
        )
        load_functions("utils/reshape_utils.py", {"_credential_digest", "is_credential_digest"}, self.env)
        load_functions("controller/llm_controller.py", {"edit_model", "test_model"}, self.env)

    def request(self, key=OMITTED, change=False):
        config = {key: value for key, value in self.original.items() if key != "api_key"}
        if key is not OMITTED:
            config["api_key"] = key
        return {
            "model_id": "owned-model", "model_name": "renamed",
            "model_series": "others", "model_type": "llm", "max_model_len": 256,
            "model_config": config, "change": change,
        }

    async def edit_and_check(self, request, expected):
        self.dao.edit_model.reset_mock()
        response = await self.env["edit_model"](request, "owner", "en-US")
        self.assertEqual(response.status_code, 200)
        args = self.dao.edit_model.call_args.args
        self.assertEqual(args[1], "renamed")
        self.assertEqual(args[5], 1)
        saved = json.loads(args[6])
        self.assertEqual(saved, expected)
        self.quota.delete_model_quota_by_model_id.assert_not_called()
        self.assertNotIn("synthetic-", response.body.decode())
        return saved

    async def test_unchanged_credential_inputs_preserve_stored_key(self):
        digest = self.env["_credential_digest"](self.original["api_key"])
        legacy = hashlib.md5(self.original["api_key"].encode(), usedforsecurity=False).hexdigest()
        for key in (OMITTED, None, "", digest, legacy):
            with self.subTest(input="omitted" if key is OMITTED else "supplied"):
                saved = await self.edit_and_check(self.request(key), self.original)
                # Feed the persisted configuration back into the real test controller.
                self.row["f_model_config"] = json.dumps(saved)
                self.provider.reset_mock()
                response = await self.env["test_model"](self.request(), "owner", "en-US")
                self.assertEqual(response.status_code, 200)
                self.assertEqual(self.provider.call_args.args[1], self.original)

    async def test_omitted_change_defaults_to_preserving_key(self):
        request = self.request()
        del request["change"]
        await self.edit_and_check(request, self.original)

    async def test_configuration_test_preserves_key_with_submitted_provider_fields(self):
        request = self.request()
        request["model_config"]["api_url"] = "https://replacement.invalid/v1"
        response = await self.env["test_model"](request, "owner", "en-US")
        self.assertEqual(response.status_code, 200)
        self.assertEqual(self.provider.call_args.args[1], dict(
            self.original, api_url="https://replacement.invalid/v1"))

    async def test_explicit_rotation_and_metadata_updates(self):
        request = self.request("synthetic-replacement", change=True)
        request["model_config"]["api_url"] = "https://replacement.invalid/v1"
        expected = dict(self.original, api_key="synthetic-replacement",
                        api_url="https://replacement.invalid/v1")
        await self.edit_and_check(request, expected)

    async def test_supported_digest_is_preserved_even_with_change_true(self):
        await self.edit_and_check(
            self.request(self.env["_credential_digest"](self.original["api_key"]), change=True),
            self.original,
        )

    async def test_keyless_model_remains_keyless(self):
        del self.original["api_key"]
        self.row["f_model_config"] = json.dumps(self.original)
        await self.edit_and_check(self.request(), self.original)
        await self.env["test_model"](self.request(), "owner", "en-US")
        self.assertEqual(self.provider.call_args.args[1], self.original)


if __name__ == "__main__":
    unittest.main()
