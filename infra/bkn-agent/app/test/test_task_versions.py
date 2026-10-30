# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Pinned one-shot runs and task provenance, using the existing JSON column."""

import asyncio
from unittest.mock import AsyncMock

import pytest
from langchain_core.messages import AIMessage, AIMessageChunk
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult
from langchain_openai import ChatOpenAI

from app import dao
from app.core import llm, runner, tools
from app.models import AgentRow, AgentVersionRow, PromptRow, PromptVersionRow
from app.test.test_public_execution import environment

AGENT = "studio_smart_qa"
HEADERS = {"x-account-id": "svc-test", "x-account-type": "app"}


async def seed_versions(factory):
    async with factory() as session:
        agent = await session.get(AgentRow, AGENT)
        agent.f_mode = "task"
        agent.f_model = "new-model"
        agent.f_limits = {"max_output_tokens": 200}
        prompt = await session.get(PromptRow, "qa-prompt")
        prompt.f_current_version = 2
        session.add(PromptVersionRow(
            f_prompt_id="qa-prompt", f_version=2, f_content="New instructions",
            f_create_user="admin", f_create_time=2,
        ))
        for version, model, current, prompt_version, limit in (
            (1, "accepted-model", False, 1, 100),
            (2, "new-model", True, 2, 200),
        ):
            session.add(AgentVersionRow(
                f_agent_id=AGENT, f_version=version, f_model=model,
                f_tools=[{"type": "toolbox", "box_id": "accepted-tools"}] if version == 1 else [],
                f_limits={"max_output_tokens": limit}, f_prompt_version=prompt_version,
                f_current=current, f_create_user="admin", f_create_time=version,
            ))
        await session.commit()


def wire_runner(monkeypatch, factory, calls, *, stream_content='{"answer": "ok"}'):
    monkeypatch.setattr(runner, "SessionLocal", factory)
    monkeypatch.setattr(runner, "load_skills", AsyncMock(return_value=""))
    monkeypatch.setattr(runner.evidence, "submit_interaction_started", AsyncMock(return_value=False))

    async def load_tools(refs, *args, **kwargs):
        calls.append(("tools", refs))
        return []

    monkeypatch.setattr(tools, "load_tools", load_tools)

    async def generate(self, *args, **kwargs):
        # ChatOpenAI keeps response.model in llm_output before ainvoke merges it.
        return ChatResult(
            generations=[ChatGeneration(message=AIMessage(content='{"answer": "ok"}'))],
            llm_output={"model_name": "extraction-provider"},
        )

    monkeypatch.setattr(ChatOpenAI, "_agenerate", generate)

    async def stream(self, *args, **kwargs):
        yield ChatGenerationChunk(
            message=AIMessageChunk(content=stream_content),
            generation_info={"model_name": "provider-model-202610"},
        )

    monkeypatch.setattr(ChatOpenAI, "_astream", stream)

    def create_agent(model, refs, *, system_prompt):
        calls.append(("config", model.model_name, model.max_tokens, system_prompt))

        class Graph:
            async def ainvoke(self, inp, cfg):
                message = await model.ainvoke(inp["messages"])
                return {"messages": [message]}

        return Graph()

    monkeypatch.setattr(runner, "create_agent", create_agent)


@pytest.mark.parametrize("endpoint", ["run", "invoke"])
@pytest.mark.parametrize("version", [1, None])
@pytest.mark.parametrize("structured", [False, True])
def test_task_uses_selected_version_and_persists_actual_usage(monkeypatch, endpoint, version, structured):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await seed_versions(factory)
            calls = []
            wire_runner(monkeypatch, factory, calls)
            # Still exercise execute_task, while making the async endpoint deterministic.
            if endpoint == "run":
                monkeypatch.setattr(runner, "submit_task", lambda *args: calls.append(("submitted", args)))
            body = {"message": "Extract"}
            if version is not None:
                body["agent_version"] = version
            if structured:
                body["response_format"] = {"type": "object", "required": ["answer"]}
            if endpoint == "run":
                body["agent_id"] = AGENT
            url = "/api/bkn-agent/v1/run" if endpoint == "run" else f"/api/bkn-agent/v1/invoke/{AGENT}"
            response = await client.post(url, json=body, headers=HEADERS)
            assert response.status_code == 200, response.text
            if endpoint == "run":
                assert response.json()["status"] == "pending"
                args = next(c[1] for c in calls if c[0] == "submitted")
                await runner.execute_task(*args)
            result = await client.get(f"/api/bkn-agent/v1/tasks/{response.json()['task_id']}", headers=HEADERS)
            task = result.json()
            assert task["status"] == "succeeded", task
            expected = version or 2
            assert task["agent_version"] == expected
            assert task["prompt_version"] == expected
            assert task["model"] == ("accepted-model" if version else "new-model")
            assert task["model_id"] == "provider-model-202610"
            config = next(c for c in calls if c[0] == "config")
            assert config[1:] == (
                task["model"], 100 if version else 200,
                "Shared instructions" if version else "New instructions",
            )
            refs = next(c[1] for c in calls if c[0] == "tools")
            assert refs == ([{"type": "toolbox", "box_id": "accepted-tools"}] if version else [])
            async with factory() as session:
                assert (await dao.get_agent(session, AGENT)).model == "new-model"
                assert (await dao.current_version(session, AGENT)).f_version == 2
                assert (await session.get(PromptRow, "qa-prompt")).f_current_version == 2
    asyncio.run(run())


