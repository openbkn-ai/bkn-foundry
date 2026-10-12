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

    def test_disabled_publisher_does_not_request_extra_snapshot_lookups(self):
        request=object()
        with patch.object(operation_audit,"_audit_publisher",None):
            self.assertIsNone(operation_audit.operation_audit_request(request))
        with patch.object(operation_audit,"_audit_publisher",object()):
            self.assertIs(operation_audit.operation_audit_request(request),request)

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
    def request(self, body=b"{}", path="/api/mf-model-manager/v1/llm/edit", headers=()):
        async def receive():
            return {"type": "http.request", "body": body, "more_body": False}
        return Request({"type":"http", "method":"POST", "path":path,
            "headers": [(b"x-account-id",b"user-1"),(b"bkn-request-id",b"req-reused"), *headers],
            "query_string":b"", "path_params":{}}, receive)

    async def test_publishes_only_source_committed_target(self):
        request = self.request(b'{"model_id":"wrong-client-id"}')
        async def call_next(request):
            operation_audit.set_operation_audit_target(request, ["actual-id"], ["采购助手"])
            return Response(status_code=200)
        published=[]
        with patch.object(operation_audit,"_audit_publisher",types.SimpleNamespace(publish=published.append,environment="test")), patch.object(operation_audit,"_actor_name",return_value="张三"):
            response=await operation_audit.operation_audit_middleware(request,call_next)
        self.assertEqual(response.status_code,200)
        self.assertEqual(published[0]["target"],{"type":"llm_model","id":"actual-id","name":"采购助手"})
        self.assertEqual(published[0]["summary"],"修改大模型“采购助手”")

    async def test_fastapi_preserves_source_snapshot_through_middleware(self):
        app=FastAPI()
        @app.post("/api/mf-model-manager/v1/llm/add")
        async def add_model(request: Request, payload: dict = Body(...)):
            operation_audit.set_operation_audit_target(request,["actual-created-id"],[payload["model_name"]])
            return {"id":"actual-created-id"}
        app.add_middleware(BaseHTTPMiddleware,dispatch=operation_audit.operation_audit_middleware)
        published=[]
        with patch.object(operation_audit,"_actor_name",return_value="张三"),patch.object(operation_audit,"_audit_publisher",types.SimpleNamespace(publish=published.append,environment="test")):
            response=TestClient(app).post("/api/mf-model-manager/v1/llm/add",json={"model_name":"采购助手"},headers={"x-account-id":"verified-user"})
        self.assertEqual(response.status_code,200)
        self.assertEqual(len(published),1)
        self.assertEqual(published[0]["target"]["id"],response.json()["id"])
        self.assertEqual(published[0]["target"]["name"],"采购助手")

    async def test_uncommitted_invalid_bodies_and_denials_do_not_publish(self):
        for body in (b"[]",b"null",b"invalid",b"{}"):
            for status in (200,202,400,401,403,409,422,500):
                async def call_next(request):
                    return Response(status_code=status)
                published=[]
                with patch.object(operation_audit,"_audit_publisher",types.SimpleNamespace(publish=published.append,environment="test")):
                    response=await operation_audit.operation_audit_middleware(self.request(body),call_next)
                self.assertEqual(response.status_code,status)
                self.assertEqual(published,[])

    async def test_directory_name_ignores_client_header_and_rejects_id_fallback(self):
        from unittest.mock import AsyncMock
        for name,expected in (("张三","张三"),("user-1",""),("","")):
            with patch.object(operation_audit,"get_username_by_ids",AsyncMock(return_value={"user-1":name})):
                self.assertEqual(await operation_audit._actor_name("user-1",{"x-account-name":"Administrator"}),expected)

    async def test_audit_failures_preserve_management_response(self):
        async def call_next(request):
            operation_audit.set_operation_audit_target(request,["actual-id"],["采购助手"])
            return Response(status_code=200)
        publisher=types.SimpleNamespace(publish=lambda record: (_ for _ in ()).throw(RuntimeError("broker unavailable")),environment="test")
        with patch.object(operation_audit,"_actor_name",return_value="张三"), patch.object(operation_audit,"_audit_publisher",publisher), self.assertLogs("app.utils.operation_audit",level="ERROR"):
            response=await operation_audit.operation_audit_middleware(self.request(),call_next)
        self.assertEqual(response.status_code,200)

    async def test_missing_actor_name_does_not_publish(self):
        async def call_next(request):
            operation_audit.set_operation_audit_target(request,["actual-id"],["采购助手"])
            return Response(status_code=200)
        published=[]
        with patch.object(operation_audit,"_actor_name",return_value=""), patch.object(operation_audit,"_audit_publisher",types.SimpleNamespace(publish=published.append,environment="test")):
            response=await operation_audit.operation_audit_middleware(self.request(),call_next)
        self.assertEqual(response.status_code,200)
        self.assertEqual(published,[])

    async def test_batch_snapshot_and_schema_limits(self):
        request=self.request()
        operation_audit.set_operation_audit_target(request,[str(i) for i in range(6)],["采购","库存","销售","财务","生产","运输"])
        target_id,name=request.state.operation_audit_target
        self.assertTrue(target_id.startswith("batch:"))
        self.assertIn("采购",name)
        self.assertIn("共6项",name)
        self.assertIn("已截断",name)
        entry={"event_time":datetime.now(timezone.utc),"actor_id":"user-1","actor_name":"张三","actor_type":"user","auth_method":"oauth","request_id":"req-a","method":"POST","action":"delete","target_type":"llm_model","target_id":target_id,"target_name":name,"outcome":"success","http_status":200}
        self.assertEqual(operation_audit.build_kafka_record(entry,"test")["target"]["name"],name)
        for name in ("",target_id,"长"*513):
            entry["target_name"]=name
            with self.assertRaises(ValueError):
                operation_audit.build_kafka_record(entry,"test")

    def controller_functions(self, filename, **namespace):
        # Execute the actual controller functions with isolated infrastructure mocks.
        import ast
        source=(Path(__file__).parents[1]/"controller"/filename).read_text()
        module=ast.parse("from __future__ import annotations\n")
        module.body += [node for node in ast.parse(source).body if isinstance(node,(ast.FunctionDef,ast.AsyncFunctionDef))]
        exec(compile(module,filename,"exec"),namespace)
        return namespace

    async def test_llm_delete_preserves_predelete_names_and_committed_failure(self):
        from unittest.mock import AsyncMock,MagicMock
        from starlette.responses import JSONResponse
        for cache_fails in (False,True):
            request=self.request(path="/api/mf-model-manager/v1/llm/delete")
            redis=types.SimpleNamespace(delete_str=AsyncMock(side_effect=RuntimeError("cache unavailable") if cache_fails else None))
            dao=types.SimpleNamespace(get_all_model_list=lambda:[{"f_model_id":"actual-a","f_model_name":"采购助手"},{"f_model_id":"actual-b","f_model_name":"库存助手"}],delete_model_by_id=MagicMock())
            namespace=self.controller_functions("llm_controller.py",set_operation_audit_target=operation_audit.set_operation_audit_target,llm_model_dao=dao,model_quota_dao=types.SimpleNamespace(delete_model_quota_by_model_id=MagicMock()),permission_manager=types.SimpleNamespace(check_single_permission=AsyncMock(return_value=True),delete_permission=AsyncMock()),redis_util=redis,quota_config_cache_tree=types.SimpleNamespace(delete_batch=MagicMock()),StandLogger=MagicMock(),JSONResponse=JSONResponse,RequestValidationError=ValueError,LLMRemoveError={})
            async def call_next(request):
                return await namespace["remove_model"]({"model_ids":["actual-a","actual-b"]},"user-1","zh-CN",audit_request=request)
            published=[]
            with patch.object(operation_audit,"_actor_name",return_value="张三"),patch.object(operation_audit,"_audit_publisher",types.SimpleNamespace(publish=published.append,environment="test")):
                response=await operation_audit.operation_audit_middleware(request,call_next)
            dao.delete_model_by_id.assert_called_once()
            self.assertEqual(len(published),1)
            self.assertIn("采购助手",published[0]["target"]["name"])
            self.assertEqual(published[0]["outcome"],"failure" if cache_fails else "success")
            self.assertEqual(response.status_code,400 if cache_fails else 200)

    async def test_user_quota_delete_preserves_user_and_model_relationship(self):
        from unittest.mock import AsyncMock,MagicMock
        from starlette.responses import JSONResponse
        request=self.request(path="/api/mf-model-manager/v1/user-quota/delete")
        dao=types.SimpleNamespace(
            get_model_name_by_quota_config_id=lambda ids:[{"f_model_name":"采购助手"}],
            get_user_model_config=lambda conf_id:[{"f_id":conf_id,"f_user_id":"user-a","f_model_conf":"model-conf"}],
            get_model_config=lambda conf_id:[{"f_model_name":"采购助手"}],
            delete_user_model_quota_config_by_id_list=MagicMock())
        namespace=self.controller_functions("model_quota_controller.py",set_operation_audit_target=operation_audit.set_operation_audit_target,model_quota_dao=dao,get_username_by_ids=AsyncMock(return_value={"user-a":"李四"}),redis_util=types.SimpleNamespace(delete_str=AsyncMock()),StandLogger=MagicMock(),JSONResponse=JSONResponse,ModelFactory_MyPymysqlPool_Connection_ConnectError_Error={})
        async def call_next(request):
            return await namespace["delete_user_model_quota_config"](["user-conf-a"],"user-1",audit_request=request)
        published=[]
        with patch.object(operation_audit,"_actor_name",return_value="张三"),patch.object(operation_audit,"_audit_publisher",types.SimpleNamespace(publish=published.append,environment="test")):
            response=await operation_audit.operation_audit_middleware(request,call_next)
        self.assertEqual(response.status_code,200)
        self.assertEqual(published[0]["target"],{"type":"user_model_quota","id":"user-conf-a","name":"用户“李四”在模型“采购助手”的配额"})
        dao.delete_user_model_quota_config_by_id_list.assert_called_once_with(["user-conf-a"])

    async def test_quota_snapshot_uses_configuration_id_and_model_name(self):
        from unittest.mock import MagicMock
        request=self.request(path="/api/mf-model-manager/v1/model-quota")
        namespace=self.controller_functions("model_quota_controller.py",set_operation_audit_target=operation_audit.set_operation_audit_target,llm_model_dao=types.SimpleNamespace(get_model_name_by_id=lambda model_id:"采购助手"),StandLogger=MagicMock())
        namespace["_audit_model_quota"](request,"actual-conf-id","model-id")
        self.assertEqual(request.state.operation_audit_target,("actual-conf-id","模型“采购助手”的配额"))


if __name__ == "__main__":
    unittest.main()
