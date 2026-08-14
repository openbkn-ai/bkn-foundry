# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import json
import unittest
from unittest import mock

from app.commons.i18n import get_error_message
from app.commons.locale import (
    LocaleResponseMiddleware,
    internal_request_headers,
    localized_error_content,
    platform_openai_error,
    reset_effective_locale,
    resolve_accept_language,
    set_effective_locale,
)
from app.utils.permission_manager import PermissionManager


class TestAcceptLanguageResolver(unittest.TestCase):
    def test_resolves_supported_ranges_and_rejections(self):
        cases = {
            "en-US": "en-US",
            "en-GB": "en-US",
            "zh_CN": "zh-CN",
            "zh-Hans, en-US;q=0.8": "zh-CN",
            "zh_Hans, en-US;q=0.8": "en-US",
            "zh-Hant, en-US;q=0.8": "en-US",
            "zh-CN;q=0, *;q=1": "en-US",
            "en;q=0, en;q=1": "en-US",
            "en-US;q=NaN, zh-CN;q=0.5": "zh-CN",
            "en_US": "zh-CN",
        }
        for header, expected in cases.items():
            with self.subTest(header=header):
                self.assertEqual(resolve_accept_language(header), expected)

    def test_internal_request_always_uses_the_frozen_single_locale(self):
        token = set_effective_locale("en-US")
        try:
            headers = internal_request_headers({"Accept-Language": "zh-CN, en-US;q=0.8"})
        finally:
            reset_effective_locale(token)
        self.assertEqual(headers["Accept-Language"], "en-US")

    def test_unknown_english_error_keeps_its_existing_english_text(self):
        content, changed = localized_error_content(
            {
                "code": "ModelFactory.ConnectController.LLMAdd.NameRepeat",
                "description": "Name already exists, please modify",
                "detail": "Name already exists, please modify",
                "solution": "Please check the input information",
            },
            "en-US",
        )
        self.assertTrue(changed)
        self.assertEqual(content["description"], "Name already exists, please modify")

    def test_legacy_parameter_error_uses_a_localized_dynamic_detail(self):
        content, changed = localized_error_content(
            {
                "code": "ParamMissing",
                "description": "参数缺失",
                "detail": "missing parameters: model_ids",
                "solution": "请检查输入信息",
            },
            "en-US",
        )
        self.assertTrue(changed)
        self.assertEqual(content["detail"], "Missing required parameter: model_ids")

    def test_platform_stream_error_uses_the_frozen_locale_and_stable_code(self):
        token = set_effective_locale("en-US")
        try:
            error = platform_openai_error(
                "ModelFactory.Stream.ModelConnectionFailed", "api_connection_error")
        finally:
            reset_effective_locale(token)
        self.assertEqual(error["error"]["code"], "ModelFactory.Stream.ModelConnectionFailed")
        self.assertEqual(error["error"]["message"], "The model service could not be reached.")