@pytest.mark.parametrize("endpoint", ["run", "invoke"])
@pytest.mark.parametrize("deleted", [False, True])
def test_missing_or_deleted_version_is_rejected_before_task_creation(monkeypatch, endpoint, deleted):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await seed_versions(factory)
            if deleted:
                async with factory() as session:
                    row = await session.get(AgentVersionRow, (AGENT, 1))
                    row.f_deleted = True
                    await session.commit()
            body = {"message": "Extract", "agent_version": 1 if deleted else 99}
            if endpoint == "run":
                body["agent_id"] = AGENT
            create = AsyncMock()
            monkeypatch.setattr(dao, "create_task", create)
            url = "/api/bkn-agent/v1/run" if endpoint == "run" else f"/api/bkn-agent/v1/invoke/{AGENT}"
            response = await client.post(url, json=body, headers=HEADERS)
            assert response.status_code == 404, response.text
            create.assert_not_awaited()
    asyncio.run(run())


def test_historical_task_fields_remain_unknown(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (_, factory, _):
            async with factory() as session:
                task = await dao.create_task(session, AGENT, {"message": "old"}, "svc-test")
                assert (task.agent_version, task.prompt_version, task.model, task.model_id) == (None,) * 4
    asyncio.run(run())


def test_streaming_model_response_is_recorded_without_evidence(monkeypatch):
    async def stream(self, *args, **kwargs):
        yield ChatGenerationChunk(message=AIMessageChunk(content="ok"))
        yield ChatGenerationChunk(message=AIMessageChunk(content=""), generation_info={"model_name": "stream-provider"})

    monkeypatch.setattr(ChatOpenAI, "_astream", stream)
    monkeypatch.setattr(llm.evidence, "new_operation", lambda: (None, None))

    async def run():
        usage = {"model": "registered"}
        token = llm.set_task_usage(usage)
        try:
            model = llm.build_chat_model("registered")
            async for _ in model._astream([]):
                pass
            assert usage["model_id"] == "stream-provider"
        finally:
            llm.reset_task_usage(token)
    asyncio.run(run())


def test_structured_extraction_records_its_own_model_response(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await seed_versions(factory)
            wire_runner(monkeypatch, factory, [], stream_content="Needs extraction")
            response = await client.post(
                f"/api/bkn-agent/v1/invoke/{AGENT}", headers=HEADERS,
                json={"message": "Extract", "agent_version": 1, "response_format": {
                    "type": "object", "properties": {"answer": {"type": "string"}},
                    "required": ["answer"],
                }},
            )
            task = response.json()
            assert task["status"] == "succeeded", task
            assert task["model"] == "accepted-model"
            assert task["model_id"] == "extraction-provider"
    asyncio.run(run())


def test_default_model_and_prompt_override_record_effective_values(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await seed_versions(factory)
            async with factory() as session:
                agent = await session.get(AgentRow, AGENT)
                agent.f_model = ""
                await session.commit()
            monkeypatch.setattr(llm.config, "DEFAULT_MODEL", "service-default")
            calls = []
            wire_runner(monkeypatch, factory, calls)
            response = await client.post(
                f"/api/bkn-agent/v1/invoke/{AGENT}", headers=HEADERS,
                json={"message": "Extract", "prompt_override": "Request instructions"},
            )
            task = response.json()
            assert task["status"] == "succeeded", task
            assert task["model"] == "service-default"
            assert task["prompt_version"] is None
            assert next(c for c in calls if c[0] == "config")[3] == "Request instructions"
    asyncio.run(run())


def test_missing_historical_prompt_does_not_fall_back_to_current(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await seed_versions(factory)
            async with factory() as session:
                await session.delete(await session.get(PromptVersionRow, ("qa-prompt", 1)))
                await session.commit()
            calls = []
            wire_runner(monkeypatch, factory, calls)
            response = await client.post(
                f"/api/bkn-agent/v1/invoke/{AGENT}", headers=HEADERS,
                json={"message": "Extract", "agent_version": 1},
            )
            assert response.json()["status"] == "failed"
            assert response.json()["failure_detail"]
            assert not calls
    asyncio.run(run())


@pytest.mark.parametrize("version", [0, -1, True, "1", 1.5])
def test_agent_version_requires_a_positive_integer(version):
    from pydantic import ValidationError
    from app.models import InvokeRequest, RunRequest

    for request in (InvokeRequest, RunRequest):
        with pytest.raises(ValidationError):
            request(message="Extract", agent_id=AGENT, agent_version=version)
