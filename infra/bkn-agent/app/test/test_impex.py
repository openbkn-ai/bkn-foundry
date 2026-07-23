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

    async def commit(self):
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

    async def fake_upsert_agent(session, agent_id, spec, account_id, commit=True):
        upserts.append(agent_id)
        return _agent(agent_id, spec.name), "created"

    async def fake_upsert_prompt(session, prompt_id, name, content, vars_schema, account_id, commit=True):
        return "version_published"

    async def no_conflict(session, agent_id, agent_name, prompt_id, prompt_name, account_id=""):
        return None

    resynced = []
    monkeypatch.setattr(dao, "check_import_conflict", no_conflict)
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


def test_import_conflict_precheck_writes_nothing_and_isolates_item(monkeypatch):
    """P1 回归：名字冲突必须在写入前拦住。

    老实现先 upsert prompt（已 commit）再 upsert agent 撞名抛错，rollback 撤不掉
    已提交的 prompt 新版本——条目报 failed，线上绑该 prompt 的 agent 却静默换了词。
    """
    app.dependency_overrides[get_session] = _fake_session
    written = {"prompts": [], "agents": []}

    async def fake_conflict(session, agent_id, agent_name, prompt_id, prompt_name, account_id=""):
        return f"agent 名「{agent_name}」已被 a-x 占用" if agent_name == "taken" else None

    async def fake_upsert_agent(session, agent_id, spec, account_id, commit=True):
        written["agents"].append(agent_id)
        return _agent(agent_id, spec.name), "updated"

    async def fake_upsert_prompt(session, prompt_id, name, content, vars_schema, account_id, commit=True):
        written["prompts"].append(prompt_id)
        return "version_published"

    async def fake_get_agent(session, agent_id):
        return None  # 引用的子 agent 目标环境不存在

    monkeypatch.setattr(dao, "check_import_conflict", fake_conflict)
    monkeypatch.setattr(dao, "upsert_agent_with_id", fake_upsert_agent)
    monkeypatch.setattr(dao, "upsert_prompt_with_id", fake_upsert_prompt)
    monkeypatch.setattr(dao, "get_agent", fake_get_agent)
    monkeypatch.setattr(toolbox_sync, "schedule_resync", lambda: None)

    pkg = {
        "format": "bkn-agent/v1",
        "exported_at": 1,
        "items": [
            {
                "agent_id": "a-1",
                "spec": {"name": "taken", "mode": "chat"},
                "prompt": {"prompt_id": "p-1", "name": "hp", "content": "新内容"},
            },
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
    assert written["prompts"] == []  # 冲突条目的 prompt 一个字都没写
    assert body["results"][1]["action"] == "updated"  # 冲突不中断后续条目
    assert written["agents"] == ["a-2"]
    assert any("ghost" in w for w in body["warnings"])
    app.dependency_overrides.pop(get_session, None)


def test_impex_requires_identity():
    r = client.post("/api/bkn-agent/v1/export", json={"agent_ids": ["a-1"]})
    assert r.status_code == 401


def test_import_cannot_overwrite_foreign_agent():
    """越权回归：导入按 agent_id upsert，命中他人 agent 会整份覆盖其定义
    （工具/提示词/模型全改写）——与直接 PUT 是同一类越权，只是换了入口。
    预检必须拦住，且按条 failed（不中断整批）。
    """
    import asyncio

    from app.models import AgentRow

    class _Result:
        def scalar_one_or_none(self):
            return None  # 无重名冲突，确保命中的是归属分支

    class _Session:
        def __init__(self, owner):
            self._owner = owner

        async def get(self, model, agent_id):
            if self._owner is None:
                return None
            return AgentRow(f_agent_id=agent_id, f_create_user=self._owner)

        async def execute(self, stmt):
            return _Result()

    async def reason_for(owner, caller):
        return await dao.check_import_conflict(
            _Session(owner), "a-1", "some-name", None, None, caller
        )

    foreign = asyncio.run(reason_for("someone-else", "u-1"))
    assert foreign and "属于" in foreign

    own = asyncio.run(reason_for("u-1", "u-1"))
    assert own is None  # 覆盖自己的 agent 仍然允许（导入幂等）

    legacy = asyncio.run(reason_for("", "u-1"))
    assert legacy is None  # 创建者未知的存量数据放行，与写接口取舍一致

    fresh = asyncio.run(reason_for(None, "u-1"))
    assert fresh is None  # 新建（目标环境不存在该 id）不受影响
