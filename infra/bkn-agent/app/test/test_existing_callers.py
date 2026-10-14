"""Exercise existing caller protocols through HTTP, DB and real LangGraph.

The only substituted runtime boundary is the external model provider; evidence
transport is disabled. These tests never call a deployed DB/model/trace service.
"""

import asyncio
import json

import pytest
from fastapi.testclient import TestClient
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage
from langgraph.checkpoint.memory import MemorySaver
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from app import dao, db, evidence
from app.config import config
from app.core import checkpoint, graph, runner
from app.main import app
from app.models import AgentSpec, Base

RESOURCE_RESULT = {
    "confidence": 0.9,
    "resource": {"display_name": "客户", "description": "客户资料"},
    "fields": [
        {
            "name": "id",
            "display_name": "客户编号",
            "description": "客户主键",
            "confidence": 0.9,
        }
    ],
    "warnings": [],
}
RESOURCE_SCHEMA = {
    "type": "object",
    "properties": {
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "resource": {
            "type": "object",
            "properties": {
                "display_name": {"type": "string"},
                "description": {"type": "string"},
                "confidence": {"type": "number", "minimum": 0, "maximum": 1},
            },
            "required": ["display_name", "description"],
            "additionalProperties": False,
        },
        "fields": {
            "type": "array",
            "items": {
                "type": "object",
                "properties": {
                    "name": {"type": "string"},
                    "display_name": {"type": "string"},
                    "description": {"type": "string"},
                    "confidence": {"type": "number", "minimum": 0, "maximum": 1},
                },
                "required": ["name", "display_name", "description"],
                "additionalProperties": False,
            },
        },
        "warnings": {"type": "array", "items": {"type": "string"}},
    },
    "required": ["confidence", "resource", "fields", "warnings"],
    "additionalProperties": False,
}
CATALOG_RESULT = {
    "confidence": 0.8,
    "logical_views": [],
    "obsolete_logical_views": [],
    "warnings": [],
}
CATALOG_SCHEMA = {
    "type": "object",
    "properties": {
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "logical_views": {"type": "array", "items": {"type": "object"}},
        "obsolete_logical_views": {"type": "array", "items": {"type": "object"}},
        "warnings": {"type": "array", "items": {"type": "string"}},
    },
    "required": ["confidence", "logical_views", "obsolete_logical_views", "warnings"],
    "additionalProperties": False,
}
ANALYSIS_RESULT = {
    "decision": "no_change",
    "summary": "无需修改",
    "recommendations": [],
}
ANALYSIS_SCHEMA = {
    "type": "object",
    "properties": {
        "decision": {
            "type": "string",
            "enum": ["change_required", "no_change", "not_evaluable"],
        },
        "summary": {"type": "string", "minLength": 1},
        "recommendations": {"type": "array", "items": {"type": "object"}},
    },
    "required": ["decision", "summary", "recommendations"],
    "additionalProperties": False,
}
OWNER_HEADERS = {"x-account-id": "caller-owner", "x-account-type": "app"}


@pytest.fixture
def caller_client(tmp_path, monkeypatch):
    engine = create_async_engine(f"sqlite+aiosqlite:///{tmp_path / 'callers.db'}")
    sessions = async_sessionmaker(engine, expire_on_commit=False)

    async def prepare():
        async with engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)
        async with sessions() as session:
            for name, mode in (
                ("resource", "task"),
                ("catalog", "task"),
                ("analysis", "chat"),
                ("invoke", "task"),
                ("invalid", "task"),
            ):
                prompt_id = f"compat_{name}_prompt"
                await dao.upsert_prompt_with_id(
                    session,
                    prompt_id,
                    prompt_id,
                    "Answer using the caller contract.",
                    None,
                    "caller-owner",
                )
                await dao.upsert_agent_with_id(
                    session,
                    f"compat_{name}",
                    AgentSpec(
                        name=f"compat_{name}",
                        mode=mode,
                        prompt_id=prompt_id,
                        model=name,
                        tools=[],
                        status="published",
                    ),
                    "caller-owner",
                )

    async def dependency():
        async with sessions() as session:
            yield session

    async def no_evidence(*args, **kwargs):
        pass

    def model(name, **kwargs):
        output = {
            "resource": json.dumps(RESOURCE_RESULT, ensure_ascii=False),
            "catalog": json.dumps(CATALOG_RESULT, ensure_ascii=False),
            "analysis": json.dumps(ANALYSIS_RESULT, ensure_ascii=False),
            "invoke": "claim attribution answer",
            "invalid": "not a JSON object",
        }[name]
        return FakeMessagesListChatModel(responses=[AIMessage(content=output)])

    monkeypatch.setattr(db, "SessionLocal", sessions)
    monkeypatch.setattr(runner, "SessionLocal", sessions)
    monkeypatch.setattr(runner, "build_chat_model", model)
    monkeypatch.setattr(graph, "build_chat_model", model)
    monkeypatch.setattr(config, "CHECKPOINTER_BACKEND", "memory")
    monkeypatch.setattr(checkpoint, "_memory_saver", MemorySaver())
    monkeypatch.setattr(evidence, "submit_interaction_started", no_evidence)
    asyncio.run(prepare())
    app.dependency_overrides[db.get_session] = dependency
    try:
        with TestClient(app) as client:
            yield client
    finally:
        app.dependency_overrides.pop(db.get_session, None)
        asyncio.run(engine.dispose())


