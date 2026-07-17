"""PR #237 第二轮审核：技能缓存按账户、原生结构化校验、toolbox 同步串行合并、import 单事务。"""
import asyncio

from langchain_core.messages import AIMessage

SCHEMA = {"type": "object", "properties": {"greeting": {"type": "string"}}, "required": ["greeting"]}


# ---------- P1-A 技能缓存键含账户 ----------

def test_skills_cache_keyed_by_account(monkeypatch):
    from app.core import skills

    skills._cache.clear()
    fetched = []

    async def fake_fetch(session, cid, headers):
        fetched.append((headers["x-account-id"], cid))
        return f"content-for-{headers['x-account-id']}"

    monkeypatch.setattr(skills, "_fetch_skill_content", fake_fetch)
    a = asyncio.run(skills.load_skills(["cap1"], "acc-A", "user"))
    b = asyncio.run(skills.load_skills(["cap1"], "acc-B", "user"))
    # 两账户各自 fetch（B 不复用 A 的缓存正文），各拿自己的内容
    assert ("acc-A", "cap1") in fetched and ("acc-B", "cap1") in fetched
    assert "content-for-acc-A" in a and "content-for-acc-B" in b
    # 同账户第二次命中缓存（不再 fetch）
    fetched.clear()
    asyncio.run(skills.load_skills(["cap1"], "acc-A", "user"))
    assert fetched == []


# ---------- P2-A 原生结构化也过 schema 校验 ----------

def test_native_invalid_falls_back(monkeypatch):
    from app.core.structured import structured_extract

    class M:
        def with_structured_output(self, schema):
            class _R:
                async def ainvoke(self, m):
                    return {"wrong": 1}  # 缺 required greeting → 不合法
            return _R()

        async def ainvoke(self, m):
            return AIMessage(content='{"greeting": "ok"}')  # 降级合法

    out = asyncio.run(structured_extract(M(), [], SCHEMA))
    assert out == {"greeting": "ok"}  # 原生不合法未被当成功，落降级拿到合法结果


def test_native_valid_returned():
    from app.core.structured import structured_extract

    class M:
        def with_structured_output(self, schema):
            class _R:
                async def ainvoke(self, m):
                    return {"greeting": "pong"}
            return _R()

    out = asyncio.run(structured_extract(M(), [], SCHEMA))
    assert out == {"greeting": "pong"}


# ---------- P1-D toolbox 同步串行 + 合并 ----------

def test_resync_serialized_and_coalesced(monkeypatch):
    from app.bootstrap import toolbox_sync

    monkeypatch.setattr(toolbox_sync.config, "TOOLBOX_SYNC_ENABLED", True)
    state = {"concurrent": 0, "max": 0, "count": 0}

    async def fake_sync_once():
        state["concurrent"] += 1
        state["max"] = max(state["max"], state["concurrent"])
        state["count"] += 1
        await asyncio.sleep(0.01)
        state["concurrent"] -= 1
        return 0

    monkeypatch.setattr(toolbox_sync, "sync_once", fake_sync_once)
    toolbox_sync._resync_queued = False

    async def drive():
        for _ in range(5):  # 突发 5 次变更
            toolbox_sync.schedule_resync()
        while toolbox_sync._background:
            await asyncio.gather(*list(toolbox_sync._background))

    asyncio.run(drive())
    assert state["max"] == 1  # 从不并发（串行，消除旧快照覆盖新的）
    assert state["count"] <= 2  # 突发合并成 ≤2 次实际同步


# ---------- P2-B import 单事务：agent 失败整体回滚 ----------

def test_import_single_transaction_rolls_back(monkeypatch):
    from fastapi.testclient import TestClient
    from sqlalchemy.exc import IntegrityError

    from app import dao
    from app.bootstrap import toolbox_sync
    from app.db import get_session
    from app.main import app

    committed = {"n": 0}
    rolled = {"n": 0}

    class _S:
        async def commit(self):
            committed["n"] += 1

        async def rollback(self):
            rolled["n"] += 1

    async def fake_session():
        yield _S()

    async def fake_conflict(session, agent_id, agent_name, prompt_id, prompt_name):
        return None  # 预检放过，让写入阶段的 IntegrityError 兜底

    async def fake_prompt(session, prompt_id, name, content, vars_schema, account_id, commit=True):
        return "created"  # flush 成功

    async def fake_agent(session, agent_id, spec, account_id, commit=True):
        raise IntegrityError("INSERT", {}, Exception("Duplicate f_name"))  # agent 写入撞唯一键

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "check_import_conflict", fake_conflict)
    monkeypatch.setattr(dao, "upsert_prompt_with_id", fake_prompt)
    monkeypatch.setattr(dao, "upsert_agent_with_id", fake_agent)
    monkeypatch.setattr(toolbox_sync, "schedule_resync", lambda: None)
    try:
        client = TestClient(app, raise_server_exceptions=False)
        pkg = {"package": {"format": "bkn-agent/v1", "exported_at": 1, "items": [{
            "agent_id": "a1",
            "spec": {"name": "dup", "mode": "chat", "status": "draft"},
            "prompt": {"prompt_id": "p1", "name": "pp", "content": "x"},
        }]}}
        r = client.post("/api/bkn-agent/v1/import", headers={
            "x-account-id": "u", "x-account-type": "user", "Content-Type": "application/json"}, json=pkg)
        assert r.status_code == 200  # 单 item 失败不炸整包
        item = r.json()["results"][0]
        assert item["action"] == "failed" and item["prompt_action"] == "none"  # prompt 未生效
        assert rolled["n"] >= 1 and committed["n"] == 0  # 回滚了，没提交半写
    finally:
        app.dependency_overrides.clear()


