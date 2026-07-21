from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)
SVC = {"x-account-id": "svc-test", "x-account-type": "app"}


def test_health():
    r = client.get("/api/v1/health")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"
    assert r.headers["x-trace-id"]
    assert r.headers["bkn-request-id"].startswith("req_")
    assert r.headers["x-request-id"] == r.headers["bkn-request-id"]
    assert r.headers["traceparent"].startswith(f"00-{r.headers['x-trace-id']}-")


def test_trace_context_propagates_request_id_and_traceparent():
    trace_id = "1234567890abcdef1234567890abcdef"
    span_id = "1234567890abcdef"
    r = client.get(
        "/api/v1/health",
        headers={
            "traceparent": f"00-{trace_id}-{span_id}-01",
            "bkn-request-id": "req_external_123",
        },
    )
    assert r.status_code == 200
    assert r.headers["x-trace-id"] == trace_id
    assert r.headers["bkn-request-id"] == "req_external_123"
    assert r.headers["x-request-id"] == "req_external_123"
    assert r.headers["traceparent"] == f"00-{trace_id}-{span_id}-01"


def test_traceparent_is_normalized_before_response():
    trace_id = "ABCDEFABCDEFABCDEFABCDEFABCDEFAB"
    span_id = "ABCDEFABCDEFABCD"
    r = client.get(
        "/api/v1/health",
        headers={
            "traceparent": f" 00-{trace_id}-{span_id}-01 ",
            "bkn-request-id": "req_external_124",
        },
    )
    assert r.status_code == 200
    assert r.headers["x-trace-id"] == trace_id.lower()
    assert r.headers["traceparent"] == f"00-{trace_id.lower()}-{span_id.lower()}-01"


def test_invalid_traceparent_is_not_reused():
    r = client.get(
        "/api/v1/health",
        headers={
            "traceparent": "00-00000000000000000000000000000000-0000000000000000-01",
            "bkn-request-id": "req_external_456",
        },
    )
    assert r.status_code == 200
    assert r.headers["x-trace-id"] != "00000000000000000000000000000000"
    assert r.headers["bkn-request-id"] == "req_external_456"


def test_auth_fail_closed_without_identity():
    r = client.get("/api/bkn-agent/v1/agents")
    assert r.status_code == 401
    # 顶层扁平 ErrorEnvelope（非 {"detail": {...}} 嵌套）
    assert r.json()["code"] == "BknAgent.Auth.AccountRequired"
    assert "detail" in r.json() and isinstance(r.json()["detail"], str)
    assert r.json()["trace_id"] == r.headers["x-trace-id"]


def test_auth_rejects_anonymous():
    r = client.get(
        "/api/bkn-agent/v1/agents",
        headers={"x-account-id": "x", "x-account-type": "anonymous"},
    )
    assert r.status_code == 401


def test_validation_error_uses_platform_error_shape():
    r = client.post("/api/bkn-agent/v1/chat", json={"agent_id": "a"}, headers=SVC)
    assert r.status_code == 400
    body = r.json()
    assert body["code"] == "BknAgent.ParamError.FormatError"
    assert body["solution"]
    assert body["trace_id"] == r.headers["x-trace-id"]


def test_unhandled_exception_preserves_trace_context(monkeypatch):
    from app import dao
    from app.db import get_session

    async def fake_session():
        yield None

    async def fail_list_agents(session, page, size):
        raise RuntimeError("db down")

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "list_agents", fail_list_agents)
    try:
        trace_id = "1234567890abcdef1234567890abcdef"
        span_id = "1234567890abcdef"
        error_client = TestClient(app, raise_server_exceptions=False)
        r = error_client.get(
            "/api/bkn-agent/v1/agents",
            headers={
                **SVC,
                "traceparent": f"00-{trace_id}-{span_id}-01",
                "bkn-request-id": "req_external_500",
            },
        )
    finally:
        app.dependency_overrides.pop(get_session, None)

    assert r.status_code == 500
    assert r.headers["x-trace-id"] == trace_id
    assert r.headers["bkn-request-id"] == "req_external_500"
    assert r.headers["x-request-id"] == "req_external_500"
    assert r.headers["traceparent"] == f"00-{trace_id}-{span_id}-01"
    assert r.json()["trace_id"] == trace_id


