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
        # Tools that cannot enforce the network are never offered to the model.
        assert [tool.name for tool in guarded] == ["query"]
        assert await guarded[0].ainvoke({"kn_id": "network-1"}) == "ok"
        assert "ToolScopeMismatch" in await guarded[0].ainvoke({"kn_id": "network-2"})
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
            # Leave room for real SQLite commits on a busy CI worker; the
            # model setup deliberately waits forever, so timeout stays certain.
            monkeypatch.setattr(config, "DEFAULT_TIMEOUT_S", 1)
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
        assert PublicChat("run", "network-1").guard_tools([tool]) == []

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


@pytest.mark.parametrize(
    "extra_headers", [{}, {"x-account-id": "victim", "x-account-type": "app"}]
)
@pytest.mark.parametrize("operation_admitted", [False, True])
def test_verified_http_identity_reaches_actual_model_request(
    monkeypatch, extra_headers, operation_admitted
):
    from langchain_core.messages import AIMessageChunk
    from langchain_core.outputs import ChatGenerationChunk
    from langchain_openai import ChatOpenAI
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph, llm

    async def run():
        async with environment(monkeypatch, stream=False) as (client, _, _):
            observed = []
            monkeypatch.setattr(
                llm.evidence,
                "new_operation",
                lambda: ("op-test", "event-test") if operation_admitted else ("", None),
            )

            @asynccontextmanager
            async def checkpoint():
                yield MemorySaver()

            async def provider(self, *args, **kwargs):
                observed.append(kwargs.get("extra_headers", {}))
                yield ChatGenerationChunk(message=AIMessageChunk(content="Answer"))

            monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
            monkeypatch.setattr(ChatOpenAI, "_astream", provider)
            response = await client.post(
                URL,
                json=BODY,
                headers={
                    **AUTH,
                    **extra_headers,
                    "x-bkn-effective-subject-id": "victim",
                    "x-bkn-effective-subject-type": "service",
                    "x-bkn-application-principal-id": "victim-app",
                    "x-bkn-delegation-id": "untrusted",
                },
            )
            assert parse_sse(response.text)[-1][1]["status"] == "succeeded", (
                response.text
            )
            assert observed
            assert all(
                headers.get("x-account-id") == "user-1" for headers in observed
            ), observed
            assert all(headers.get("x-account-type") == "user" for headers in observed)
            assert all(headers.get("bkn-request-id") for headers in observed)

    asyncio.run(run())


def test_oauth_introspection_uses_hydra_admin_http_route(monkeypatch):
    from aiohttp import web

    from app.config import config

    real_post = public_auth._post

    async def run():
        requests = []

        async def introspect(request):
            form = await request.post()
            requests.append(dict(form))
            assert "x-account-id" not in request.headers
            return web.json_response(
                {"active": True, "sub": "user-1", "ext": {"visitor_type": "realname"}}
            )

        server = web.Application()
        server.router.add_post("/admin/oauth2/introspect", introspect)
        runner = web.AppRunner(server)
        await runner.setup()
        try:
            await web.TCPSite(runner, "127.0.0.1", 0).start()
            monkeypatch.setattr(
                config, "HYDRA_ADMIN_BASE", f"http://127.0.0.1:{runner.addresses[0][1]}"
            )
            async with environment(monkeypatch) as (client, _, _):
                fake_post = public_auth._post

                async def post(url, **kwargs):
                    if "/authz/checks" in url:
                        return await fake_post(url, **kwargs)
                    return await real_post(url, **kwargs)

                monkeypatch.setattr(public_auth, "_post", post)
                response = await client.post(
                    URL,
                    json=BODY,
                    headers={**AUTH, "x-account-id": "victim", "x-account-type": "app"},
                )
                assert response.status_code == 200, response.text
                assert parse_sse(response.text)[-1][1]["status"] == "succeeded"
                assert requests == [{"token": "token-1"}]
        finally:
            await runner.cleanup()

    asyncio.run(run())


