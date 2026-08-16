# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import asyncio
import json
import unittest

from app.commons.locale import (
    LocaleResponseMiddleware,
    internal_request_headers,
    localized_error_content,
    localized_stream_error,
    reset_effective_locale,
    resolve_accept_language,
    set_effective_locale,
)
from app.commons.i18n import get_error_message, lookup_error_message
from app.commons.errors.codes import ParamValidationErrors
from app.utils.common import get_user_info


class TestAcceptLanguageResolver(unittest.TestCase):
    def test_resolves_supported_and_legacy_ranges(self):
        cases = {
            "en-US": "en-US",
            "en-GB": "en-US",
            "zh_CN": "zh-CN",
            "zh-Hans": "zh-CN",
            "zh-Hant, en-US;q=0.8": "en-US",
            "zh-CN;q=0, en-US;q=1": "en-US",
            "zh-CN;q=0, *;q=1": "en-US",
            "en;q=0, en;q=1": "en-US",
            "en-US;q=NaN, zh-CN;q=0.5": "zh-CN",
            "en_US": "zh-CN",
        }
        for header, expected in cases.items():
            with self.subTest(header=header):
                self.assertEqual(resolve_accept_language(header), expected)

    def test_falls_back_when_every_supported_language_is_rejected(self):
        self.assertEqual(resolve_accept_language("zh-CN;q=0, en-US;q=0"), "zh-CN")

    def test_uses_canonical_locale_for_error_catalog_lookup(self):
        message = lookup_error_message(ParamValidationErrors.ParamMissing, "en-US")
        self.assertIsNotNone(message)
        self.assertEqual(message["code"], ParamValidationErrors.ParamMissing)

    def test_localizes_dynamic_missing_parameter_detail(self):
        localized, is_localized = localized_error_content(
            {
                "code": ParamValidationErrors.ParamMissing,
                "description": "参数缺失",
                "detail": "missing parameters: max_model_len",
                "solution": "请检查参数",
                "link": "",
            },
            "en-US",
        )

        self.assertTrue(is_localized)
        self.assertEqual(localized["description"], "Required parameter is missing.")
        self.assertEqual(localized["detail"], "Missing required parameter: max_model_len")

    def test_uses_plural_template_for_multiple_missing_parameters(self):
        localized, _ = localized_error_content(
            {
                "code": ParamValidationErrors.ParamMissing,
                "description": "参数缺失",
                "detail": "missing parameters: max_model_len, model_name",
                "solution": "请检查参数",
                "link": "",
            },
            "en-US",
        )

        self.assertEqual(localized["detail"], "Missing required parameters: max_model_len, model_name")

    def test_localizes_fastapi_missing_parameter_detail(self):
        content = {
            "code": "ModelFactory.Router.ParamError.ParamMissing",
            "description": "参数缺失",
            "detail": "page 参数缺失",
            "solution": "请检查填写的参数是否正确。",
            "link": "",
        }

        english, is_localized = localized_error_content(content, "en-US")
        chinese, _ = localized_error_content(content, "zh-CN")

        self.assertTrue(is_localized)
        self.assertEqual(english["description"], "Required parameter is missing.")
        self.assertEqual(english["detail"], "Missing required parameter: page")
        self.assertEqual(chinese["detail"], "缺少必填参数：page")

    def test_localizes_fastapi_missing_body_without_chinese_detail(self):
        content = {
            "code": "ModelFactory.Router.ParamError.ParamMissing",
            "description": "参数缺失",
            "detail": "参数缺失",
            "solution": "请检查填写的参数是否正确。",
            "link": "",
        }

        english, is_localized = localized_error_content(content, "en-US")
        chinese, _ = localized_error_content(content, "zh-CN")

        self.assertTrue(is_localized)
        self.assertEqual(english["detail"], "A required request parameter is missing.")
        self.assertEqual(chinese["detail"], "缺少必填参数。")

    def test_localizes_dynamic_parameter_type_detail_in_chinese(self):
        localized, is_localized = localized_error_content(
            {
                "code": ParamValidationErrors.ParamTypeError,
                "description": "参数类型错误",
                "detail": "parameters type error: model_names",
                "solution": "请检查参数",
                "link": "",
            },
            "zh-CN",
        )

        self.assertTrue(is_localized)
        self.assertEqual(localized["detail"], "参数类型错误：model_names")

    def test_controller_error_message_hides_internal_detail_template(self):
        message = asyncio.run(get_error_message(ParamValidationErrors.ParamMissing, "zh-CN"))

        self.assertNotIn("detail_template", message)
        self.assertNotIn("detail_template_plural", message)
        self.assertEqual(message["code"], ParamValidationErrors.ParamMissing)
        self.assertEqual(message["detail"], "缺少必填参数。")

    def test_business_error_keeps_code_and_uses_locale_catalog(self):
        code = "ModelFactory.PromptController.PromptAdd.ParameterError"

        english = asyncio.run(get_error_message(code, "en-US"))
        chinese = asyncio.run(get_error_message(code, "zh-CN"))

        self.assertEqual(english["code"], code)
        self.assertEqual(chinese["code"], code)
        self.assertEqual(english["description"], "Prompt creation is invalid.")
        self.assertEqual(chinese["description"], "提示词创建参数无效。")
        self.assertNotEqual(english["detail"], chinese["detail"])

    def test_sse_error_keeps_code_and_uses_request_locale(self):
        code = "ModelFactory.LLM.Error"
        results = {}
        for locale in ("en-US", "zh-CN"):
            token = set_effective_locale(locale)
            try:
                results[locale] = localized_stream_error(
                    code, "ModelFactory.Stream.InvalidMessageRole")
            finally:
                reset_effective_locale(token)

        self.assertEqual(results["en-US"]["code"], code)
        self.assertEqual(results["zh-CN"]["code"], code)
        self.assertEqual(results["en-US"]["description"], "Message role is invalid.")
        self.assertEqual(results["zh-CN"]["description"], "消息角色无效。")

    def test_uses_request_scoped_locale_instead_of_the_raw_header(self):
        class State:
            effective_locale = "en-US"

        class Request:
            headers = {"accept-language": "zh-CN, en-US;q=0.8"}
            state = State()

        user_id, language, role = asyncio.run(get_user_info(Request()))
        self.assertEqual((user_id, language, role), ("", "en-US", ""))

    def test_internal_requests_always_use_the_frozen_single_locale(self):
        token = set_effective_locale("en-US")
        try:
            headers = internal_request_headers({"Accept-Language": "zh-CN, en-US;q=0.8"})
        finally:
            reset_effective_locale(token)
        self.assertEqual(headers["Accept-Language"], "en-US")


