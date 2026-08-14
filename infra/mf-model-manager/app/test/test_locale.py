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
    reset_effective_locale,
    resolve_accept_language,
    set_effective_locale,
)
from app.commons.i18n import lookup_error_message
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

    def test_preserves_dynamic_parameter_error_detail_when_translation_is_empty(self):
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
        self.assertEqual(localized["detail"], "missing parameters: max_model_len")

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
        self.assertEqual(payload["description"], "Request failed.")
        self.assertEqual(payload["detail"], "The request could not be completed.")
        self.assertNotIn("无效", json.dumps(payload, ensure_ascii=False))

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


if __name__ == "__main__":
    unittest.main()
