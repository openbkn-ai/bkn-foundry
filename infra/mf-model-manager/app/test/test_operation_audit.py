import sys
import types
import unittest
from unittest.mock import patch

from starlette.requests import Request
from starlette.responses import Response

get_user_info = types.ModuleType("app.commons.get_user_info")
get_user_info.get_username_by_ids = None
sys.modules.setdefault("app.commons.get_user_info", get_user_info)
database_pool = types.ModuleType("app.mydb.pymysql_pool")
database_pool.PymysqlPool = object
sys.modules.setdefault("app.mydb.pymysql_pool", database_pool)

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


class TestOperationAuditFailureReporting(unittest.IsolatedAsyncioTestCase):
    async def test_reports_audit_write_failure_without_overturning_management_response(self):
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

        with patch.object(operation_audit, "_actor_name", return_value="user-1"), \
             patch.object(operation_audit, "_write", side_effect=RuntimeError("database unavailable")), \
             self.assertLogs("app.utils.operation_audit", level="ERROR") as logs:
            response = await operation_audit.operation_audit_middleware(request, call_next)

        self.assertEqual(response.status_code, 200)
        self.assertTrue(any(
            "operation_audit_write_failed" in message and
            "req-audit-write-failure" in message and
            "update" in message
            for message in logs.output
        ))

    async def test_writes_actor_scoped_audit_without_removed_platform_fields(self):
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
        with patch.object(operation_audit, "_actor_name", return_value="user-1"), \
             patch.object(operation_audit, "_write", side_effect=captured.append):
            response = await operation_audit.operation_audit_middleware(request, call_next)

        self.assertEqual(response.status_code, 200)
        self.assertNotIn("tenant_id", captured[0])
        self.assertNotIn("business_domain_id", captured[0])


class TestOperationAuditLegacySchemaFallback(unittest.TestCase):
    """A 0.1.5 image can start against a database that has not run the 0.1.5
    migration; the audit write must still land instead of being lost."""

    def _write_with_cursor(self, cursor):
        class _Connection:
            def cursor(self_inner):
                return cursor

            def commit(self_inner):
                self_inner.committed = True

            def close(self_inner):
                pass

        class _Pool:
            def connection(self_inner):
                return _Connection()

        class _PymysqlPool:
            @staticmethod
            def get_pool():
                return _Pool()

        with patch.object(operation_audit, "PymysqlPool", _PymysqlPool):
            operation_audit._write({"event_id": "evt-1"})

    def test_retries_with_legacy_column_when_database_is_not_migrated(self):
        executed = []

        class _Cursor:
            def execute(self_inner, statement, entry):
                executed.append((statement, entry))
                if len(executed) == 1:
                    raise RuntimeError(
                        "(1364, \"Field 'business_domain_id' doesn't have a default value\")"
                    )

            def close(self_inner):
                pass

        self._write_with_cursor(_Cursor())

        self.assertEqual(len(executed), 2)
        self.assertNotIn("business_domain_id", executed[0][0])
        self.assertIn("business_domain_id", executed[1][0])
        self.assertEqual(executed[1][1]["business_domain_id"], "")

    def test_reraises_unrelated_database_errors(self):
        class _Cursor:
            def execute(self_inner, statement, entry):
                raise RuntimeError("(1146, \"Table 't_model_manager_operation_audit' doesn't exist\")")

            def close(self_inner):
                pass

        with self.assertRaises(RuntimeError):
            self._write_with_cursor(_Cursor())


if __name__ == "__main__":
    unittest.main()