@pytest.mark.parametrize("language,chinese", [("en-US", False), ("zh-CN", True)])
def test_public_tool_error_and_failure_details_follow_request_language(
    monkeypatch, language, chinese
):
    async def run():
        async with environment(monkeypatch) as (client, _, _):

            async def blocked(session, agent, req, account_id, account_type, *, public):
                async def query(**kwargs):
                    pytest.fail("a foreign network must not be queried")

                tool = StructuredTool.from_function(
                    coroutine=query,
                    name="query",
                    description="Query",
                    args_schema={
                        "type": "object",
                        "properties": {"kn_id": {"type": "string"}},
                    },
                    metadata={
                        "bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN
                    },
                )
                guarded = public.guard_tools([tool])[0]

                async def events():
                    result = await guarded.ainvoke({"kn_id": "network-2"})
                    message = ToolMessage(
                        content=result,
                        tool_call_id="blocked",
                        name="query",
                        status="error",
                    )
                    for event in public.updates({"tools": {"messages": [message]}}):
                        yield event
                    yield 'event: error\ndata: {"code":"BknAgent.Execution.Failed"}\n\n'

                return events()

            monkeypatch.setattr(executions, "stream_chat", blocked)
            response = await client.post(
                URL, json=BODY, headers={**AUTH, "Accept-Language": language}
            )
            events = parse_sse(response.text)
            detail = next(
                data["output"] for name, data in events if name == "tool_finished"
            )
            assert detail["code"] == "BknAgent.Execution.ToolScopeMismatch"
            assert (
                any("\u4e00" <= char <= "\u9fff" for char in detail["detail"])
                == chinese
            )
            failure = events[-1][1]["error"]
            assert failure["code"] == "BknAgent.Execution.Failed"
            assert (
                any("\u4e00" <= char <= "\u9fff" for char in failure["detail"])
                == chinese
            )
            run_id = events[0][1]["run_id"]
            read = (
                await client.get(
                    f"{URL}/{run_id}", headers={**AUTH, "Accept-Language": language}
                )
            ).json()
            assert read["error"] == failure

    asyncio.run(run())


def test_large_tool_projection_keeps_a_readable_prefix():
    rows = [{"id": index, "name": f"row-{index}" * 40} for index in range(100)]
    projected = project(json.dumps(rows))
    assert isinstance(projected, str)
    assert projected.startswith('[{"id": 0')
    assert projected.endswith("…[truncated]")
    assert len(projected) <= 16000 + len("…[truncated]")
    # Small results keep their structure.
    assert project('{"value": 7}') == {"value": 7}


def _closing_model(responses):
    from langchain_core.language_models.fake_chat_models import (
        FakeMessagesListChatModel,
    )

    class Model(FakeMessagesListChatModel):
        def bind_tools(self, tools, **kwargs):
            return self

        def _generate(self, messages, *args, **kwargs):
            # The closing call must not leave tool calls unanswered either.
            pending = set()
            for message in messages:
                if isinstance(message, AIMessage):
                    pending.update(call["id"] for call in message.tool_calls)
                if isinstance(message, ToolMessage):
                    pending.discard(message.tool_call_id)
            assert not pending or self.i < len(self.responses) - 1, (
                "closing call saw unanswered tool calls"
            )
            return super()._generate(messages, *args, **kwargs)

    return Model(responses=responses)


async def _run_closing_turn(
    monkeypatch,
    model,
    limits,
    tool_delay=0,
    tool_result="partial data",
    tool_name="query",
    runs=None,
):
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph

    async with environment(monkeypatch, stream=False) as (client, factory, _):
        async with factory() as session:
            agent = await session.get(AgentRow, "studio_smart_qa")
            agent.f_limits = limits
            await session.commit()

        async def query(kn_id: str) -> str:
            """Read the selected network."""
            if runs is not None:
                runs.append(kn_id)
            await asyncio.sleep(tool_delay)
            return tool_result

        tool = StructuredTool.from_function(
            coroutine=query,
            name=tool_name,
            metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
        )

        async def tools(*args, **kwargs):
            return [tool]

        saver = MemorySaver()

        @asynccontextmanager
        async def checkpoint():
            yield saver

        monkeypatch.setattr(graph, "load_tools", tools)
        monkeypatch.setattr(graph, "build_chat_model", lambda *args, **kwargs: model)
        monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
        response = await client.post(URL, json=BODY, headers=AUTH)
        events = parse_sse(response.text)
        thread_id = events[0][1]["thread_id"]
        state = saver.get_tuple({"configurable": {"thread_id": thread_id}})
        return events, state.checkpoint["channel_values"]["messages"]


