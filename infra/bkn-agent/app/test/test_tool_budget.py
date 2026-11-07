"""The model hears its tool call budget, and how many calls are left once few are."""

import asyncio

from langchain.agents import create_agent
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage
from langchain_core.tools import tool

from app.core import tool_budget, tools


SEEN: list = []


class RecordingModel(FakeMessagesListChatModel):
    """Answers from a script and keeps every request's messages in SEEN."""

    def bind_tools(self, tools, **kwargs):
        return self

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        SEEN.append(list(messages))
        return super()._generate(messages, stop=stop, run_manager=run_manager, **kwargs)


def _call(i: int) -> AIMessage:
    return AIMessage(
        content="",
        tool_calls=[{"name": "lookup", "args": {"key": str(i)}, "id": f"call-{i}", "type": "tool_call"}],
    )


def test_the_prompt_states_the_budget_and_nothing_without_one():
    assert "at most 40 times" in tool_budget.budget_instruction(40)
    assert tool_budget.budget_instruction(None) == ""
    assert tool_budget.budget_instruction(0) == ""
    assert tool_budget.budget_middleware(None, {}) == []
    assert tool_budget.budget_middleware(0, {}) == []


def test_the_note_starts_at_a_quarter_left_and_says_when_none_are():
    assert tool_budget.budget_note(40, 29) is None
    assert "10 of the 40 tool calls" in tool_budget.budget_note(40, 30)
    assert "1 of the 40 tool calls" in tool_budget.budget_note(40, 39)
    assert "The 40 tool calls of this run are used up" in tool_budget.budget_note(40, 40)
    assert "used up" in tool_budget.budget_note(40, 45)
    # A small budget still hears of its last three calls.
    assert tool_budget.budget_note(8, 4) is None
    assert "3 of the 8 tool calls" in tool_budget.budget_note(8, 5)


class _Request:
    def __init__(self, messages):
        self.messages = messages

    def override(self, messages):
        return _Request(messages)


def test_the_calls_left_are_the_caps_own_count():
    """Regression: the note counted tool results, but a read the repeated-read cache
    answered never reaches the cap, so the model heard "used up" with calls left."""

    @tool
    async def lookup(key: str) -> str:
        """Look a key up."""
        return f"value-{key}"

    left: dict = {}
    capped = tools.apply_tool_call_cap([lookup], 4, "", "", left)
    notice = tool_budget.budget_middleware(4, left)[0]
    # Four results in the thread, all from the cache: the cap has made no call.
    cached = [HumanMessage(content="q")]
    for i in range(1, 5):
        cached += [_call(i), ToolMessage(content=f"value-{i}", tool_call_id=f"call-{i}")]
    assert notice._request(_Request(cached)).messages == cached
    for i in range(3):
        asyncio.run(capped[0].coroutine(key=str(i)))
    assert "1 of the 4 tool calls" in notice._request(_Request(cached)).messages[-1].content


def test_the_model_hears_the_calls_left_and_the_thread_keeps_no_note():
    """Regression: with max_tool_calls=4 the model was told nothing until a call
    was refused, so it kept probing and the question went unanswered."""
    calls: list = []

    @tool
    async def lookup(key: str) -> str:
        """Look a key up."""
        calls.append(key)
        return f"value-{key}"

    SEEN.clear()
    model = RecordingModel(responses=[_call(1), _call(2), _call(3), _call(4), AIMessage(content="done")])
    left: dict = {}
    capped = tools.apply_tool_call_cap([lookup], 4, "", "", left)
    graph = create_agent(
        model,
        capped,
        system_prompt="You answer." + tool_budget.budget_instruction(4),
        middleware=tool_budget.budget_middleware(4, left),
    )

    result = asyncio.run(graph.ainvoke({"messages": [HumanMessage(content="Look things up")]}))

    def note(request):
        last = request[-1]
        return last.content if isinstance(last, HumanMessage) and "Platform note" in last.content else None

    assert len(SEEN) == 5
    assert "at most 4 times" in SEEN[0][0].content
    # Four calls, and the note starts at three left: after the first result.
    assert note(SEEN[0]) is None
    assert ["3 of the 4" in note(SEEN[1]), "2 of the 4" in note(SEEN[2]), "1 of the 4" in note(SEEN[3])] == [True] * 3
    assert "used up" in note(SEEN[4])
    assert calls == ["1", "2", "3", "4"]
    # The notes were in the requests only, and the tool results are untouched.
    saved = result["messages"]
    assert not any(isinstance(m, HumanMessage) and "Platform note" in m.content for m in saved)
    assert [m.content for m in saved if isinstance(m, ToolMessage)] == ["value-1", "value-2", "value-3", "value-4"]
