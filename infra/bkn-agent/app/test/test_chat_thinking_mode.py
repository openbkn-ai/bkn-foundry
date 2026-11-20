"""The chat route passes x-bkn-agent-thinking-mode to the model, as the task routes do."""
import asyncio
from types import SimpleNamespace

from starlette.requests import Request


def _request(headers: dict[str, str]) -> Request:
    raw = [(k.lower().encode(), v.encode()) for k, v in headers.items()]
    return Request({"type": "http", "method": "POST", "path": "/chat", "headers": raw})


def test_chat_turns_the_models_thinking_off_while_set_up_and_while_streaming(monkeypatch):
    from app.core import llm
    from app.routers import chat as chat_router

    seen = {}

    async def get_agent(_session, agent_id):
        return SimpleNamespace(agent_id=agent_id, mode="chat")

    async def stream_chat(_session, _agent, _req, _account_id, _account_type):
        seen["setup"] = llm.current_thinking_mode()

        async def events():
            seen["stream"] = llm.current_thinking_mode()
            yield "event: done\n\n"

        return events()

    monkeypatch.setattr(chat_router.dao, "get_agent", get_agent)
    monkeypatch.setattr(chat_router, "stream_chat", stream_chat)

    async def run():
        response = await chat_router.chat(
            SimpleNamespace(agent_id="a1"), _request({"x-bkn-agent-thinking-mode": "disabled"}),
            account=SimpleNamespace(account_id="u1", account_type="user"), session=None)
        # The handler has returned: its own setting is gone before the stream is read.
        seen["after_handler"] = llm.current_thinking_mode()
        return [chunk async for chunk in response.body_iterator]

    chunks = asyncio.run(run())

    assert chunks == ["event: done\n\n"]
    assert seen == {"setup": "disabled", "after_handler": "", "stream": "disabled"}


def test_chat_without_the_header_leaves_thinking_to_the_model(monkeypatch):
    from app.core import llm
    from app.routers import chat as chat_router

    seen = {}

    async def get_agent(_session, agent_id):
        return SimpleNamespace(agent_id=agent_id, mode="chat")

    async def stream_chat(*_args):
        seen["setup"] = llm.current_thinking_mode()

        async def events():
            seen["stream"] = llm.current_thinking_mode()
            yield "x"

        return events()

    monkeypatch.setattr(chat_router.dao, "get_agent", get_agent)
    monkeypatch.setattr(chat_router, "stream_chat", stream_chat)

    async def run():
        response = await chat_router.chat(SimpleNamespace(agent_id="a1"), _request({}),
                                          account=SimpleNamespace(account_id="u1", account_type="user"), session=None)
        return [chunk async for chunk in response.body_iterator]

    asyncio.run(run())
    assert seen == {"setup": "", "stream": ""}