def test_step_limit_closes_with_an_answer_instead_of_failing(monkeypatch):
    def call(call_id):
        return AIMessage(
            content="",
            tool_calls=[
                {"name": "query", "args": {"kn_id": "network-1"}, "id": call_id}
            ],
        )

    model = _closing_model(
        [call("first"), call("second"), AIMessage(content="Closing answer")]
    )

    events, messages = asyncio.run(
        _run_closing_turn(monkeypatch, model, {"max_turns": 1})
    )

    assert events[-1][1]["status"] == "succeeded", events
    result = next(data for event, data in events if event == "result")
    assert result["content"]["answer"] == "Closing answer"
    # The step that asked for a tool is answered, and the closing answer is
    # saved so the next turn continues from it.
    tool_results = {m.tool_call_id for m in messages if isinstance(m, ToolMessage)}
    assert {"first", "second"} <= tool_results
    assert messages[-1].content == "Closing answer"


def test_closing_call_gets_budgeted_tool_results(monkeypatch):
    def call(call_id):
        return AIMessage(
            content="",
            tool_calls=[{"name": "query", "args": {"kn_id": "network-1"}, "id": call_id}],
        )

    base = type(_closing_model([]))
    seen = []

    class Model(base):
        def _generate(self, messages, *args, **kwargs):
            seen.append([m for m in messages if isinstance(m, ToolMessage)])
            return super()._generate(messages, *args, **kwargs)

    model = Model(responses=[call("a"), call("b"), call("c"), AIMessage(content="Closing")])
    limits = {"max_turns": 2, "keep_tool_results": 1, "data_result_chars": 1000}
    events, messages = asyncio.run(
        _run_closing_turn(monkeypatch, model, limits, tool_result="x" * 3000)
    )

    assert events[-1][1]["status"] == "succeeded", events
    closing = seen[-1]
    # Every raw result is in the saved thread, but the closing call, like the
    # loop's calls, sees them capped or replaced, never in full.
    assert len(closing) == 3
    assert all(len(m.content) < 1500 for m in closing)
    assert any("omitted to save context" in m.content for m in closing)
    # The interrupted call's fill does not take the kept slot: the newest real
    # result is still visible, truncated, as it was to the loop's last call.
    real = [m for m in closing if m.status != "error"]
    assert "Result truncated" in real[-1].content
    saved = [m for m in messages if isinstance(m, ToolMessage) and m.status != "error"]
    assert all(m.content == "x" * 3000 for m in saved)


def test_empty_final_message_gets_a_closing_answer(monkeypatch):
    model = _closing_model(
        [AIMessage(content=""), AIMessage(content="Recovered answer")]
    )

    events, _ = asyncio.run(_run_closing_turn(monkeypatch, model, None))

    assert events[-1][1]["status"] == "succeeded", events
    result = next(data for event, data in events if event == "result")
    assert result["content"]["answer"] == "Recovered answer"


def test_answer_excludes_narration_spoken_before_tool_calls(monkeypatch):
    model = _closing_model(
        [
            AIMessage(
                content="Let me look that up first.",
                tool_calls=[
                    {"name": "query", "args": {"kn_id": "network-1"}, "id": "call"}
                ],
            ),
            AIMessage(content="Final answer"),
        ]
    )

    events, _ = asyncio.run(_run_closing_turn(monkeypatch, model, None))

    tokens = "".join(data["content"] for event, data in events if event == "token")
    result = next(data for event, data in events if event == "result")
    # Progress still streams, but the committed answer is the last message only.
    assert "Let me look that up first." in tokens
    assert result["content"]["answer"] == "Final answer"


def test_time_budget_keeps_room_to_close_with_an_answer(monkeypatch):
    model = _closing_model(
        [
            AIMessage(
                content="",
                tool_calls=[
                    {"name": "query", "args": {"kn_id": "network-1"}, "id": "slow"}
                ],
            ),
            AIMessage(content="Answer from what was found"),
        ]
    )

    # timeout_s=1 leaves 0.2s after the loop; the tool alone would take 3s.
    events, messages = asyncio.run(
        _run_closing_turn(monkeypatch, model, {"timeout_s": 1}, tool_delay=3)
    )

    assert events[-1][1]["status"] == "succeeded", events
    result = next(data for event, data in events if event == "result")
    assert result["content"]["answer"] == "Answer from what was found"
    # The interrupted call is answered as unknown, never replayed as success.
    assert any(
        isinstance(m, ToolMessage) and m.tool_call_id == "slow" and m.status == "error"
        for m in messages
    )


