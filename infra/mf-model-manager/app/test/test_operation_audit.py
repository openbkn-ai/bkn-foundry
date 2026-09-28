import sys
import types
import unittest
import uuid
from pathlib import Path
from datetime import datetime, timezone
from unittest.mock import patch

from starlette.requests import Request
from starlette.responses import Response

get_user_info = types.ModuleType("app.commons.get_user_info")
get_user_info.get_username_by_ids = None
_previous_get_user_info = sys.modules.get("app.commons.get_user_info")
sys.modules["app.commons.get_user_info"] = get_user_info
from fastapi import Body, FastAPI
from fastapi.testclient import TestClient
from starlette.middleware.base import BaseHTTPMiddleware

from app.utils import operation_audit
if _previous_get_user_info is None:
    del sys.modules["app.commons.get_user_info"]
else:
    sys.modules["app.commons.get_user_info"] = _previous_get_user_info


class TestOperationAuditRequestID(unittest.TestCase):
    def test_legacy_operation_audit_query_is_not_registered(self):
        router_source = (Path(__file__).parents[1] / "routers" / "__init__.py").read_text()
        self.assertNotIn("operation_audit_router", router_source)

    def test_generates_request_id_when_gateway_did_not_provide_one(self):
        request_id, generated = operation_audit.operation_audit_request_id({})

        self.assertTrue(generated)
        self.assertTrue(request_id.startswith("req_"))

    def test_preserves_gateway_request_id(self):
        request_id, generated = operation_audit.operation_audit_request_id({"bkn-request-id": "req_gateway"})

        self.assertEqual(request_id, "req_gateway")
        self.assertFalse(generated)

    def test_kafka_record_observes_management_attempt_without_fake_change_facts(self):
        entry = {
            "event_time": datetime(2026, 9, 28, 11, 0, tzinfo=timezone.utc),
            "actor_id": "user-1", "actor_name": "Operator", "actor_type": "user",
            "auth_method": "oauth", "request_id": "req-model-test", "method": "POST",
            "action": "update", "target_type": "llm_model", "target_id": "model-1",
            "target_name": "Model One", "outcome": "success", "http_status": 200,
        }

        self.assertTrue(hasattr(operation_audit, "build_kafka_record"))
        first = operation_audit.build_kafka_record(entry, "test")
        second = operation_audit.build_kafka_record(entry, "test")

        self.assertNotEqual(first["event_id"], second["event_id"])
        self.assertEqual(uuid.UUID(first["event_id"]).version, 7)
        self.assertEqual(first["event_name"], "model_manager.operation.observed")
        self.assertEqual(first["source_id"], "model-manager")
        self.assertEqual(first["scope"]["business_module"], "model_management")
        self.assertEqual(first["target"], {"type": "llm_model", "id": "model-1", "name": "Model One"})
        self.assertEqual(first["facts"], {"action": "update", "decision": "allowed"})
        self.assertNotIn("before_hash", first["facts"])
        self.assertNotIn("after_hash", first["facts"])

    def test_trace_valid_token_shaped_request_id_uses_stable_audit_alias(self):
        entry = {
            "event_time": datetime(2026, 9, 28, 11, 0, tzinfo=timezone.utc),
            "actor_id": "user-1", "actor_name": "Operator", "actor_type": "user",
            "auth_method": "oauth", "request_id": "req_bkn_abcdefghijkl", "method": "POST",
            "action": "update", "target_type": "llm_model", "target_id": "model-1",
            "target_name": "Model One", "outcome": "success", "http_status": 200,
        }

        first = operation_audit.build_kafka_record(entry, "test")
        second = operation_audit.build_kafka_record(entry, "test")

        self.assertNotEqual(first["correlation"]["request_id"], entry["request_id"])
        self.assertEqual(first["correlation"]["request_id"], second["correlation"]["request_id"])
        self.assertTrue(first["correlation"]["request_id"].startswith("req_"))

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


