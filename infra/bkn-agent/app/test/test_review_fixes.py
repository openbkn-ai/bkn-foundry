"""PR #237 审核意见回归：错误封套扁平、PUT 冲突不 500、任务启动回收。"""
import asyncio

from fastapi.testclient import TestClient
from sqlalchemy.exc import IntegrityError

from app.main import app

client = TestClient(app, raise_server_exceptions=False)
USER = {"x-account-id": "u-1", "x-account-type": "user"}


# ---------- #1 ErrorEnvelope 顶层扁平 ----------

def test_business_error_envelope_is_flat():
    """业务错误（401）顶层就是 {code,...}，不是 {"detail":{...}} 嵌套。"""
    r = client.get("/api/bkn-agent/v1/agents")  # 无身份头 → 401
    body = r.json()
    assert r.status_code == 401
    assert body["code"] == "BknAgent.Auth.AccountRequired"
    assert set(body) >= {"code", "description", "detail", "solution", "link"}


def test_route_404_wrapped_into_envelope():
    """不存在的路由默认 detail 是串，也补齐成封套（非裸 {"detail":"Not Found"}）。"""
    r = client.get("/api/bkn-agent/v1/nope-not-a-route", headers=USER)
    body = r.json()
    assert r.status_code == 404
    assert body["code"] == "BknAgent.Http.404"
    assert "code" in body and "detail" in body


def test_method_not_allowed_wrapped():
    r = client.request("PATCH", "/api/v1/health")  # health 只有 GET
    assert r.status_code == 405
    assert r.json()["code"] == "BknAgent.Http.405"


# ---------- #3 PUT 名称冲突 → 冲突封套，非 500 ----------

def test_put_agent_name_conflict_not_500(monkeypatch):
    from app import dao
    from app.db import get_session

    async def fake_session():
        yield object()

    async def fake_update(session, agent_id, spec, account_id):
        raise IntegrityError("UPDATE ...", {}, Exception("Duplicate entry for key f_name"))

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "update_agent", fake_update)
    monkeypatch.setattr("app.routers.agents.toolbox_sync.schedule_resync", lambda: None)
    try:
        r = client.put(
            "/api/bkn-agent/v1/agents/a-1",
            headers={**USER, "Content-Type": "application/json"},
            json={"name": "dup_name", "mode": "chat", "status": "draft"},
        )
        assert r.status_code == 400  # 不是 500
        assert "Conflict" in r.json()["code"]
    finally:
        app.dependency_overrides.clear()


# ---------- #2 启动回收悬挂任务 ----------

class _FakeResult:
    rowcount = 3


class _FakeSession:
    def __init__(self):
        self.executed = []
        self.committed = False

    async def execute(self, stmt):
        self.executed.append(stmt)
        return _FakeResult()

    async def commit(self):
        self.committed = True


def test_recover_stale_tasks_marks_and_commits():
    from app import dao

    s = _FakeSession()
    n = asyncio.run(dao.recover_stale_tasks(s))
    assert n == 3 and s.committed and len(s.executed) == 1


def test_recover_on_startup_is_nonblocking(monkeypatch):
    """DB 不可用时启动回收只告警不抛（不阻断服务启动）。"""
    import app.main as main_mod

    class _Boom:
        def __call__(self):
            raise RuntimeError("db down")

    # SessionLocal() 抛错 → _recover_stale_tasks 吞掉
    monkeypatch.setattr("app.db.SessionLocal", _Boom())
    asyncio.run(main_mod._recover_stale_tasks())  # 不应抛
