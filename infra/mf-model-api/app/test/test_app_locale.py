# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest

from fastapi import FastAPI
from fastapi.testclient import TestClient
from fastapi.responses import JSONResponse
from starlette.middleware.base import BaseHTTPMiddleware

from app.commons.locale import LocaleResponseMiddleware
from app.utils.app_utils import install_locale_openapi, unhandled_exception_handler


class TestAppLocaleIntegration(unittest.TestCase):
    def setUp(self):
        self.app = FastAPI()
        self.app.add_middleware(BaseHTTPMiddleware, dispatch=self._pass_through)
        self.app.add_middleware(LocaleResponseMiddleware)
        self.app.add_exception_handler(Exception, unhandled_exception_handler)

        @self.app.get("/api/mf-model-api/v1/test-error")
        async def test_error():
            raise RuntimeError("unexpected")

        @self.app.get("/api/mf-model-api/v1/chat/completions")
        async def test_openai_error():
            raise RuntimeError("unexpected")

        @self.app.get("/api/mf-model-api/v1/test-chunked-error")
        async def test_chunked_error():
            return JSONResponse(
                status_code=400,
                content={
                    "code": "ModelFactory.Router.ParamError.ParamMissing",
                    "description": "参数缺失",
                    "detail": "missing parameters: model_ids",
                    "solution": "请检查输入信息",
                },
            )

        @self.app.get("/api/private/mf-model-api/v1/test-private")
        async def test_private():
            return {"status": "ok"}

        @self.app.get("/api/v1/health/test-error")
        async def test_health_error():
            raise RuntimeError("unexpected")

        install_locale_openapi(self.app)
        self.client = TestClient(self.app, raise_server_exceptions=False)

    @staticmethod
    async def _pass_through(request, call_next):
        return await call_next(request)

    def test_unhandled_business_error_has_locale_and_cache_headers(self):
        response = self.client.get("/api/mf-model-api/v1/test-error", headers={"Accept-Language": "en-US"})
        self.assertEqual(response.status_code, 500)
        self.assertEqual(response.headers["content-language"], "en-US")
        self.assertEqual(response.headers["cache-control"], "private, no-cache")
        self.assertEqual(response.json()["description"], "Request failed.")

    def test_unhandled_openai_error_keeps_the_openai_error_contract(self):
        response = self.client.get("/api/mf-model-api/v1/chat/completions", headers={"Accept-Language": "en-US"})
        self.assertEqual(response.status_code, 500)
        self.assertEqual(response.headers["content-language"], "en-US")
        self.assertEqual(response.json()["error"]["code"], "ModelFactory.InternalError")
        self.assertEqual(response.json()["error"]["type"], "server_error")

    def test_base_http_middleware_does_not_bypass_error_localization(self):
        response = self.client.get("/api/mf-model-api/v1/test-chunked-error", headers={"Accept-Language": "en-US"})
        self.assertEqual(response.status_code, 400)
        self.assertEqual(response.headers["content-language"], "en-US")
        self.assertEqual(response.headers["cache-control"], "private, no-cache")
        self.assertEqual(response.json()["detail"], "Missing required parameter: model_ids")

    def test_health_error_remains_a_machine_response(self):
        response = self.client.get("/api/v1/health/test-error")
        self.assertEqual(response.status_code, 500)
        self.assertNotIn("content-language", response.headers)
        self.assertNotIn("cache-control", response.headers)

    def test_openapi_declares_locale_headers_for_business_operations(self):
        schema = self.app.openapi()
        operation = schema["paths"]["/api/mf-model-api/v1/test-error"]["get"]
        self.assertIn({"$ref": "#/components/parameters/AcceptLanguage"}, operation["parameters"])
        self.assertEqual(
            operation["responses"]["401"]["headers"]["Content-Language"]["$ref"],
            "#/components/headers/ContentLanguage",
        )
        private_operation = schema["paths"]["/api/private/mf-model-api/v1/test-private"]["get"]
        self.assertIn({"$ref": "#/components/parameters/AcceptLanguage"}, private_operation["parameters"])
        self.assertNotIn("401", private_operation["responses"])


if __name__ == "__main__":
    unittest.main()