class TestOperationAuditFailureReporting(unittest.IsolatedAsyncioTestCase):
    async def test_management_route_publishes_kafka_audit_without_local_write(self):
        async def receive():
            return {"type": "http.request", "body": b'{"model_id":"model-1"}', "more_body": False}

        request = Request({
            "type": "http", "method": "POST", "path": "/api/mf-model-manager/v1/llm/edit",
            "headers": [(b"x-account-id", b"user-1"), (b"bkn-request-id", b"req-reused")],
            "query_string": b"", "path_params": {},
        }, receive)

        async def call_next(_request):
            return Response(status_code=200)

        published = []
        publisher = types.SimpleNamespace(publish=published.append, environment="test")
        with patch.object(operation_audit, "_audit_publisher", publisher, create=True), \
             patch.object(operation_audit, "_actor_name", return_value="user-1"):
            response = await operation_audit.operation_audit_middleware(request, call_next)

        self.assertEqual(response.status_code, 200)
        self.assertEqual(len(published), 1)
        self.assertEqual(published[0]["event_name"], "model_manager.operation.observed")
        self.assertEqual(published[0]["target"]["id"], "model-1")

    async def test_missing_target_uses_safe_request_alias(self):
        async def receive():
            return {"type": "http.request", "body": b"{}", "more_body": False}

        request = Request({
            "type": "http", "method": "POST", "path": "/api/mf-model-manager/v1/llm/add",
            "headers": [(b"x-account-id", b"user-1"), (b"bkn-request-id", b"req_bkn_abcdefghijkl")],
            "query_string": b"", "path_params": {},
        }, receive)

        async def call_next(_request):
            return Response(status_code=400)

        published = []
        publisher = types.SimpleNamespace(publish=published.append, environment="test")
        with patch.object(operation_audit, "_audit_publisher", publisher), \
             patch.object(operation_audit, "_actor_name", return_value="user-1"):
            await operation_audit.operation_audit_middleware(request, call_next)

        self.assertEqual(len(published), 1)
        self.assertNotIn("bkn_abcdefghijkl", published[0]["target"]["id"])
        self.assertEqual(published[0]["target"]["id"], published[0]["target"]["name"])

    async def test_reports_audit_publish_failure_without_overturning_management_response(self):
        async def receive():
            return {"type": "http.request", "body": b'{"model_id":"model-1"}', "more_body": False}

        request = Request({
            "type": "http",
            "method": "POST",
            "path": "/api/mf-model-manager/v1/llm/edit",
            "headers": [
                (b"x-account-id", b"user-1"),
                (b"bkn-request-id", b"req-audit-write-failure"),
            ],
            "query_string": b"",
            "path_params": {},
        }, receive)

        async def call_next(_request):
            return Response(status_code=200)

        publisher = types.SimpleNamespace(publish=lambda _record: (_ for _ in ()).throw(RuntimeError("broker unavailable")), environment="test")
        with patch.object(operation_audit, "_actor_name", return_value="user-1"), \
             patch.object(operation_audit, "_audit_publisher", publisher), \
             self.assertLogs("app.utils.operation_audit", level="ERROR") as logs:
            response = await operation_audit.operation_audit_middleware(request, call_next)

        self.assertEqual(response.status_code, 200)
        self.assertTrue(any(
            "operation_audit_publish_failed" in message and
            "update" in message
            for message in logs.output
        ))

    async def test_publishes_actor_scoped_audit_without_removed_platform_fields(self):
        async def receive():
            return {"type": "http.request", "body": b'{"model_id":"model-1"}', "more_body": False}

        request = Request({
            "type": "http",
            "method": "POST",
            "path": "/api/mf-model-manager/v1/llm/edit",
            "headers": [
                (b"x-account-id", b"user-1"),
                (b"bkn-request-id", b"req-audit-actor-only"),
            ],
            "query_string": b"",
            "path_params": {},
        }, receive)

        async def call_next(_request):
            return Response(status_code=200)

        captured = []
        publisher = types.SimpleNamespace(publish=captured.append, environment="test")
        with patch.object(operation_audit, "_actor_name", return_value="user-1"), \
             patch.object(operation_audit, "_audit_publisher", publisher):
            response = await operation_audit.operation_audit_middleware(request, call_next)

        self.assertEqual(response.status_code, 200)
        self.assertNotIn("tenant_id", captured[0])
        self.assertNotIn("business_domain_id", captured[0])


if __name__ == "__main__":
    unittest.main()
