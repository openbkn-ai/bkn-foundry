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


def test_delete_thread_requires_identity():
    r = client.delete("/api/bkn-agent/v1/threads/t-1")
    assert r.status_code == 401


def test_delete_thread_missing_and_foreign_owner_indistinguishable(monkeypatch):
    from app import dao
    from app.models import ThreadRow
    from app.routers import threads as threads_router

    deleted = []

    async def record(*args):
        deleted.append(args)

    monkeypatch.setattr(threads_router, "delete_thread_checkpoints", record)
    monkeypatch.setattr(dao, "delete_thread_row", record)
    key = _override_session()
    try:
        async def none_row(session, thread_id):
            return None

        monkeypatch.setattr(dao, "get_thread_row", none_row)
        r_missing = client.delete("/api/bkn-agent/v1/threads/t-1", headers=SVC)

        async def foreign_row(session, thread_id):
            return ThreadRow(
                f_thread_id=thread_id,
                f_agent_id="a-1",
                f_account_id="someone-else",
                f_create_time=1,
                f_update_time=1,
            )

        monkeypatch.setattr(dao, "get_thread_row", foreign_row)
        r_foreign = client.delete("/api/bkn-agent/v1/threads/t-1", headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r_missing.status_code == r_foreign.status_code == 404
    missing_body = r_missing.json()
    foreign_body = r_foreign.json()
    assert missing_body.pop("trace_id") == r_missing.headers["x-trace-id"]
    assert foreign_body.pop("trace_id") == r_foreign.headers["x-trace-id"]
    assert missing_body == foreign_body
    assert deleted == []


def _own_thread_row(session, thread_id):
    from app.models import ThreadRow

    return ThreadRow(
        f_thread_id=thread_id,
        f_agent_id="a-1",
        f_account_id=SVC["x-account-id"],
        f_create_time=1,
        f_update_time=2,
    )


def _patch_delete(monkeypatch, calls):
    from app import dao
    from app.routers import threads as threads_router

    async def own_row(session, thread_id):
        return _own_thread_row(session, thread_id)

    async def drop_checkpoints(thread_id):
        calls.append(("checkpoints", thread_id))

    async def drop_row(session, thread_id):
        calls.append(("row", thread_id))

    monkeypatch.setattr(dao, "get_thread_row", own_row)
    monkeypatch.setattr(threads_router, "delete_thread_checkpoints", drop_checkpoints)
    monkeypatch.setattr(dao, "delete_thread_row", drop_row)


def test_delete_thread_drops_checkpoints_before_the_row(monkeypatch):
    from app.core import graph

    calls = []
    _patch_delete(monkeypatch, calls)
    key = _override_session()
    try:
        r = client.delete("/api/bkn-agent/v1/threads/t-1", headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r.status_code == 204
    assert calls == [("checkpoints", "t-1"), ("row", "t-1")]
    assert "t-1" not in graph._busy_threads


def test_delete_thread_keeps_the_row_when_checkpoints_fail(monkeypatch):
    from app.core import graph
    from app.routers import threads as threads_router

    calls = []
    _patch_delete(monkeypatch, calls)

    async def broken(thread_id):
        raise RuntimeError("checkpoint store down")

    monkeypatch.setattr(threads_router, "delete_thread_checkpoints", broken)
    key = _override_session()
    try:
        with TestClient(app, raise_server_exceptions=False) as failing:
            r = failing.delete("/api/bkn-agent/v1/threads/t-1", headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r.status_code == 500
    assert calls == []
    assert "t-1" not in graph._busy_threads


def test_delete_thread_refuses_a_running_chat_or_execution(monkeypatch):
    from app.core import graph
    from app.routers import executions

    calls = []
    _patch_delete(monkeypatch, calls)
    key = _override_session()
    try:
        graph._busy_threads.add("t-chat")
        executions._thread_claims.add("t-run")
        r_chat = client.delete("/api/bkn-agent/v1/threads/t-chat", headers=SVC)
        r_run = client.delete("/api/bkn-agent/v1/threads/t-run", headers=SVC)
        # A refused delete must not release the running turn's claim, nor take one.
        assert "t-chat" in graph._busy_threads
        assert "t-run" not in graph._busy_threads
    finally:
        graph._busy_threads.discard("t-chat")
        executions._thread_claims.discard("t-run")
        app.dependency_overrides.pop(key, None)

    assert r_chat.status_code == r_run.status_code == 409
    assert r_chat.json()["code"] == r_run.json()["code"] == "BknAgent.Thread.Busy"
    assert calls == []


def _agent_out(agent_id, owner):
    from app.models import AgentOut

    return AgentOut(
        agent_id=agent_id, name="a", mode="chat", model="", tools=[], skills=[],
        status="draft", create_user=owner, update_user=owner, create_time=1, update_time=2,
    )


def _override_get_agent(monkeypatch, returns):
    from app import dao
    from app.db import get_session

    async def fake_session():
        class _S:
            pass

        yield _S()

    async def fake_get_agent(session, agent_id):
        return returns

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "get_agent", fake_get_agent)


def test_update_agent_rejects_non_owner(monkeypatch):
    """写操作止血：非创建者不得修改他人 agent（此前 account 被解析后忽略）。"""
    _override_get_agent(monkeypatch, _agent_out("a-1", owner="owner"))
    body = {"name": "renamed"}
    r = client.put(
        "/api/bkn-agent/v1/agents/a-1", json=body,
        headers={"x-account-id": "intruder", "x-account-type": "user"},
    )
    assert r.status_code == 403, r.text
    app.dependency_overrides.clear()


def test_delete_agent_rejects_non_owner(monkeypatch):
    _override_get_agent(monkeypatch, _agent_out("a-1", owner="owner"))
    r = client.delete(
        "/api/bkn-agent/v1/agents/a-1",
        headers={"x-account-id": "intruder", "x-account-type": "user"},
    )
    assert r.status_code == 403, r.text
    app.dependency_overrides.clear()


def test_update_agent_allows_legacy_unowned(monkeypatch):
    """兼容：f_create_user 为空的存量 agent 不因归属校验被锁死。"""
    from app import dao

    _override_get_agent(monkeypatch, _agent_out("a-1", owner=""))

    async def fake_update(session, agent_id, spec, account_id):
        return _agent_out(agent_id, owner="")

    monkeypatch.setattr(dao, "update_agent", fake_update)
    r = client.put(
        "/api/bkn-agent/v1/agents/a-1", json={"name": "renamed"},
        headers={"x-account-id": "anyone", "x-account-type": "user"},
    )
    assert r.status_code == 200, r.text
    app.dependency_overrides.clear()


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