class TestLocaleResponseMiddleware(unittest.IsolatedAsyncioTestCase):
    async def test_localizes_error_and_applies_private_cache_policy(self):
        async def app(scope, receive, send):
            self.assertEqual(scope["state"]["effective_locale"], "en-US")
            await send({
                "type": "http.response.start",
                "status": 401,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({
                "type": "http.response.body",
                "body": json.dumps({
                    "code": "Unauthorized",
                    "description": "token invalid",
                    "detail": "token无效或已过期",
                    "solution": "check token",
                    "link": "",
                }).encode("utf-8"),
            })

        messages = []
        async def collect(message):
            messages.append(message)

        middleware = LocaleResponseMiddleware(app)
        await middleware(
            {
                "type": "http",
                "path": "/api/mf-model-manager/v1/llm/list",
                "headers": [(b"accept-language", b"zh-CN;q=0.2, en-US;q=0.8")],
            },
            None,
            collect,
        )

        headers = dict(messages[0]["headers"])
        payload = json.loads(messages[1]["body"])
        self.assertEqual(headers[b"content-language"], b"en-US")
        self.assertEqual(headers[b"cache-control"], b"private, no-cache")
        self.assertEqual(payload["code"], "Unauthorized")
        self.assertEqual(payload["description"], "Authentication failed.")
        self.assertEqual(payload["detail"], "The access token is invalid or has expired.")
        self.assertEqual(payload["solution"], "Obtain a valid access token and try again.")
        self.assertNotIn("无效", json.dumps(payload, ensure_ascii=False))

    async def test_preserves_existing_english_message_for_unknown_error_code(self):
        localized, is_localized = localized_error_content(
            {
                "code": "ModelFactory.ConnectController.LLMAdd.NameRepeat",
                "description": "Name already exists, please modify",
                "detail": "Name already exists, please modify",
                "solution": "Please check the input information",
                "link": "",
            },
            "en-US",
        )

        self.assertTrue(is_localized)
        self.assertEqual(localized["description"], "Name already exists, please modify")
        self.assertEqual(localized["detail"], "Name already exists, please modify")
        self.assertEqual(localized["solution"], "Please check the input information")

    async def test_does_not_apply_business_cache_policy_to_health(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({"type": "http.response.body", "body": b'{"status":"ok"}'})

        messages = []
        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/v1/health/ready", "headers": []},
            None,
            collect,
        )
        headers = dict(messages[0]["headers"])
        self.assertNotIn(b"cache-control", headers)
        self.assertNotIn(b"content-language", headers)

    async def test_localizes_framework_not_found(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 404,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({
                "type": "http.response.body",
                "body": b'{"detail":"Not Found"}',
            })

        messages = []
        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {
                "type": "http",
                "path": "/api/mf-model-manager/v1/not-a-route",
                "headers": [(b"accept-language", b"zh-CN")],
            },
            None,
            collect,
        )

        headers = dict(messages[0]["headers"])
        payload = json.loads(messages[1]["body"])
        self.assertEqual(headers[b"content-language"], b"zh-CN")
        self.assertEqual(payload["code"], "HTTP_404")
        self.assertEqual(payload["description"], "资源不存在。")

    async def test_preserves_extensions_for_known_business_http_error(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 400,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({
                "type": "http.response.body",
                "body": json.dumps({
                    "detail": {
                        "code": "ModelFactory.OperationAudit.InvalidTimestamp",
                        "link": "",
                    },
                    "trace_id": "trace-1",
                }).encode("utf-8"),
            })

        messages = []
        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {
                "type": "http",
                "path": "/api/mf-model-manager/v1/operation-audits",
                "headers": [(b"accept-language", b"en-US")],
            },
            None,
            collect,
        )

        payload = json.loads(messages[1]["body"])
        self.assertEqual(payload["code"], "ModelFactory.OperationAudit.InvalidTimestamp")
        self.assertEqual(payload["detail"], "from or to must use the RFC3339 timestamp format.")
        self.assertEqual(payload["trace_id"], "trace-1")

    async def test_does_not_localize_health_json_errors(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 503,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({"type": "http.response.body", "body": b'{"detail":"not ready"}'})

        messages = []
        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/v1/health/ready", "headers": []},
            None,
            collect,
        )

        headers = dict(messages[0]["headers"])
        self.assertNotIn(b"cache-control", headers)
        self.assertNotIn(b"content-language", headers)
        self.assertEqual(messages[1]["body"], b'{"detail":"not ready"}')

    async def test_preserves_stricter_upstream_cache_directives(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 200,
                "headers": [
                    (b"content-type", b"application/json"),
                    (b"cache-control", b"public, no-store, no-transform"),
                ],
            })
            await send({"type": "http.response.body", "body": b'{}'})

        messages = []
        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-manager/v1/llm/list", "headers": []},
            None,
            collect,
        )

        headers = dict(messages[0]["headers"])
        self.assertEqual(headers[b"cache-control"], b"no-store, no-transform, private")

    async def test_forwards_sse_response_start_before_the_first_event(self):
        messages = []

        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"text/event-stream; charset=utf-8")],
            })
            self.assertEqual(len(messages), 1)
            self.assertEqual(dict(messages[0]["headers"])[b"cache-control"], b"private, no-cache")
            await send({"type": "http.response.body", "body": b"data: first\\n\\n", "more_body": True})

        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {
                "type": "http",
                "path": "/api/mf-model-manager/v1/llm/chat",
                "headers": [(b"accept-language", b"en-US")],
            },
            None,
            collect,
        )

        self.assertEqual(messages[0]["type"], "http.response.start")
        self.assertEqual(messages[1]["body"], b"data: first\\n\\n")


if __name__ == "__main__":
    unittest.main()
