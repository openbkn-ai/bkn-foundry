# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Public executions use real SQL persistence; model/identity servers are isolated."""

import asyncio
import json
import tempfile
from contextlib import asynccontextmanager

import httpx
import pytest
from fastapi import HTTPException
from langchain_core.messages import AIMessage, ToolMessage
from langchain_core.tools import StructuredTool
from sqlalchemy import select
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine
from starlette.requests import Request

from app import dao, public_auth
from app.auth import Account
from app.core.context_loader import _MCP_RECEIPT_TRUST_TOKEN
from app.core.public_chat import PublicChat, project
from app.db import get_session
from app.main import app
from app.models import (
    AgentRow,
    Base,
    ExecutionRequest,
    PromptOverrideRow,
    PromptRow,
    PromptVersionRow,
    TaskRow,
)
from app.routers import executions

URL = "/api/bkn-agent/v1/executions"
BODY = {
    "agent_id": "studio_smart_qa",
    "message": "Question",
    "client_request_id": "request-1",
    "context": {"kn_id": "network-1"},
}
AUTH = {"Authorization": "Bearer token-1", "Accept-Language": "en-US"}


def parse_sse(text):
    return [
        (block.splitlines()[0][7:], json.loads(block.splitlines()[1][6:]))
        for block in text.strip().split("\n\n")
    ]


@asynccontextmanager
async def environment(monkeypatch, *, stream=True):
    directory = tempfile.TemporaryDirectory(prefix="bkn-execution-test-")
    engine = create_async_engine(f"sqlite+aiosqlite:///{directory.name}/test.db")
    factory = async_sessionmaker(engine, expire_on_commit=False)
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)
    async with factory() as session:
        session.add_all(
            [
                AgentRow(
                    f_agent_id="studio_smart_qa",
                    f_name="QA",
                    f_mode="chat",
                    f_prompt_id="qa-prompt",
                    f_tools=[],
                    f_skills=[],
                    f_status="published",
                    f_create_user="admin",
                    f_update_user="admin",
                    f_create_time=1,
                    f_update_time=1,
                ),
                PromptRow(
                    f_prompt_id="qa-prompt",
                    f_name="Shared",
                    f_current_version=1,
                    f_update_user="admin",
                    f_update_time=1,
                ),
                PromptVersionRow(
                    f_prompt_id="qa-prompt",
                    f_version=1,
                    f_content="Shared instructions",
                    f_create_user="admin",
                    f_create_time=1,
                ),
            ]
        )
        await session.commit()
    calls = []

    async def post(url, **kwargs):
        if url.endswith("/oauth2/introspect"):
            token = kwargs["data"]["token"]
            if token == "invalid":
                return {"active": False}
            return {
                "active": True,
                "sub": "other" if token == "other" else "user-1",
                "ext": {"visitor_type": "realname"},
            }
        checks = kwargs["json"]["checks"]
        return {
            "allowed": True,
            "results": [
                {
                    "resource_type": c["resource"]["type"],
                    "resource_id": c["resource"]["id"],
                    "operation": c["operation"],
                    "allowed": True,
                }
                for c in checks
            ],
        }

    async def session_dependency():
        async with factory() as session:
            yield session

    async def chat(session, agent, req, account_id, account_type, *, public):
        calls.append(req)
        await dao.touch_thread(session, req.thread_id, agent.agent_id, account_id)
        public.answer.append("Answer")
        public.prompt_version = 1

        async def events():
            yield public.event("token", {"content": "Answer"})
            yield "event: done\ndata: {}\n\n"

        return events()

    monkeypatch.setattr(executions, "SessionLocal", factory)
    monkeypatch.setattr(public_auth, "_post", post)
    if stream:
        monkeypatch.setattr(executions, "stream_chat", chat)
    app.dependency_overrides[get_session] = session_dependency
    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="http://test"
        ) as client:
            yield client, factory, calls
    finally:
        app.dependency_overrides.pop(get_session, None)
        executions._active.clear()
        executions._thread_claims.clear()
        await engine.dispose()
        directory.cleanup()