@pytest.mark.parametrize(
    "name,schema,expected",
    [
        ("resource", RESOURCE_SCHEMA, RESOURCE_RESULT),
        ("catalog", CATALOG_SCHEMA, CATALOG_RESULT),
    ],
)
def test_vega_run_and_poll_preserve_task_id_terminal_status_and_json_string(
    caller_client, name, schema, expected
):
    response = caller_client.post(
        "/api/bkn-agent/v1/run",
        json={
            "agent_id": f"compat_{name}",
            "message": "Explain this resource",
            "response_format": schema,
        },
        headers=OWNER_HEADERS,
    )
    assert response.status_code == 200
    task_id = response.json()["task_id"]
    for _ in range(50):
        poll = caller_client.get(
            f"/api/bkn-agent/v1/tasks/{task_id}", headers=OWNER_HEADERS
        )
        assert poll.status_code == 200
        task = poll.json()
        if task["status"] not in {"pending", "running"}:
            break
    assert task["status"] == "succeeded", task
    assert isinstance(task["output"], str)
    assert json.loads(task["output"]) == expected
    foreign = caller_client.get(
        f"/api/bkn-agent/v1/tasks/{task_id}",
        headers={"x-account-id": "foreign", "x-account-type": "app"},
    )
    assert foreign.status_code == 404


def test_ee_analysis_chat_preserves_sse_structured_content_done_and_history(
    caller_client,
):
    response = caller_client.post(
        "/api/bkn-agent/v1/chat",
        json={
            "agent_id": "compat_analysis",
            "message": "Analyze this interaction",
            "response_format": ANALYSIS_SCHEMA,
        },
        headers=OWNER_HEADERS,
    )
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/event-stream")
    events = []
    for block in response.text.strip().split("\n\n"):
        lines = block.splitlines()
        name = next(
            line.removeprefix("event: ") for line in lines if line.startswith("event: ")
        )
        data = json.loads(
            next(
                line.removeprefix("data: ")
                for line in lines
                if line.startswith("data: ")
            )
        )
        events.append((name, data))
    assert events[0][0] == "meta"
    assert events[-1][0] == "done"
    assert not any(name == "error" for name, _ in events)
    assert (
        next(data["content"] for name, data in events if name == "structured")
        == ANALYSIS_RESULT
    )
    thread = events[0][1]["thread_id"]
    history = caller_client.get(
        f"/api/bkn-agent/v1/threads/{thread}", headers=OWNER_HEADERS
    )
    assert history.status_code == 200
    assert json.loads(history.json()["messages"][-1]["content"]) == ANALYSIS_RESULT


def test_sync_invoke_preserves_persisted_terminal_response(caller_client):
    response = caller_client.post(
        "/api/bkn-agent/v1/invoke/compat_invoke",
        json={"message": "attribute a claim"},
        headers={**OWNER_HEADERS, "x-bkn-agent-thinking-mode": "disabled"},
    )
    assert response.status_code == 200
    result = response.json()
    assert result["status"] == "succeeded"
    assert result["output"] == "claim attribution answer"
    assert (
        caller_client.get(
            f"/api/bkn-agent/v1/tasks/{result['task_id']}", headers=OWNER_HEADERS
        ).json()["output"]
        == result["output"]
    )


def test_invalid_structured_result_remains_failed_instead_of_false_success(
    caller_client,
):
    response = caller_client.post(
        "/api/bkn-agent/v1/invoke/compat_invalid",
        json={
            "message": "invalid result",
            "response_format": ANALYSIS_SCHEMA,
        },
        headers=OWNER_HEADERS,
    )
    assert response.status_code == 200
    result = response.json()
    assert result["status"] == "failed"
    assert result["failure_detail"]
    assert not result.get("output")