def test_thread_requires_identity():
    r = client.get("/api/bkn-agent/v1/threads/t-1")
    assert r.status_code == 401


def _override_session():
    async def fake_session():
        yield None

    from app.db import get_session

    app.dependency_overrides[get_session] = fake_session
    return get_session


def test_thread_missing_and_foreign_owner_indistinguishable(monkeypatch):
    """非 owner 与不存在必须同响应（不泄露 thread 存在性）。"""
    from app import dao
    from app.models import ThreadRow

    key = _override_session()
    try:
        async def none_row(session, thread_id):
            return None

        monkeypatch.setattr(dao, "get_thread_row", none_row)
        r_missing = client.get("/api/bkn-agent/v1/threads/t-1", headers=SVC)

        async def foreign_row(session, thread_id):
            return ThreadRow(
                f_thread_id=thread_id,
                f_agent_id="a-1",
                f_account_id="someone-else",
                f_create_time=1,
                f_update_time=1,
            )

        monkeypatch.setattr(dao, "get_thread_row", foreign_row)
        r_foreign = client.get("/api/bkn-agent/v1/threads/t-1", headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r_missing.status_code == r_foreign.status_code == 404
    missing_body = r_missing.json()
    foreign_body = r_foreign.json()
    assert missing_body.pop("trace_id") == r_missing.headers["x-trace-id"]
    assert foreign_body.pop("trace_id") == r_foreign.headers["x-trace-id"]
    assert missing_body == foreign_body


def test_thread_owner_reads_history(monkeypatch):
    from app import dao
    from app.models import ThreadMessage, ThreadRow
    from app.routers import threads as threads_router

    key = _override_session()
    try:
        async def own_row(session, thread_id):
            return ThreadRow(
                f_thread_id=thread_id,
                f_agent_id="a-1",
                f_account_id=SVC["x-account-id"],
                f_create_time=1,
                f_update_time=2,
            )

        async def fake_history(thread_id):
            return [ThreadMessage(role="user", content="hi"), ThreadMessage(role="assistant", content="hello")]

        monkeypatch.setattr(dao, "get_thread_row", own_row)
        monkeypatch.setattr(threads_router, "read_thread_messages", fake_history)
        r = client.get("/api/bkn-agent/v1/threads/t-1", headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r.status_code == 200
    body = r.json()
    assert body["agent_id"] == "a-1"
    assert [m["role"] for m in body["messages"]] == ["user", "assistant"]


def test_task_read_is_owner_scoped(monkeypatch):
    """P0 回归：GET /tasks/{id} 必须按 account 过滤，越权与不存在同响应（404）。"""
    from app import dao
    from app.db import get_session
    from app.models import TaskOut

    class _S:
        pass

    async def fake_session():
        yield _S()

    seen = {}

    async def fake_get_task(session, task_id, account_id=None):
        seen["account_id"] = account_id
        if account_id != "owner":  # dao 侧过滤：非 owner 返回 None
            return None
        return TaskOut(
            task_id=task_id, agent_id="a-1", status="succeeded", input={"message": "secret"},
            output="42", create_time=1, update_time=2,
        )

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "get_task", fake_get_task)

    r = client.get("/api/bkn-agent/v1/tasks/t-1", headers={"x-account-id": "intruder", "x-account-type": "user"})
    assert r.status_code == 404
    assert seen["account_id"] == "intruder"  # 归属条件真的传下去了

    r_ok = client.get("/api/bkn-agent/v1/tasks/t-1", headers={"x-account-id": "owner", "x-account-type": "user"})
    assert r_ok.status_code == 200 and r_ok.json()["output"] == "42"
    app.dependency_overrides.pop(get_session, None)