def test_run_persisted_before_success_and_retry_does_not_run_twice(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            response = await client.post(
                URL,
                json=BODY,
                headers={**AUTH, "x-account-id": "spoofed", "x-account-type": "app"},
            )
            assert response.status_code == 200, response.text
            events = parse_sse(response.text)
            assert [e for e, _ in events] == [
                "meta",
                "token",
                "usage",
                "result",
                "terminal",
            ]
            assert [d["sequence"] for _, d in events] == [1, 2, 3, 4, 5]
            run_id = events[0][1]["run_id"]
            thread_id = events[0][1]["thread_id"]
            result = (await client.get(f"{URL}/{run_id}", headers=AUTH)).json()
            assert result["status"] == "succeeded"
            assert result["result"]["answer"] == "Answer"
            async with factory() as session:
                row = await session.get(TaskRow, run_id)
                assert row.f_account_id == "user-1"
                assert row.f_input["subject"]["account_type"] == "user"
                assert "token-1" not in json.dumps(row.f_input)
            repeat = await client.post(URL, json=BODY, headers=AUTH)
            assert parse_sse(repeat.text)[-1][1]["status"] == "succeeded"
            assert len(calls) == 1
            changed = await client.post(
                URL, json={**BODY, "message": "Different"}, headers=AUTH
            )
            assert changed.status_code == 409
            # A subsequent turn is bound to the recorded network and identity.
            next_turn = {
                **BODY,
                "client_request_id": "request-2",
                "thread_id": thread_id,
            }
            assert (
                await client.post(URL, json=next_turn, headers=AUTH)
            ).status_code == 200
            assert (
                await client.post(
                    URL,
                    json={
                        **next_turn,
                        "client_request_id": "request-3",
                        "context": {"kn_id": "network-2"},
                    },
                    headers=AUTH,
                )
            ).status_code == 409
            assert (
                await client.post(
                    URL,
                    json={**next_turn, "client_request_id": "request-4"},
                    headers={**AUTH, "Authorization": "Bearer other"},
                )
            ).status_code == 404
            for suffix in ("", "/cancel"):
                method = client.post if suffix else client.get
                assert (
                    await method(
                        f"{URL}/{run_id}{suffix}",
                        headers={**AUTH, "Authorization": "Bearer other"},
                    )
                ).status_code == 404
            assert (await client.post(f"{URL}/{run_id}/cancel", headers=AUTH)).json()[
                "status"
            ] == "succeeded"

    asyncio.run(run())


@pytest.mark.parametrize(
    "headers",
    [
        {},
        {"x-account-id": "user-1", "x-account-type": "user"},
        {"Authorization": "Bearer invalid"},
    ],
)
def test_headers_and_invalid_token_cannot_start_run(monkeypatch, headers):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            assert (
                await client.post(URL, json=BODY, headers=headers)
            ).status_code == 401
            async with factory() as session:
                assert not (await session.execute(select(TaskRow))).scalars().all()
            assert not calls

    asyncio.run(run())


@pytest.mark.parametrize(
    "extra",
    [
        {"prompt_override": "replace"},
        {"skills": ["x"]},
        {"model": "x"},
        {"response_format": {"type": "object"}},
        {"context": {"kn_id": "network-1", "bkn_context": {}}},
    ],
)
def test_public_request_rejects_config_and_lifecycle_overrides(monkeypatch, extra):
    async def run():
        async with environment(monkeypatch) as (client, _, calls):
            assert (
                await client.post(URL, json={**BODY, **extra}, headers=AUTH)
            ).status_code == 400
            assert not calls

    asyncio.run(run())


@pytest.mark.parametrize("change", ["draft", "task", "unknown", "unscoped"])
def test_unsupported_agent_never_starts(monkeypatch, change):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            async with factory() as session:
                agent = await session.get(AgentRow, "studio_smart_qa")
                if change == "draft":
                    agent.f_status = "draft"
                if change == "task":
                    agent.f_mode = "task"
                if change == "unscoped":
                    agent.f_tools = [{"type": "agent", "agent_id": "nested"}]
                await session.commit()
            body = (
                {**BODY, "agent_id": "graph_explorer"} if change == "unknown" else BODY
            )
            assert (await client.post(URL, json=body, headers=AUTH)).status_code in {
                404,
                409,
            }
            assert not calls

    asyncio.run(run())


def test_failed_setup_is_persisted_and_releases_thread(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):

            async def fail(*args, **kwargs):
                raise RuntimeError("secret upstream failure")

            monkeypatch.setattr(executions, "stream_chat", fail)
            response = await client.post(URL, json=BODY, headers=AUTH)
            events = parse_sse(response.text)
            assert [event for event, _ in events] == ["meta", "terminal"]
            assert events[-1][1]["status"] == "failed"
            run_id = events[0][1]["run_id"]
            assert "secret upstream failure" not in response.text
            assert (await client.get(f"{URL}/{run_id}", headers=AUTH)).json()[
                "status"
            ] == "failed"
            assert not executions._thread_claims

    asyncio.run(run())


def test_authz_rechecks_on_read_cancel_and_retry(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, calls):
            response = await client.post(URL, json=BODY, headers=AUTH)
            run_id = parse_sse(response.text)[0][1]["run_id"]
            old = public_auth._post

            async def denied(url, **kwargs):
                result = await old(url, **kwargs)
                if url.endswith("/checks"):
                    result["allowed"] = False
                    result["results"][0]["allowed"] = False
                return result

            monkeypatch.setattr(public_auth, "_post", denied)
            assert (
                await client.get(f"{URL}/{run_id}", headers=AUTH)
            ).status_code == 403
            assert (
                await client.post(f"{URL}/{run_id}/cancel", headers=AUTH)
            ).status_code == 403
            assert (await client.post(URL, json=BODY, headers=AUTH)).status_code == 403
            assert len(calls) == 1

    asyncio.run(run())


@pytest.mark.parametrize(
    "kind,info,expected",
    [
        ("bak_test", {"active": True, "sub": "u", "account_type": "user"}, "user"),
        ("bak_test", {"active": True, "sub": "a", "account_type": "app"}, "app"),
        ("oauth", {"active": True, "sub": "a", "client_id": "a"}, "app"),
        (
            "oauth",
            {"active": True, "sub": "u", "ext": {"visitor_type": "realname"}},
            "user",
        ),
        (
            "oauth",
            {"active": True, "sub": "u", "ext": {"visitor_type": "anonymous"}},
            None,
        ),
        (
            "oauth",
            {"active": True, "sub": " u", "ext": {"visitor_type": "realname"}},
            None,
        ),
    ],
)
def test_credential_identity_contract(monkeypatch, kind, info, expected):
    async def run():
        async def post(url, **kwargs):
            return info

        monkeypatch.setattr(public_auth, "_post", post)
        request = Request(
            {
                "type": "http",
                "headers": [
                    (b"authorization", f"Bearer {kind}".encode()),
                    (b"x-account-id", b"spoof"),
                ],
            }
        )
        if expected:
            account = await public_auth.get_public_account(request)
            assert account.account_type == expected
            assert (
                dict(request.scope["headers"])[b"x-account-id"].decode() == info["sub"]
            )
        else:
            with pytest.raises(HTTPException) as error:
                await public_auth.get_public_account(request)
            assert error.value.status_code == 401

    asyncio.run(run())


def test_tool_scope_blocks_foreign_network_and_unscoped_code(monkeypatch):
    async def run():
        calls = []

        async def query(**kwargs):
            calls.append(kwargs)
            return "ok"

        schema = {
            "type": "object",
            "properties": {"kn_id": {"type": "string"}},
            "required": ["kn_id"],
        }
        scoped = StructuredTool.from_function(
            coroutine=query,
            name="query",
            description="Query",
            args_schema=schema,
            metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
        )
        code = StructuredTool.from_function(
            coroutine=query,
            name="run_code",
            description="Code",
            args_schema={"type": "object", "properties": {"code": {"type": "string"}}},
        )
        fake = scoped.model_copy(update={"metadata": {}})
        public = PublicChat("run", "network-1")
        guarded = public.guard_tools([scoped, code, fake])
        assert await guarded[0].ainvoke({"kn_id": "network-1"}) == "ok"
        assert "different" in await guarded[0].ainvoke({"kn_id": "network-2"})
        assert "unavailable" in await guarded[1].ainvoke(
            {"code": "query other network"}
        )
        assert "unavailable" in await guarded[2].ainvoke({"kn_id": "network-1"})
        assert calls == [{"kn_id": "network-1"}]

    asyncio.run(run())


def test_tool_events_use_real_ids_errors_usage_and_redaction():
    public = PublicChat("run", "network-1")
    messages = [
        AIMessage(
            content="",
            tool_calls=[
                {
                    "name": "query",
                    "args": {"token": "private", "query": "q"},
                    "id": "call-1",
                }
            ],
            usage_metadata={"input_tokens": 5, "output_tokens": 2, "total_tokens": 7},
        ),
        ToolMessage(
            content='{"password":"secret","value":7}',
            tool_call_id="call-1",
            name="query",
            status="error",
        ),
    ]
    events = parse_sse("".join(public.updates({"model": {"messages": messages}})))
    assert events[0][1]["tool_call_id"] == events[1][1]["tool_call_id"] == "call-1"
    assert events[0][1]["input"]["token"] == "[redacted]"
    assert events[1][1]["status"] == "failed"
    assert events[1][1]["output"]["password"] == "[redacted]"
    assert public.usage["total_tokens"] == 7
    assert "bak_private" not in json.dumps(project("Bearer abc bak_private"))
    assert "truncated" in project("long " * 2000)


def test_cancel_running_execution_commits_cancelled_once(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (_, factory, calls):
            started = asyncio.Event()

            async def blocking(
                session, agent, req, account_id, account_type, *, public
            ):
                await dao.touch_thread(
                    session, req.thread_id, agent.agent_id, account_id
                )

                async def events():
                    started.set()
                    await asyncio.Event().wait()
                    yield "event: done\ndata: {}\n\n"

                return events()

            monkeypatch.setattr(executions, "stream_chat", blocking)
            async with factory() as session:
                response = await executions.execute(
                    ExecutionRequest(**BODY), Account("user-1", "user"), session
                )
            seen = []

            async def consume():
                async for event in response.body_iterator:
                    seen.append(event)

            consumer = asyncio.create_task(consume())
            await asyncio.wait_for(started.wait(), 2)
            run_id = parse_sse(seen[0])[0][1]["run_id"]
            thread_id = parse_sse(seen[0])[0][1]["thread_id"]
            # Concurrent turn and duplicate request cannot start another model.
            for body in (
                BODY,
                {**BODY, "client_request_id": "next", "thread_id": thread_id},
            ):
                async with factory() as session:
                    with pytest.raises(HTTPException) as error:
                        await executions.execute(
                            ExecutionRequest(**body), Account("user-1", "user"), session
                        )
                    assert error.value.status_code == 409
            async with factory() as session:
                await executions.cancel_execution(
                    run_id, Account("user-1", "user"), session
                )
            await asyncio.wait_for(consumer, 2)
            assert parse_sse("".join(seen))[-1][1]["status"] == "cancelled"
            async with factory() as session:
                state = await executions.get_execution(
                    run_id, Account("user-1", "user"), session
                )
                assert state.status == "cancelled"
                assert state.result is None
                await dao.finish_execution(
                    session, run_id, "succeeded", output='{"answer":"late"}'
                )
                session.expire_all()
                assert (
                    await executions.get_execution(
                        run_id, Account("user-1", "user"), session
                    )
                ).status == "cancelled"
            assert not executions._thread_claims

    asyncio.run(run())


def test_disconnect_before_setup_persists_cancel_and_releases_claim(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (_, factory, calls):
            async with factory() as session:
                response = await executions.execute(
                    ExecutionRequest(**BODY), Account("user-1", "user"), session
                )
            first = await anext(response.body_iterator)
            run_id = parse_sse(first)[0][1]["run_id"]
            await response.body_iterator.aclose()
            async with factory() as session:
                assert (
                    await executions.get_execution(
                        run_id, Account("user-1", "user"), session
                    )
                ).status == "cancelled"
            assert not calls
            assert not executions._active
            assert not executions._thread_claims

    asyncio.run(run())


def test_pending_cancel_cannot_be_overwritten_by_start(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (_, factory, calls):
            account = Account("user-1", "user")
            async with factory() as session:
                response = await executions.execute(
                    ExecutionRequest(**BODY), account, session
                )
                tasks = (await session.execute(select(TaskRow))).scalars().all()
                run_id = tasks[0].f_task_id
            async with factory() as session:
                assert (
                    await executions.cancel_execution(run_id, account, session)
                ).status == "cancelled"
            events = parse_sse(
                "".join([event async for event in response.body_iterator])
            )
            assert [event for event, _ in events] == ["meta", "terminal"]
            assert events[-1][1]["status"] == "cancelled"
            assert not calls
            assert not executions._thread_claims

    asyncio.run(run())


def test_same_id_in_different_subject_type_is_not_owner(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            response = await client.post(URL, json=BODY, headers=AUTH)
            run_id = parse_sse(response.text)[0][1]["run_id"]
            old = public_auth._post

            async def app_identity(url, **kwargs):
                if url.endswith("/oauth2/introspect"):
                    return {
                        "active": True,
                        "sub": "user-1",
                        "ext": {"visitor_type": "business"},
                    }
                return await old(url, **kwargs)

            monkeypatch.setattr(public_auth, "_post", app_identity)
            assert (
                await client.get(f"{URL}/{run_id}", headers=AUTH)
            ).status_code == 404

    asyncio.run(run())


def test_internal_thread_cannot_be_adopted_without_public_scope(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            async with factory() as session:
                await dao.touch_thread(session, "legacy", "studio_smart_qa", "user-1")
            assert (
                await client.post(
                    URL, json={**BODY, "thread_id": "legacy"}, headers=AUTH
                )
            ).status_code == 404
            assert not calls

    asyncio.run(run())


@pytest.mark.parametrize(
    "result,status",
    [
        ({"allowed": True, "results": []}, 503),
        ({"allowed": True, "results": [{"allowed": True}, {"allowed": True}]}, 503),
    ],
)
def test_incomplete_authorization_response_is_not_permission(
    monkeypatch, result, status
):
    async def run():
        async def post(*args, **kwargs):
            return result

        monkeypatch.setattr(public_auth, "_post", post)
        with pytest.raises(HTTPException) as error:
            await public_auth.authorize_execution(Account("u", "user"), "a", "n")
        assert error.value.status_code == status

    asyncio.run(run())


def test_shared_prompt_does_not_read_existing_personal_override(monkeypatch):
    from app.core.prompt import resolve_prompt

    async def run():
        async with environment(monkeypatch) as (_, factory, _):
            async with factory() as session:
                session.add(
                    PromptOverrideRow(
                        f_agent_id="studio_smart_qa",
                        f_account_id="user-1",
                        f_content="Personal",
                        f_update_time=1,
                    )
                )
                await session.commit()
                agent = await dao.get_agent(session, "studio_smart_qa")
                shared = await resolve_prompt(
                    session, agent, "user-1", "Request", {}, shared_only=True
                )
                legacy = await resolve_prompt(session, agent, "user-1", None, {})
                assert shared == ("Shared instructions", "default", 1)
                assert legacy[0] == "Personal"

    asyncio.run(run())


def test_real_graph_updates_use_guarded_tool_and_final_answer(monkeypatch):
    from contextlib import asynccontextmanager

    from langchain_core.language_models.fake_chat_models import (
        FakeMessagesListChatModel,
    )
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph

    class Model(FakeMessagesListChatModel):
        def bind_tools(self, tools, **kwargs):
            return self

    async def run():
        async with environment(monkeypatch, stream=False) as (client, _, _):
            calls = []

            async def query(kn_id: str) -> str:
                """Read the selected network."""
                calls.append(kn_id)
                return "Network answer"

            tool = StructuredTool.from_function(
                coroutine=query,
                name="query",
                metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
            )

            async def tools(*args, **kwargs):
                return [tool]

            saver = MemorySaver()

            @asynccontextmanager
            async def checkpoint():
                yield saver

            model = Model(
                responses=[
                    AIMessage(
                        content="",
                        tool_calls=[
                            {
                                "name": "query",
                                "args": {"kn_id": "network-1"},
                                "id": "real-call",
                            }
                        ],
                    ),
                    AIMessage(
                        content=[
                            {"type": "reasoning", "text": "private reasoning"},
                            {"type": "text", "text": "Final answer"},
                        ],
                        usage_metadata={
                            "input_tokens": 5,
                            "output_tokens": 2,
                            "total_tokens": 7,
                        },
                    ),
                ]
            )
            monkeypatch.setattr(graph, "load_tools", tools)
            monkeypatch.setattr(
                graph, "build_chat_model", lambda *args, **kwargs: model
            )
            monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
            response = await client.post(URL, json=BODY, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "succeeded", response.text
            names = [event for event, _ in events]
            assert (
                names.index("tool_started")
                < names.index("tool_finished")
                < names.index("result")
            )
            started = next(data for event, data in events if event == "tool_started")
            finished = next(data for event, data in events if event == "tool_finished")
            assert started["tool_call_id"] == finished["tool_call_id"] == "real-call"
            result = next(data for event, data in events if event == "result")
            assert result["content"]["answer"] == "Final answer"
            assert result["content"]["usage"]["total_tokens"] == 7
            assert calls == ["network-1"]
            assert "private reasoning" not in response.text

    asyncio.run(run())


def test_http_cancel_stops_active_stream_and_persists_terminal(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            started = asyncio.Event()

            async def blocking(
                session, agent, req, account_id, account_type, *, public
            ):
                await dao.touch_thread(
                    session, req.thread_id, agent.agent_id, account_id
                )

                async def events():
                    started.set()
                    await asyncio.Event().wait()
                    yield "unused"

                return events()

            monkeypatch.setattr(executions, "stream_chat", blocking)
            pending = asyncio.create_task(client.post(URL, json=BODY, headers=AUTH))
            await asyncio.wait_for(started.wait(), 2)
            async with factory() as session:
                row = (await session.execute(select(TaskRow))).scalar_one()
                run_id = row.f_task_id
            response = await client.post(f"{URL}/{run_id}/cancel", headers=AUTH)
            assert response.status_code == 200
            stream = await asyncio.wait_for(pending, 2)
            assert parse_sse(stream.text)[-1][1]["status"] == "cancelled"
            assert (await client.get(f"{URL}/{run_id}", headers=AUTH)).json()[
                "status"
            ] == "cancelled"

    asyncio.run(run())


def test_setup_timeout_is_failed_and_retry_does_not_restart(monkeypatch):
    from app.config import config

    async def run():
        async with environment(monkeypatch) as (client, _, calls):

            async def slow(*args, **kwargs):
                calls.append("setup")
                await asyncio.Event().wait()

            monkeypatch.setattr(executions, "stream_chat", slow)
            monkeypatch.setattr(config, "DEFAULT_TIMEOUT_S", 0.01)
            response = await client.post(URL, json=BODY, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "failed"
            assert events[-1][1]["error"]["code"] == "BknAgent.Chat.Timeout"
            assert "result" not in [event for event, _ in events]
            run_id = events[0][1]["run_id"]
            state = (await client.get(f"{URL}/{run_id}", headers=AUTH)).json()
            assert state["error"]["code"] == "BknAgent.Chat.Timeout"
            assert (await client.post(URL, json=BODY, headers=AUTH)).status_code == 200
            assert calls == ["setup"]

    asyncio.run(run())


def test_missing_mcp_session_is_failure_in_public_loop(monkeypatch):
    from app.core import graph

    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, _):
            async with factory() as session:
                row = await session.get(AgentRow, "studio_smart_qa")
                row.f_tools = [{"type": "context_loader"}]
                await session.commit()

            async def unavailable(*args, **kwargs):
                return None

            monkeypatch.setattr(graph.context_loader, "open_session", unavailable)
            monkeypatch.setattr(
                graph,
                "build_chat_model",
                lambda *a, **k: pytest.fail("must not run without MCP"),
            )
            response = await client.post(URL, json=BODY, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "failed"
            assert events[-1][1]["error"]["code"] == "BknAgent.Execution.Unavailable"
            assert not executions._thread_claims

    asyncio.run(run())


def test_asgi_disconnect_cleans_up_without_gc(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (_, factory, _):
            disconnected = asyncio.Event()

            async def blocking(
                session, agent, req, account_id, account_type, *, public
            ):
                await dao.touch_thread(
                    session, req.thread_id, agent.agent_id, account_id
                )

                async def events():
                    yield public.event("token", {"content": "partial"})
                    await asyncio.Event().wait()

                return events()

            monkeypatch.setattr(executions, "stream_chat", blocking)
            body = json.dumps(BODY).encode()
            headers = [
                (b"content-type", b"application/json"),
                (b"authorization", b"Bearer token-1"),
            ]
            request_sent = False
            received = []

            async def receive():
                nonlocal request_sent
                if not request_sent:
                    request_sent = True
                    return {"type": "http.request", "body": body, "more_body": False}
                await disconnected.wait()
                return {"type": "http.disconnect"}

            async def send(message):
                received.append(message)
                if b"event: token" in message.get("body", b""):
                    disconnected.set()

            scope = {
                "type": "http",
                "asgi": {"version": "3.0", "spec_version": "2.3"},
                "http_version": "1.1",
                "method": "POST",
                "scheme": "http",
                "path": URL,
                "raw_path": URL.encode(),
                "query_string": b"",
                "headers": headers,
                "server": ("test", 80),
                "client": ("test", 1000),
                "root_path": "",
            }
            await asyncio.wait_for(app(scope, receive, send), 2)
            async with factory() as session:
                row = (await session.execute(select(TaskRow))).scalar_one()
                assert row.f_status == "failed"
                assert row.f_failure_detail == "cancelled"
                assert row.f_output is None
            assert not executions._active
            assert not executions._thread_claims

    asyncio.run(run())


def test_token_dependency_outage_and_malformed_response_fail_closed(monkeypatch):
    async def run():
        import aiohttp

        class Response:
            status = 200

            async def __aenter__(self):
                return self

            async def __aexit__(self, *args):
                pass

            async def json(self):
                return []

        class Client:
            def __init__(self, **kwargs):
                pass

            async def __aenter__(self):
                return self

            async def __aexit__(self, *args):
                pass

            def post(self, url, **kwargs):
                assert kwargs["allow_redirects"] is False
                return Response()

        monkeypatch.setattr(aiohttp, "ClientSession", Client)
        with pytest.raises(HTTPException) as error:
            await public_auth._post("http://auth", data={"token": "private"})
        assert error.value.status_code == 503

        def unavailable(**kwargs):
            raise aiohttp.ClientConnectionError("private details")

        monkeypatch.setattr(aiohttp, "ClientSession", unavailable)
        with pytest.raises(HTTPException) as error:
            await public_auth._post("http://auth", data={"token": "private"})
        assert error.value.status_code == 503
        assert "private details" not in str(error.value.detail)

    asyncio.run(run())


@pytest.mark.parametrize(
    "name",
    ["run_code", "run_shell", "execute_tool", "execute_skill", "execute_native_tool"],
)
def test_code_cannot_become_scoped_by_advertising_a_network_field(name):
    async def run():
        async def code(kn_id: str, code: str) -> str:
            """Execute arbitrary code."""
            pytest.fail("arbitrary code must never execute")

        tool = StructuredTool.from_function(
            coroutine=code,
            name=name,
            metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
        )
        guarded = PublicChat("run", "network-1").guard_tools([tool])[0]
        result = await guarded.ainvoke(
            {"kn_id": "network-1", "code": "anything", "__scoped": True}
        )
        assert "unavailable" in result

    asyncio.run(run())


def test_cancelled_real_tool_loop_can_continue_without_replaying_tool(monkeypatch):
    from langchain_core.language_models.fake_chat_models import (
        FakeMessagesListChatModel,
    )
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph

    class Model(FakeMessagesListChatModel):
        def bind_tools(self, tools, **kwargs):
            return self

        def _generate(self, messages, *args, **kwargs):
            # A real model rejects assistant tool_calls without matching tool
            # responses before the next user message. The fake must check it.
            pending = set()
            for message in messages:
                if isinstance(message, AIMessage):
                    pending.update(call["id"] for call in message.tool_calls)
                if isinstance(message, ToolMessage):
                    pending.discard(message.tool_call_id)
                if message.type == "human":
                    assert not pending, "unanswered tool calls in resumed history"
            return super()._generate(messages, *args, **kwargs)

    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, _):
            started = asyncio.Event()
            calls = []

            async def query(kn_id: str) -> str:
                """Read a network."""
                calls.append(kn_id)
                started.set()
                await asyncio.Event().wait()
                return "unreachable"

            tool = StructuredTool.from_function(
                coroutine=query,
                name="query",
                metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
            )

            async def tools(*args, **kwargs):
                return [tool]

            saver = MemorySaver()

            @asynccontextmanager
            async def checkpoint():
                yield saver

            model = Model(
                responses=[
                    AIMessage(
                        content="",
                        tool_calls=[
                            {
                                "name": "query",
                                "args": {"kn_id": "network-1"},
                                "id": "interrupted",
                            }
                        ],
                    )
                ]
            )
            monkeypatch.setattr(graph, "load_tools", tools)
            monkeypatch.setattr(
                graph, "build_chat_model", lambda *args, **kwargs: model
            )
            monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
            pending = asyncio.create_task(client.post(URL, json=BODY, headers=AUTH))
            await asyncio.wait_for(started.wait(), 2)
            async with factory() as session:
                row = (await session.execute(select(TaskRow))).scalar_one()
                run_id, thread_id = row.f_task_id, row.f_parent_thread_id
            await client.post(f"{URL}/{run_id}/cancel", headers=AUTH)
            await asyncio.wait_for(pending, 2)
            model = Model(responses=[AIMessage(content="Continued answer")])
            continued = await client.post(
                URL,
                json={
                    **BODY,
                    "client_request_id": "after-cancel",
                    "thread_id": thread_id,
                },
                headers=AUTH,
            )
            assert parse_sse(continued.text)[-1][1]["status"] == "succeeded", (
                continued.text
            )
            assert calls == ["network-1"]

    asyncio.run(run())


@pytest.mark.parametrize("account_type", ["other", "id_card", "contactor"])
def test_appkey_uses_the_same_user_mapping_as_context_loader(monkeypatch, account_type):
    async def run():
        async def post(*args, **kwargs):
            return {"active": True, "sub": "user-1", "account_type": account_type}

        monkeypatch.setattr(public_auth, "_post", post)
        request = Request(
            {"type": "http", "headers": [(b"authorization", b"Bearer bak_private")]}
        )
        assert await public_auth.get_public_account(request) == Account(
            "user-1", "user"
        )

    asyncio.run(run())