# ---------- max_output_tokens 透传 ----------

def test_max_output_tokens_passes_to_model():
    from app.core.llm import build_chat_model

    m = build_chat_model("", max_output_tokens=16000)
    assert m.max_tokens == 16000
    # 不设则不带（provider 默认）
    assert build_chat_model("").max_tokens is None


def test_agent_limits_max_output_tokens_bounds():
    import pytest
    from pydantic import ValidationError

    from app.models import AgentLimits

    assert AgentLimits(max_output_tokens=8192).max_output_tokens == 8192
    assert AgentLimits().max_output_tokens is None
    with pytest.raises(ValidationError):
        AgentLimits(max_output_tokens=0)


def test_max_output_tokens_floor_aligns_mf_model_api():
    """下限=10 对齐 mf-model-api conint(ge=10)：收 1..9 会建成功但执行必 400。"""
    import pytest
    from pydantic import ValidationError

    from app.models import AgentLimits

    assert AgentLimits(max_output_tokens=10).max_output_tokens == 10
    for bad in (1, 9):
        with pytest.raises(ValidationError):
            AgentLimits(max_output_tokens=bad)


def test_response_format_root_must_be_object():
    """array/标量根会过 schema 语法校验但执行链必挂（dict(r)/按 {} 抽取），边界即拒。"""
    import pytest
    from pydantic import ValidationError

    from app.models import ChatRequest

    ok = ChatRequest(agent_id="a", message="m",
                     response_format={"type": "object", "properties": {}})
    assert ok.response_format["type"] == "object"
    for bad in ({"type": "array", "items": {"type": "object"}},
                {"type": "string"},
                {"properties": {}}):  # 缺 type 也拒，报错指明包 object
        with pytest.raises(ValidationError):
            ChatRequest(agent_id="a", message="m", response_format=bad)


def test_tool_refs_validated_at_creation():
    """ToolRef 建时校验：未知 type / 缺必备引用字段=建成功执行必败，边界即拒。"""
    import pytest
    from pydantic import ValidationError

    from app.models import AgentSpec

    ok = AgentSpec(name="t", mode="chat", tools=[
        {"type": "toolbox", "box_id": "b1"},
        {"type": "mcp", "url": "http://x/mcp"},
        {"type": "agent", "agent_id": "a2"},
    ])
    assert len(ok.tools) == 3
    for bad in ([{"type": "xxx"}],
                [{"type": "toolbox"}],          # 缺 box_id
                [{"type": "mcp"}],              # 缺 url
                [{"type": "agent", "agent_id": ""}]):  # 空引用
        with pytest.raises(ValidationError):
            AgentSpec(name="t", mode="chat", tools=bad)


def test_tool_refs_type_checked():
    """P2 四轮：ToolRef 值类型也校验（url=123 等此前落库、执行才炸）。"""
    import pytest
    from pydantic import ValidationError

    from app.models import AgentSpec

    for bad in ([{"type": "mcp", "url": 123}],
                [{"type": "toolbox", "box_id": {"x": 1}}],
                [{"type": "agent", "agent_id": 123}],
                [{"type": "mcp", "url": "not-a-url"}],       # 非 http(s)
                [{"type": "agent", "agent_id": "a", "name": 42}]):  # name 类型
        with pytest.raises(ValidationError):
            AgentSpec(name="t", mode="chat", tools=bad)
    # 合法值校验后转回 dict（执行链/入库兼容），附加字段保留
    ok = AgentSpec(name="t", mode="chat", tools=[
        {"type": "mcp", "url": "http://x/mcp", "name": "m1", "transport": "sse"}])
    assert isinstance(ok.tools[0], dict)
    assert ok.tools[0]["transport"] == "sse" and ok.tools[0]["name"] == "m1"


def test_agent_out_skips_tools_validation():
    """P2 四轮：出库（AgentOut）不复验——存量脏 ToolRef/脏 name 不阻断列表与同步。"""
    from app.models import AgentOut

    dirty = AgentOut(
        agent_id="legacy-uuid", name="t", mode="chat", model="",
        tools=[{"type": "xxx"}, {"type": "mcp", "url": 123}, 42],  # 脏存量（含标量）
        skills=[], status="published",
        create_user="u", update_user="u", create_time=0, update_time=0,
    )
    assert dirty.tools[0]["type"] == "xxx" and dirty.tools[2] == 42  # 原样放行，可读取可修复
    dirty.model_dump()  # 序列化不炸（列表/同步路径）


def test_export_rejects_dirty_agent_with_clear_error(monkeypatch):
    """P2 四轮：export 回填写入模型遇脏数据报单条明确 400，不落 500。"""
    from fastapi.testclient import TestClient

    from app import dao
    from app.db import get_session
    from app.main import app
    from app.models import AgentOut

    async def fake_session():
        yield object()

    async def fake_get_agent(session, agent_id):
        return AgentOut(agent_id=agent_id, name="t", mode="chat", model="",
                        tools=[{"type": "xxx"}], skills=[], status="draft",
                        create_user="u", update_user="u", create_time=0, update_time=0)

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "get_agent", fake_get_agent)
    try:
        client = TestClient(app, raise_server_exceptions=False)
        r = client.post("/api/bkn-agent/v1/export",
                        headers={"x-account-id": "u", "x-account-type": "user"},
                        json={"agent_ids": ["a1"]})
        assert r.status_code == 400
        assert "DirtyAgent" in r.json()["code"]
    finally:
        app.dependency_overrides.clear()
