"""/invoke 端点行为与 AgentSpec.name 字符集校验。

这四条原本住在 test_toolbox_sync.py 里，与 toolbox 同步无关；同步删除时
一并搬到这里。/invoke 是 agent 收敛后唯一的对外调用面，不能没有行为测试
（test_contract.py 只比对 app.openapi() 与冻结 spec 的结构，不跑运行时）。
"""
from fastapi.testclient import TestClient

from app.main import app
from app.models import AgentOut, TaskOut

client = TestClient(app)
SVC = {"x-account-id": "svc-test", "x-account-type": "app"}


def _agent(agent_id: str = "a-1", status: str = "published") -> AgentOut:
    return AgentOut(
        agent_id=agent_id,
        name=f"agent_{agent_id.replace('-', '_')}",  # 名字字符集受 AgentSpec 校验约束
        mode="task",
        status=status,
        create_user="u-1",
        update_user="u-1",
        create_time=1000,
        update_time=2000,
    )


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
    """draft agent 与不存在同响应，不泄露存在性（tasks.py 的 status != published 分支）。"""
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
    """同步等到终态才返回。终态由 runner 在独立 session 写入，
    不 expire_all 会读到本 session 的旧缓存。"""
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


def test_agent_name_charset_enforced_at_api():
    """name pattern 仍在 models.py 生效，需要有测试守着。"""
    import pytest
    from pydantic import ValidationError

    from app.models import AgentSpec

    AgentSpec(name="my_agent_2")  # ok
    AgentSpec(name="订单助手")  # 汉字 ok
    for bad in ("My Agent", "my-agent", "agent!", "agent.v2"):
        with pytest.raises(ValidationError):
            AgentSpec(name=bad)
