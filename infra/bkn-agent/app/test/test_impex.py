"""导入导出：roundtrip 语义、同名冲突不中断、引用 warning、resync 触发。"""
from fastapi.testclient import TestClient

from app import dao
from app.bootstrap import toolbox_sync
from app.db import get_session
from app.main import app
from app.models import AgentOut, PromptOut

client = TestClient(app)
HDR = {"x-account-id": "u-1", "x-account-type": "user"}


class _FakeSession:
    async def rollback(self):
        pass


async def _fake_session():
    yield _FakeSession()


def _agent(agent_id: str, name: str, tools=None, prompt_id=None) -> AgentOut:
    return AgentOut(
        agent_id=agent_id,
        name=name,
        mode="chat",
        prompt_id=prompt_id,
        tools=tools or [],
        status="published",
        create_user="u-1",
        update_user="u-1",
        create_time=1,
        update_time=1,
    )


def test_export_then_import_roundtrip(monkeypatch):
    app.dependency_overrides[get_session] = _fake_session
    agents = {
        "a-1": _agent("a-1", "helper", prompt_id="p-1"),
        "a-2": _agent("a-2", "oneshot", tools=[{"type": "agent", "agent_id": "a-1"}]),
    }
    prompts = {
        "p-1": PromptOut(
            prompt_id="p-1", name="hp", current_version=3, content="你是助手",
            vars_schema=None, update_user="u-1", update_time=1,
        )
    }

    async def fake_get_agent(session, agent_id):
        return agents.get(agent_id)

    async def fake_get_prompt(session, prompt_id):
        return prompts.get(prompt_id)

    monkeypatch.setattr(dao, "get_agent", fake_get_agent)
    monkeypatch.setattr(dao, "get_prompt", fake_get_prompt)

    r = client.post("/api/bkn-agent/v1/export", json={"agent_ids": ["a-1", "a-2"]}, headers=HDR)
    assert r.status_code == 200, r.text
    pkg = r.json()
    assert pkg["format"] == "bkn-agent/v1"
    assert len(pkg["items"]) == 2
    assert pkg["items"][0]["prompt"]["content"] == "你是助手"
    assert pkg["items"][1]["prompt"] is None

    upserts = []

    async def fake_upsert_agent(session, agent_id, spec, account_id):
        upserts.append(agent_id)
        return _agent(agent_id, spec.name), "created"

    async def fake_upsert_prompt(session, prompt_id, name, content, vars_schema, account_id):
        return "version_published"

    resynced = []
    monkeypatch.setattr(dao, "upsert_agent_with_id", fake_upsert_agent)
    monkeypatch.setattr(dao, "upsert_prompt_with_id", fake_upsert_prompt)
    monkeypatch.setattr(toolbox_sync, "schedule_resync", lambda: resynced.append(1))

    r = client.post("/api/bkn-agent/v1/import", json={"package": pkg}, headers=HDR)
    assert r.status_code == 200, r.text
    body = r.json()
    assert [x["action"] for x in body["results"]] == ["created", "created"]
    assert body["results"][0]["prompt_action"] == "version_published"
    assert upserts == ["a-1", "a-2"]
    assert body["warnings"] == []  # a-2 引用的 a-1 在包内
    assert resynced == [1]
    app.dependency_overrides.pop(get_session, None)


def test_import_name_conflict_isolated_and_missing_ref_warns(monkeypatch):
    app.dependency_overrides[get_session] = _fake_session

    async def fake_upsert_agent(session, agent_id, spec, account_id):
        if spec.name == "taken":
            raise ValueError("agent 名「taken」已被 a-x 占用")
        return _agent(agent_id, spec.name), "updated"

    async def fake_get_agent(session, agent_id):
        return None  # 引用的子 agent 目标环境不存在

    monkeypatch.setattr(dao, "upsert_agent_with_id", fake_upsert_agent)
    monkeypatch.setattr(dao, "get_agent", fake_get_agent)
    monkeypatch.setattr(toolbox_sync, "schedule_resync", lambda: None)

    pkg = {
        "format": "bkn-agent/v1",
        "exported_at": 1,
        "items": [
            {"agent_id": "a-1", "spec": {"name": "taken", "mode": "chat"}},
            {
                "agent_id": "a-2",
                "spec": {"name": "ok", "mode": "chat", "tools": [{"type": "agent", "agent_id": "ghost"}]},
            },
        ],
    }
    r = client.post("/api/bkn-agent/v1/import", json={"package": pkg}, headers=HDR)
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["results"][0]["action"] == "failed"
    assert "已被" in body["results"][0]["error"]
    assert body["results"][1]["action"] == "updated"  # 冲突不中断后续条目
    assert any("ghost" in w for w in body["warnings"])
    app.dependency_overrides.pop(get_session, None)


def test_impex_requires_identity():
    r = client.post("/api/bkn-agent/v1/export", json={"agent_ids": ["a-1"]})
    assert r.status_code == 401
