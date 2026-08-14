# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest

from fastapi import FastAPI, HTTPException
from fastapi.testclient import TestClient

from app.commons.locale import LocaleResponseMiddleware
from app.utils.app_utils import configure_locale_openapi, unhandled_exception_handler


class TestAppLocaleIntegration(unittest.TestCase):
    def setUp(self):
        self.app = FastAPI()
        self.app.add_middleware(LocaleResponseMiddleware)
        self.app.add_exception_handler(Exception, unhandled_exception_handler)

        @self.app.get("/api/mf-model-manager/v1/test-error")
        async def test_error():
            raise RuntimeError("unexpected")

        @self.app.get("/api/mf-model-manager/v1/test-http-error")
        async def test_http_error():
            raise HTTPException(
                status_code=404,
                detail={"code": "ModelFactory.OperationAudit.EventNotFound", "link": ""},
            )

        @self.app.get("/api/private/mf-model-manager/v1/test-private")
        async def test_private():
            return {"status": "ok"}

        @self.app.get("/api/v1/health/test-error")
        async def test_health_error():
            raise RuntimeError("unexpected")

        configure_locale_openapi(self.app)
        self.client = TestClient(self.app, raise_server_exceptions=False)

    def test_unhandled_exception_has_locale_and_cache_headers(self):
        response = self.client.get(
            "/api/mf-model-manager/v1/test-error",
            headers={"Accept-Language": "en-US"},
        )

        self.assertEqual(response.status_code, 500)
        self.assertEqual(response.headers["content-language"], "en-US")
        self.assertEqual(response.headers["cache-control"], "private, no-cache")
        self.assertEqual(response.json()["description"], "Request failed.")

    def test_http_exception_is_localized_with_a_stable_error_code(self):
        response = self.client.get(
            "/api/mf-model-manager/v1/test-http-error",
            headers={"Accept-Language": "zh-CN"},
        )

        self.assertEqual(response.status_code, 404)
        self.assertEqual(response.headers["content-language"], "zh-CN")
        self.assertEqual(response.json()["code"], "ModelFactory.OperationAudit.EventNotFound")
        self.assertEqual(response.json()["description"], "审计事件不存在。")

    def test_framework_not_found_uses_the_status_code_fallback(self):
        response = self.client.get(
            "/api/mf-model-manager/v1/not-a-route",
            headers={"Accept-Language": "en-US"},
        )

        self.assertEqual(response.status_code, 404)
        self.assertEqual(response.headers["content-language"], "en-US")
        self.assertEqual(response.json()["code"], "HTTP_404")

    def test_health_exception_keeps_the_machine_error_contract(self):
        response = self.client.get("/api/v1/health/test-error")

        self.assertEqual(response.status_code, 500)
        self.assertNotIn("content-language", response.headers)
        self.assertNotIn("cache-control", response.headers)
        self.assertEqual(response.text, "Internal Server Error")

    def test_generated_openapi_declares_locale_contract(self):
        schema = self.app.openapi()
        operation = schema["paths"]["/api/mf-model-manager/v1/test-error"]["get"]
        self.assertIn(
            {"$ref": "#/components/parameters/AcceptLanguage"},
            operation["parameters"],
        )
        self.assertIn("401", operation["responses"])
        self.assertEqual(
            operation["responses"]["401"]["headers"]["Content-Language"]["$ref"],
            "#/components/headers/ContentLanguage",
        )
        private_operation = schema["paths"]["/api/private/mf-model-manager/v1/test-private"]["get"]
        self.assertNotIn("401", private_operation["responses"])


if __name__ == "__main__":
    unittest.main()