class TestLocaleResponseMiddleware(unittest.IsolatedAsyncioTestCase):
    async def test_localizes_openai_error_and_sets_cache_headers(self):
        async def app(scope, receive, send):
            self.assertEqual(scope["state"]["effective_locale"], "en-US")
            await send({
                "type": "http.response.start",
                "status": 400,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({
                "type": "http.response.body",
                "body": json.dumps({"error": {
                    "message": "messages 参数缺失",
                    "type": "invalid_request_error",
                    "param": "messages",
                    "code": "ModelFactory.Router.ParamError.ParamMissing",
                }}).encode("utf-8"),
            })

        messages = []
        async def collect(message):
            messages.append(message)
        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/chat/completions",
             "headers": [(b"accept-language", b"zh-CN;q=0.2, en-US;q=0.8")]},
            None,
            collect,
        )

        headers = dict(messages[0]["headers"])
        content = json.loads(messages[1]["body"])
        self.assertEqual(headers[b"content-language"], b"en-US")
        self.assertEqual(headers[b"cache-control"], b"private, no-cache")
        self.assertEqual(content["error"]["code"], "ModelFactory.Router.ParamError.ParamMissing")
        self.assertEqual(content["error"]["message"], "Missing required parameter: messages")

    async def test_localizes_chunked_json_error_after_the_final_body_chunk(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 400,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({
                "type": "http.response.body",
                "body": b'{"error":{"message":"missing parameters: ',
                "more_body": True,
            })
            await send({
                "type": "http.response.body",
                "body": b'messages","type":"invalid_request_error","param":"messages",'
                        b'"code":"ModelFactory.Router.ParamError.ParamMissing"}}',
                "more_body": False,
            })

        messages = []

        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/chat/completions",
             "headers": [(b"accept-language", b"en-US")]},
            None,
            collect,
        )

        self.assertEqual(len(messages), 2)
        headers = dict(messages[0]["headers"])
        content = json.loads(messages[1]["body"])
        self.assertEqual(headers[b"content-language"], b"en-US")
        self.assertEqual(headers[b"cache-control"], b"private, no-cache")
        self.assertEqual(content["error"]["code"], "ModelFactory.Router.ParamError.ParamMissing")
        self.assertEqual(content["error"]["message"], "Missing required parameter: messages")

    async def test_forwards_sse_headers_before_the_first_event(self):
        messages = []
        async def collect(message):
            messages.append(message)

        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"text/event-stream; charset=utf-8")],
            })
            self.assertEqual(len(messages), 1)
            self.assertEqual(dict(messages[0]["headers"])[b"cache-control"], b"private, no-cache")
            await send({"type": "http.response.body", "body": b"data: first\\n\\n", "more_body": True})

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/chat/completions",
             "headers": [(b"accept-language", b"en-US")]},
            None,
            collect,
        )
        self.assertEqual(messages[0]["type"], "http.response.start")
        self.assertEqual(messages[1]["body"], b"data: first\\n\\n")

    async def test_preserves_upstream_openai_errors_without_content_language(self):
        provider_error = {
            "error": {
                "message": "Provider API key is invalid",
                "type": "authentication_error",
                "param": None,
                "code": "invalid_api_key",
            }
        }

        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 401,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({"type": "http.response.body", "body": json.dumps(provider_error).encode("utf-8")})

        messages = []

        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/chat/completions",
             "headers": [(b"accept-language", b"en-US")]},
            None,
            collect,
        )
        headers = dict(messages[0]["headers"])
        self.assertNotIn(b"content-language", headers)
        self.assertEqual(json.loads(messages[1]["body"]), provider_error)

    async def test_localizes_default_framework_not_found_for_business_api(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 404,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({"type": "http.response.body", "body": b'{"detail":"Not Found"}'})

        messages = []

        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/models/missing",
             "headers": [(b"accept-language", b"en-US")]},
            None,
            collect,
        )
        headers = dict(messages[0]["headers"])
        content = json.loads(messages[1]["body"])
        self.assertEqual(headers[b"content-language"], b"en-US")
        self.assertEqual(content["code"], "HTTP_404")
        self.assertEqual(content["detail"], "The requested resource does not exist.")

    async def test_localizes_framework_method_not_allowed_as_openai_error(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 405,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({"type": "http.response.body", "body": b'{"detail":"Method Not Allowed"}'})

        messages = []

        async def collect(message):
            messages.append(message)

        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/chat/completions",
             "headers": [(b"accept-language", b"en-US")]},
            None,
            collect,
        )
        headers = dict(messages[0]["headers"])
        content = json.loads(messages[1]["body"])
        self.assertEqual(headers[b"content-language"], b"en-US")
        self.assertEqual(content["error"]["code"], "HTTP_405")
        self.assertEqual(content["error"]["message"], "The requested resource does not support this HTTP method.")

    async def test_marks_existing_chinese_error_with_content_language(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 401,
                "headers": [(b"content-type", b"application/json")],
            })
            await send({
                "type": "http.response.body",
                "body": json.dumps({
                    "code": "Unauthorized",
                    "description": "认证失败",
                    "detail": "令牌无效或已过期",
                    "solution": "请重新登录后重试。",
                }).encode("utf-8"),
            })

        messages = []
        async def collect(message):
            messages.append(message)
        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/models", "headers": []}, None, collect)
        self.assertEqual(dict(messages[0]["headers"])[b"content-language"], b"zh-CN")

    async def test_health_response_is_not_decorated(self):
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
            {"type": "http", "path": "/api/v1/health/ready", "headers": []}, None, collect)
        headers = dict(messages[0]["headers"])
        self.assertNotIn(b"cache-control", headers)
        self.assertNotIn(b"content-language", headers)

    async def test_preserves_stricter_cache_directives(self):
        async def app(scope, receive, send):
            await send({
                "type": "http.response.start",
                "status": 200,
                "headers": [
                    (b"content-type", b"application/json"),
                    (b"cache-control", b"public, no-store, no-transform"),
                ],
            })
            await send({"type": "http.response.body", "body": b"{}"})

        messages = []
        async def collect(message):
            messages.append(message)
        await LocaleResponseMiddleware(app)(
            {"type": "http", "path": "/api/mf-model-api/v1/models", "headers": []}, None, collect)
        self.assertEqual(
            dict(messages[0]["headers"])[b"cache-control"],
            b"no-store, no-transform, private",
        )


class _Response:
    status = 200

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def json(self):
        return {"result": True}


class _Session:
    def __init__(self):
        self.calls = []

    def post(self, url, json=None, headers=None):
        self.calls.append({"url": url, "headers": headers})
        return _Response()


class TestInternalLocalePropagation(unittest.IsolatedAsyncioTestCase):
    async def test_legacy_async_catalog_returns_a_mutable_error_envelope(self):
        content = await get_error_message("ParamMissing", "en-US")
        self.assertEqual(content["code"], "ParamMissing")
        self.assertEqual(content["description"], "Required parameter is missing.")

    async def test_permission_request_uses_frozen_locale(self):
        session = _Session()
        manager = PermissionManager()
        manager.get_session = mock.AsyncMock(return_value=session)
        token = set_effective_locale("en-US")
        try:
            with mock.patch("app.utils.permission_manager.base_config.AUTH_ENABLED", True):
                allowed = await manager.check_single_permission(
                    "user-1", "model-1", "display", "large_model", "user")
        finally:
            reset_effective_locale(token)
        self.assertTrue(allowed)
        self.assertEqual(session.calls[0]["headers"]["Accept-Language"], "en-US")


if __name__ == "__main__":
    unittest.main()
