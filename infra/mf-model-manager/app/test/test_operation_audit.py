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


class TestOperationAuditFailureReporting(unittest.IsolatedAsyncioTestCase):
    async def test_reports_audit_write_failure_without_overturning_management_response(self):
        async def receive():
            return {"type": "http.request", "body": b'{"model_id":"model-1"}', "more_body": False}

        request = Request({
            "type": "http",
            "method": "POST",
            "path": "/api/mf-model-manager/v1/llm/edit",
            "headers": [
                (b"x-tenant-id", b"tenant-1"),
                (b"x-business-domain", b"domain-1"),
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


if __name__ == "__main__":
    unittest.main()