def test_malformed_tool_result_is_a_failed_call_not_a_failed_turn():
    async def run():
        async def query(kn_id: str) -> str:
            """Read the network."""
            raise RuntimeError(
                "Invalid structured content returned by tool query: None is not of type 'array'"
            )

        tool = StructuredTool.from_function(
            coroutine=query,
            name="query",
            metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
        )
        guarded = PublicChat("run", "network-1").guard_tools([tool])[0]
        result = await guarded.ainvoke({"kn_id": "network-1"})
        assert "Invalid structured content" in result

    asyncio.run(run())


def test_streamed_reasoning_is_kept_by_the_model_client():
    from app.core.llm import TraceChatOpenAI
    from langchain_core.messages import AIMessageChunk

    model = TraceChatOpenAI(base_url="http://unused", api_key="x", model="m")
    chunk = {
        "choices": [
            {
                "index": 0,
                "delta": {
                    "role": "assistant",
                    "content": "",
                    "reasoning_content": "think",
                },
            }
        ]
    }
    generation = model._convert_chunk_to_generation_chunk(chunk, AIMessageChunk, {})
    assert generation.message.additional_kwargs["reasoning_content"] == "think"
    assert generation.message.content == ""


def test_reasoning_streams_as_its_own_event_and_stays_out_of_the_answer(monkeypatch):
    from langchain_core.language_models.chat_models import BaseChatModel
    from langchain_core.messages import AIMessageChunk
    from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph

    class Thinking(BaseChatModel):
        @property
        def _llm_type(self):
            return "thinking"

        def bind_tools(self, tools, **kwargs):
            return self

        def _generate(self, messages, stop=None, run_manager=None, **kwargs):
            return ChatResult(
                generations=[ChatGeneration(message=AIMessage(content="Answer"))]
            )

        async def _astream(self, messages, stop=None, run_manager=None, **kwargs):
            yield ChatGenerationChunk(
                message=AIMessageChunk(
                    content="", additional_kwargs={"reasoning_content": "Let me think"}
                )
            )
            yield ChatGenerationChunk(message=AIMessageChunk(content="Answer"))

    async def run():
        async with environment(monkeypatch, stream=False) as (client, _, _):
            saver = MemorySaver()

            @asynccontextmanager
            async def checkpoint():
                yield saver

            async def tools(*args, **kwargs):
                return []

            monkeypatch.setattr(graph, "load_tools", tools)
            monkeypatch.setattr(graph, "build_chat_model", lambda *a, **k: Thinking())
            monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
            response = await client.post(URL, json=BODY, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "succeeded", response.text
            reasoning = [
                data["content"] for event, data in events if event == "reasoning"
            ]
            assert reasoning == ["Let me think"]
            result = next(data for event, data in events if event == "result")
            assert result["content"]["answer"] == "Answer"

    asyncio.run(run())


def test_reasoning_tokens_are_sent_in_pieces_before_the_next_event(monkeypatch):
    from app.core import public_chat

    chat = public_chat.PublicChat(run_id="run", kn_id="kn")
    monkeypatch.setattr(public_chat.time, "monotonic", lambda: 100.0)
    held = [chat.reasoning(t) for t in ["Let", " me", " think"]]
    assert held == ["", "", ""]

    out = parse_sse(chat.event("token", {"content": "Answer"}))
    assert out == [
        ("reasoning", {"content": "Let me think", "run_id": "run", "sequence": 1}),
        ("token", {"content": "Answer", "run_id": "run", "sequence": 2}),
    ]
    # Nothing held, nothing prepended.
    assert parse_sse(chat.event("usage", {}))[0][0] == "usage"


def test_reasoning_is_flushed_once_enough_text_or_time_gathers(monkeypatch):
    from app.core import public_chat

    clock = [0.0]
    monkeypatch.setattr(public_chat.time, "monotonic", lambda: clock[0])
    chat = public_chat.PublicChat(run_id="run", kn_id="kn")

    assert chat.reasoning("x" * (public_chat.REASONING_FLUSH_CHARS - 1)) == ""
    sent = parse_sse(chat.reasoning("y"))
    assert sent[0][1]["content"] == "x" * (public_chat.REASONING_FLUSH_CHARS - 1) + "y"

    assert chat.reasoning("slow") == ""
    clock[0] += public_chat.REASONING_FLUSH_S
    assert parse_sse(chat.reasoning(" thought"))[0][1]["content"] == "slow thought"


def test_model_sees_capped_and_cleared_tool_results_but_history_keeps_them(monkeypatch):
    from langchain_core.language_models.fake_chat_models import (
        FakeMessagesListChatModel,
    )

    seen = []

    class Model(FakeMessagesListChatModel):
        def bind_tools(self, tools, **kwargs):
            return self

        def _generate(self, messages, *args, **kwargs):
            seen.append([m for m in messages if isinstance(m, ToolMessage)])
            return super()._generate(messages, *args, **kwargs)

    def call(call_id):
        return AIMessage(
            content="",
            tool_calls=[
                {"name": "query", "args": {"kn_id": "network-1"}, "id": call_id}
            ],
        )

    model = Model(
        responses=[call(f"c{i}") for i in range(4)] + [AIMessage(content="Done")]
    )

    limits = {"keep_tool_results": 2, "data_result_chars": 1000, "max_turns": 10}
    events, messages = asyncio.run(
        _run_closing_turn(monkeypatch, model, limits, tool_result="x" * 3000)
    )

    assert events[-1][1]["status"] == "succeeded", events
    last = seen[-1]
    # Only the two most recent results are kept, each capped with a note.
    full = [m for m in last if "Result truncated" in m.content]
    cleared = [m for m in last if "omitted to save context" in m.content]
    assert len(full) == 2 and len(cleared) == 2
    assert all(len(m.content) < 1500 for m in full)
    # The saved thread keeps every result in full.
    saved = [m for m in messages if isinstance(m, ToolMessage)]
    assert len(saved) == 4 and all(m.content == "x" * 3000 for m in saved)


def test_cap_tool_message_uses_the_wider_schema_cap():
    from app.core.context_budget import cap_tool_message

    data = ToolMessage(content="y" * 5000, tool_call_id="1", name="run_sql")
    schema = ToolMessage(content="y" * 5000, tool_call_id="2", name="get_kn_detail")
    assert "Result truncated" in cap_tool_message(data, 1000, 8000).content
    assert cap_tool_message(schema, 1000, 8000) is schema


def test_earlier_questions_bring_answers_but_not_their_tool_results():
    from langchain_core.messages import HumanMessage

    from app.core.context_budget import ToolResultBudget

    class Request:
        def __init__(self, messages):
            self.messages = messages

        def override(self, messages):
            return Request(messages)

    history = [
        HumanMessage(content="first"),
        AIMessage(content="", tool_calls=[{"name": "q", "args": {}, "id": "a"}]),
        ToolMessage(content="old rows", tool_call_id="a", name="q"),
        AIMessage(content="First answer"),
        HumanMessage(content="second"),
        AIMessage(content="", tool_calls=[{"name": "q", "args": {}, "id": "b"}]),
        ToolMessage(content="new rows", tool_call_id="b", name="q"),
    ]
    sent = ToolResultBudget(1000, 1000, keep=3)._request(Request(history)).messages
    assert "earlier question omitted" in sent[2].content
    assert sent[3].content == "First answer"
    assert sent[6].content == "new rows"
    # The saved history is untouched.
    assert history[2].content == "old rows"


class _Request:
    def __init__(self, messages):
        self.messages = messages

    def override(self, messages):
        return _Request(messages)


def _turn(*sizes, name="run_cypher"):
    """One question whose tool calls return results of the given sizes."""
    from langchain_core.messages import HumanMessage

    messages = [HumanMessage(content="list every material")]
    for i, size in enumerate(sizes):
        call = {"name": name, "args": {"query": f"MATCH page {i}"}, "id": f"c{i}"}
        messages.append(AIMessage(content="", tool_calls=[call]))
        messages.append(ToolMessage(content="r" * size, tool_call_id=f"c{i}", name=name))
    return messages


def test_results_that_fit_the_budget_stay_visible_together():
    from app.core.context_budget import ToolResultBudget

    # keep=2 at 1000 chars allows 2000 chars: the newest large result and the
    # three small ones fit, the oldest large one does not.
    history = _turn(900, 100, 100, 100, 900)
    sent = ToolResultBudget(1000, 1000, keep=2)._request(_Request(history)).messages
    results = [m.content for m in sent if isinstance(m, ToolMessage)]
    assert results[1:] == ["r" * 100] * 3 + ["r" * 900]
    assert "omitted to save context" in results[0]
    # The placeholder names the call, so the model knows what it already ran.
    assert 'run_cypher {"query": "MATCH page 0"}' in results[0]
    assert "about 900 characters" in results[0]


def test_keep_holds_even_when_results_exceed_the_budget():
    from app.core.context_budget import ToolResultBudget

    sent = ToolResultBudget(1000, 1000, keep=2)._request(
        _Request(_turn(3000, 3000, 3000))
    ).messages
    results = [m.content for m in sent if isinstance(m, ToolMessage)]
    assert "omitted to save context" in results[0]
    assert all("Result truncated" in r for r in results[1:])


def test_truncation_note_asks_for_fewer_columns_not_pages():
    from app.core.context_budget import cap_tool_message

    note = cap_tool_message(
        ToolMessage(content="y" * 5000, tool_call_id="1", name="run_cypher"), 1000, 8000
    ).content
    assert "only the columns you need" in note
    assert "most compact response format" in note
    assert "do not split the rows into pages" in note


def test_a_repeated_read_in_the_same_question_reuses_its_result():
    from langchain_core.messages import HumanMessage

    from app.core.context_budget import earlier_result

    query = {"query": "MATCH (b) RETURN b"}
    earlier = [
        HumanMessage(content="first"),
        AIMessage(content="", tool_calls=[{"name": "run_cypher", "args": query, "id": "old"}]),
        ToolMessage(content="stale rows", tool_call_id="old", name="run_cypher"),
        AIMessage(content="First answer"),
        HumanMessage(content="second"),
    ]
    # A result from an earlier question is not reused: the data may have changed.
    again = {"name": "run_cypher", "args": query, "id": "new"}
    assert earlier_result(earlier, again) is None

    first = {"name": "run_cypher", "args": dict(reversed(list(query.items()))), "id": "a"}
    messages = earlier + [
        AIMessage(content="", tool_calls=[first]),
        ToolMessage(content="rows", tool_call_id="a", name="run_cypher"),
        AIMessage(content="", tool_calls=[again]),
    ]
    assert earlier_result(messages, again).content == "rows"
    # A failed result is retried, and calls that are not reads always run.
    messages[-2] = ToolMessage(
        content="boom", tool_call_id="a", name="run_cypher", status="error"
    )
    assert earlier_result(messages, again) is None
    assert earlier_result(messages, {**again, "name": "run_code"}) is None


def test_a_write_since_the_read_makes_it_run_again():
    from app.core.context_budget import earlier_result

    read = {"name": "run_cypher", "args": {"query": "MATCH (o) RETURN o"}, "id": "a"}
    write = {"name": "execute_action", "args": {"action": "close"}, "id": "w"}
    again = {**read, "id": "b"}
    messages = _turn() + [
        AIMessage(content="", tool_calls=[read]),
        ToolMessage(content="open", tool_call_id="a", name="run_cypher"),
        AIMessage(content="", tool_calls=[write]),
        ToolMessage(content="done", tool_call_id="w", name="execute_action"),
        AIMessage(content="", tool_calls=[again]),
    ]
    assert earlier_result(messages, again) is None

    # A write run beside the read leaves its order unknown, so it counts too.
    beside = _turn() + [
        AIMessage(content="", tool_calls=[read, write]),
        ToolMessage(content="open", tool_call_id="a", name="run_cypher"),
        ToolMessage(content="done", tool_call_id="w", name="execute_action"),
        AIMessage(content="", tool_calls=[again]),
    ]
    assert earlier_result(beside, again) is None

    # Other reads in between do not count as writes.
    explore = {"name": "explore_subgraph", "args": {"start": "o1"}, "id": "e"}
    reads = _turn() + [
        AIMessage(content="", tool_calls=[read]),
        ToolMessage(content="open", tool_call_id="a", name="run_cypher"),
        AIMessage(content="", tool_calls=[explore]),
        ToolMessage(content="graph", tool_call_id="e", name="explore_subgraph"),
        AIMessage(content="", tool_calls=[again]),
    ]
    assert earlier_result(reads, again).content == "open"


def test_public_execution_reuses_a_repeated_read(monkeypatch):
    def call(call_id):
        return AIMessage(
            content="",
            tool_calls=[
                {"name": "run_cypher", "args": {"kn_id": "network-1"}, "id": call_id}
            ],
        )

    model = _closing_model([call("first"), call("again"), AIMessage(content="Done")])
    runs = []
    events, messages = asyncio.run(
        _run_closing_turn(
            monkeypatch, model, None, tool_name="run_cypher", tool_result="rows", runs=runs
        )
    )

    assert events[-1][1]["status"] == "succeeded", events
    # The tool ran once; the repeat was answered from the saved result.
    assert runs == ["network-1"]
    results = {m.tool_call_id: m.content for m in messages if isinstance(m, ToolMessage)}
    assert results == {"first": "rows", "again": "rows"}


def test_repeated_read_cache_answers_without_calling_the_tool():
    from app.core.context_budget import RepeatedReadCache

    call = {"name": "run_cypher", "args": {"query": "MATCH page 0"}, "id": "again"}

    class ToolRequest:
        state = {"messages": _turn(10) + [AIMessage(content="", tool_calls=[call])]}
        tool_call = call

    def handler(_request):
        raise AssertionError("the tool must not run again")

    reply = RepeatedReadCache().wrap_tool_call(ToolRequest(), handler)
    assert reply.tool_call_id == "again" and reply.content == "r" * 10


def _summarizer():
    from langchain_core.language_models.fake_chat_models import (
        FakeMessagesListChatModel,
    )

    from app.core.context_budget import TurnSummarization

    model = FakeMessagesListChatModel(responses=[AIMessage(content="summary")])
    return TurnSummarization(
        model, trigger=("tokens", 10), keep=("messages", 2), trim_tokens_to_summarize=4000
    )


def test_summarization_never_folds_the_current_question_away():
    from langchain_core.messages import HumanMessage

    summarizer = _summarizer()
    # One long question: LangChain would cut inside it and drop the question.
    assert summarizer._determine_cutoff_index(_turn(*[100] * 10)) == 0

    earlier = [HumanMessage(content="first"), AIMessage(content="First answer")]
    messages = earlier + _turn(*[100] * 10)
    assert summarizer._determine_cutoff_index(messages) == len(earlier)

    # Only an earlier summary before the question: nothing new to summarize.
    # LangChain's own summary message, so a change to its marker shows up here.
    summary = summarizer._build_new_messages("summary")[0]
    assert summarizer._determine_cutoff_index([summary] + _turn(*[100] * 10)) == 0


def test_summary_input_keeps_the_question_despite_large_tool_results():
    summarizer = _summarizer()
    # Raw results far past the trim window used to leave no question inside it,
    # and the summary became "Previous conversation was too long to summarize."
    trimmed = summarizer._trim_messages_for_summary(
        _turn(*[40000] * 3) + [AIMessage(content="First answer")]
    )
    assert trimmed and trimmed[0].content == "list every material"
    assert all(len(m.content) < 200 for m in trimmed if isinstance(m, ToolMessage))


def _status_error(status, body):
    import httpx
    import openai

    request = httpx.Request("POST", "http://mf/chat/completions")
    return openai.APIStatusError(
        "upstream", response=httpx.Response(status, request=request), body=body
    )


def test_failures_are_classified_with_the_upstream_error():
    import httpx
    import openai

    from app.core.public_chat import classify_failure

    unreachable = openai.APIConnectionError(request=httpx.Request("POST", "http://mf"))
    assert classify_failure(unreachable) == ("BknAgent.Model.Unavailable", None)

    code, upstream = classify_failure(
        _status_error(
            500,
            {
                "error": {
                    "code": "ModelFactory.Mydb.DataBase.ParameterError",
                    "message": "模型工厂暂时无法访问数据库。 Bearer secret-token",
                }
            },
        )
    )
    assert code == "BknAgent.Model.Unavailable"
    assert upstream["code"] == "ModelFactory.Mydb.DataBase.ParameterError"
    assert "secret-token" not in upstream["message"]

    code, _ = classify_failure(
        _status_error(400, {"error": {"message": "context too long"}})
    )
    assert code == "BknAgent.Model.Rejected"
    assert classify_failure(RuntimeError("boom")) == ("BknAgent.Chat.Failed", None)

    # An error event inside an accepted stream has no HTTP status; it is still
    # the model's refusal, and its message is the real cause.
    mid_stream = openai.APIError(
        "Insufficient Balance sk-leaked",
        request=httpx.Request("POST", "http://mf"),
        body={"code": "insufficient_balance", "type": "invalid_request_error"},
    )
    code, upstream = classify_failure(mid_stream)
    assert code == "BknAgent.Model.Rejected"
    assert upstream["code"] == "insufficient_balance"
    assert upstream["message"].startswith("Insufficient Balance")
    assert "sk-leaked" not in upstream["message"]

    # Model Factory's stream is always HTTP 200, so a busy, rate-limited or
    # unreachable provider also arrives as an error frame: worth retrying.
    for body in (
        {"code": "ModelFactory.Upstream", "type": "service_unavailable_error"},
        {"code": "ModelFactory.Upstream", "type": "rate_limit_exceeded"},
        {"code": "ModelFactory.Upstream", "type": "api_connection_error"},
        # Passed through from the provider: Anthropic overload and server errors,
        # and OpenAI's 429, whose rate-limit marker is the code.
        {"type": "overloaded_error"},
        {"type": "api_error"},
        {"code": "rate_limit_exceeded", "type": "tokens"},
    ):
        transient = openai.APIError(
            "Service is too busy", request=httpx.Request("POST", "http://mf"), body=body
        )
        code, upstream = classify_failure(transient)
        assert code == "BknAgent.Model.Unavailable", body
        assert upstream["message"] == "Service is too busy"


def test_terminal_names_the_real_failure(monkeypatch):
    from langchain_core.language_models.chat_models import BaseChatModel
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph

    class Broken(BaseChatModel):
        @property
        def _llm_type(self):
            return "broken"

        def bind_tools(self, tools, **kwargs):
            return self

        def _generate(self, messages, stop=None, run_manager=None, **kwargs):
            raise _status_error(
                500, {"error": {"code": "ModelFactory.Down", "message": "数据库不可用"}}
            )

    async def run():
        async with environment(monkeypatch, stream=False) as (client, _, _):
            saver = MemorySaver()

            @asynccontextmanager
            async def checkpoint():
                yield saver

            async def tools(*args, **kwargs):
                return []

            monkeypatch.setattr(graph, "load_tools", tools)
            monkeypatch.setattr(graph, "build_chat_model", lambda *a, **k: Broken())
            monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
            response = await client.post(
                URL, json=BODY, headers={**AUTH, "Accept-Language": "zh-CN"}
            )
            terminal = parse_sse(response.text)[-1][1]
            assert terminal["status"] == "failed"
            assert terminal["error"]["code"] == "BknAgent.Model.Unavailable"
            assert terminal["error"]["detail"] == "模型服务暂时不可用。"
            assert terminal["error"]["upstream"] == {
                "code": "ModelFactory.Down",
                "message": "数据库不可用",
            }

    asyncio.run(run())


def test_tool_events_tell_unreachable_calls_from_tool_errors():
    async def run():
        async def down(kn_id: str) -> str:
            """Read the network."""
            raise ConnectionError("connection refused")

        tool = StructuredTool.from_function(
            coroutine=down,
            name="query",
            metadata={"bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN},
        )
        public = PublicChat("run", "network-1")
        unreachable = await public.guard_tools([tool])[0].ainvoke(
            {"kn_id": "network-1"}
        )
        messages = [
            AIMessage(
                content="",
                tool_calls=[
                    {"name": "query", "args": {}, "id": "a"},
                    {"name": "run_cypher", "args": {}, "id": "b"},
                ],
            ),
            ToolMessage(
                content=unreachable, tool_call_id="a", name="query", status="error"
            ),
            ToolMessage(
                content='{"code":"BknBackend.Cypher.Unsupported"}',
                tool_call_id="b",
                name="run_cypher",
                status="error",
            ),
        ]
        events = parse_sse("".join(public.updates({"tools": {"messages": messages}})))
        finished = {d["tool_call_id"]: d for e, d in events if e == "tool_finished"}
        assert finished["a"]["error_kind"] == "unavailable"
        assert finished["b"]["error_kind"] == "error"

    asyncio.run(run())
