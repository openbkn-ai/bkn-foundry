"""toolbox 注册包构造与 /invoke 端点（#212）。"""
from fastapi.testclient import TestClient

from app.bootstrap import toolbox_sync
from app.main import app
from app.models import AgentOut, TaskOut

client = TestClient(app)
SVC = {"x-account-id": "svc-test", "x-account-type": "app"}


def _agent(agent_id: str = "a-1", status: str = "published") -> AgentOut:
    return AgentOut(
        agent_id=agent_id,
        name=f"agent-{agent_id}",
        mode="task",
        status=status,
        create_user="u-1",
        update_user="u-1",
        create_time=1000,
        update_time=2000,
    )


def test_package_version_equals_source_id():
    """.adp 硬约束：metadata.version == source_id，破了导入静默坏 metadata。"""
    pkg = toolbox_sync.build_package([_agent("a-1"), _agent("a-2")])
    tools = pkg["toolbox"]["configs"][0]["tools"]
    assert len(tools) == 2
    for t in tools:
        assert t["source_id"] == t["metadata"]["version"]


def test_package_ids_deterministic_and_path_pinned():
    t1 = toolbox_sync.build_package([_agent("a-1")])["toolbox"]["configs"][0]["tools"][0]
    t2 = toolbox_sync.build_package([_agent("a-1")])["toolbox"]["configs"][0]["tools"][0]
    assert t1["tool_id"] == t2["tool_id"]
    assert t1["source_id"] == t2["source_id"]
    assert t1["metadata"]["path"] == "/api/bkn-agent/v1/invoke/a-1"
    assert toolbox_sync.build_package([])["toolbox"]["configs"][0]["box_id"] == toolbox_sync.BOX_ID


def test_invoke_requires_identity():
    r = client.post("/api/bkn-agent/v1/invoke/a-1", json={"message": "hi"})
    assert r.status_code == 401


class _FakeSession:
    def expire_all(self):
        pass


def _override_session():
    async def fake_session():
        yield _FakeSession()

    from app.db import get_session

    app.dependency_overrides[get_session] = fake_session
    return get_session


def test_invoke_draft_agent_hidden(monkeypatch):
    from app import dao

    key = _override_session()
    try:
        async def draft(session, agent_id):
            return _agent(agent_id, status="draft")

        monkeypatch.setattr(dao, "get_agent", draft)
        r = client.post("/api/bkn-agent/v1/invoke/a-1", json={"message": "hi"}, headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r.status_code == 404


def test_invoke_waits_for_terminal_state(monkeypatch):
    from app import dao
    from app.core import runner

    key = _override_session()
    try:
        async def published(session, agent_id):
            return _agent(agent_id)

        async def create_task(session, agent_id, task_input, account_id, parent_thread_id=None):
            return TaskOut(task_id="t-1", agent_id=agent_id, status="pending", create_time=1, update_time=1)

        executed = {}

        async def execute(task_id, agent, req_input, account_id, account_type):
            executed["task_id"] = task_id

        async def get_task(session, task_id):
            return TaskOut(task_id=task_id, agent_id="a-1", status="succeeded", output="done", create_time=1, update_time=2)

        monkeypatch.setattr(dao, "get_agent", published)
        monkeypatch.setattr(dao, "create_task", create_task)
        monkeypatch.setattr(runner, "execute_task", execute)
        monkeypatch.setattr(dao, "get_task", get_task)
        r = client.post("/api/bkn-agent/v1/invoke/a-1", json={"message": "hi"}, headers=SVC)
    finally:
        app.dependency_overrides.pop(key, None)

    assert r.status_code == 200
    assert executed["task_id"] == "t-1"
    body = r.json()
    assert body["status"] == "succeeded"
    assert body["output"] == "done"
