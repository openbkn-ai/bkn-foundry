import unittest

from fastapi import Body, FastAPI
from fastapi.testclient import TestClient
from starlette.middleware.base import BaseHTTPMiddleware

from app.utils import operation_audit


class TestOperationAuditRequestID(unittest.TestCase):
    def test_generates_request_id_when_gateway_did_not_provide_one(self):
        request_id, generated = operation_audit.operation_audit_request_id({})

        self.assertTrue(generated)
        self.assertTrue(request_id.startswith("req_"))

    def test_preserves_gateway_request_id(self):
        request_id, generated = operation_audit.operation_audit_request_id({"bkn-request-id": "req_gateway"})

        self.assertEqual(request_id, "req_gateway")
        self.assertFalse(generated)

    def test_replays_body_to_audited_route(self):
        app = FastAPI()

        @app.post("/api/mf-model-manager/v1/llm/add")
        async def add_model(payload: dict = Body(...)):
            return payload

        app.add_middleware(BaseHTTPMiddleware, dispatch=operation_audit.operation_audit_middleware)

        response = TestClient(app).post(
            "/api/mf-model-manager/v1/llm/add",
            json={"max_model_len": 4096, "model_name": "test-model"},
        )

        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json()["model_name"], "test-model")


if __name__ == "__main__":
    unittest.main()
