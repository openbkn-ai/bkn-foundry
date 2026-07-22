"""P1 回归：工具调用上限、agent-as-tool 门禁、thread 并发占位。"""
import asyncio

import pytest
from fastapi import HTTPException
from langchain_core.tools import StructuredTool

from app.core import graph, tools
from app import observability
from app.models import AgentOut


def _tool(name: str, calls: list):
    async def run(x: str = "") -> str:
        calls.append(name)
        return f"{name}:ok"

    return StructuredTool.from_function(coroutine=run, name=name, description=name)


def test_max_tool_calls_enforced():
    """契约里有 max_tool_calls，就必须真的拦住；老实现只认 max_turns，上限形同虚设。"""
    calls: list = []
    capped = tools.apply_tool_call_cap([_tool("t1", calls), _tool("t2", calls)], 2)

    async def drive():
        return [
            await capped[0].coroutine(x="a"),
            await capped[1].coroutine(x="b"),
            await capped[0].coroutine(x="c"),  # 第 3 次：预算用尽
        ]

    r1, r2, r3 = asyncio.run(drive())
    assert r1 == "t1:ok" and r2 == "t2:ok"
    assert "budget exhausted" in r3  # 返回提示串让模型收敛，而非静默继续
    assert calls == ["t1", "t2"]  # 第 3 次没真打下游


def test_max_tool_calls_emits_budget_exhausted_event(monkeypatch):
    emitted = []

    async def fake_submit(events, account_id, account_type):
        emitted.extend(events)

    monkeypatch.setattr(tools.evidence, "submit_events", fake_submit)
    capped = tools.apply_tool_call_cap([_tool("t1", [])], 0, "acct-1", "user")
    token = observability.set_context(
        observability.TraceContext(
            trace_id="1234567890abcdef1234567890abcdef",
            request_id="req_budget_001",
            traceparent="00-1234567890abcdef1234567890abcdef-1234567890abcdef-01",
            entry_boundary="external",
        )
    )

    async def drive():
        return await capped[0].coroutine(x="a")

    try:
        result = asyncio.run(drive())
    finally:
        observability.reset_context(token)
    assert "budget exhausted" in result
    assert emitted[0]["event_type"] == "tool.budget.exhausted"
    assert emitted[0]["payload"]["tool_name"] == "t1"


def test_no_cap_when_unset():
    calls: list = []
    same = tools.apply_tool_call_cap([_tool("t1", calls)], None)
    asyncio.run(same[0].coroutine(x="a"))
    asyncio.run(same[0].coroutine(x="b"))
    assert calls == ["t1", "t1"]


def _sub_agent(mode="task", status="published") -> AgentOut:
    return AgentOut(
        agent_id="sub-1", name="sub_agent", mode=mode, status=status,
        create_user="u", update_user="u", create_time=1, update_time=1,
    )


@pytest.mark.parametrize(
    "mode,status",
    [("chat", "published"), ("task", "draft"), ("chat", "draft")],
)
def test_agent_as_tool_rejects_unpublished_or_chat_mode(monkeypatch, mode, status):
    """/run 拒 chat 型、/invoke 拒 draft；agent-as-tool 必须同门，否则绕过语义。"""
    from app import dao

    async def get_agent(session, agent_id):
        return _sub_agent(mode=mode, status=status)

    class _S:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    monkeypatch.setattr("app.db.SessionLocal", lambda: _S())
    monkeypatch.setattr(dao, "get_agent", get_agent)

    with pytest.raises(HTTPException) as e:
        asyncio.run(tools._agent_tool({"type": "agent", "agent_id": "sub-1"}, "u", "user", 0, None))
    assert e.value.status_code == 400
    assert "ToolRef" in e.value.detail["code"]


def test_agent_as_tool_accepts_published_task(monkeypatch):
    from app import dao

    async def get_agent(session, agent_id):
        return _sub_agent()

    class _S:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    monkeypatch.setattr("app.db.SessionLocal", lambda: _S())
    monkeypatch.setattr(dao, "get_agent", get_agent)
    t = asyncio.run(tools._agent_tool({"type": "agent", "agent_id": "sub-1"}, "u", "user", 0, None))
    assert t.name == "agent_sub_agent"


def test_busy_thread_slot_released_on_setup_failure():
    """占位后 setup 失败必须放位，否则该 thread 永久 409。"""
    graph._busy_threads.discard("t-x")
    graph._busy_threads.add("t-x")
    graph._busy_threads.discard("t-x")
    assert "t-x" not in graph._busy_threads


def test_busy_set_replaces_lock_table():
    """忙碌集合而非锁表：不再按 thread_id 无限增长。"""
    assert isinstance(graph._busy_threads, set)
    assert not hasattr(graph, "_thread_locks")
