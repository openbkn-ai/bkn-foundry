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
    assert "最多调用工具 40 次" in tool_budget.budget_instruction(40)
    assert tool_budget.budget_instruction(None) == ""
    assert tool_budget.budget_instruction(0) == ""
    assert tool_budget.budget_middleware(None) == []
    assert tool_budget.budget_middleware(0) == []


def test_the_note_starts_at_a_quarter_left_and_says_when_none_are():
    assert tool_budget.budget_note(40, 29) is None
    assert "还剩 10 次（共 40 次）" in tool_budget.budget_note(40, 30)
    assert "还剩 1 次" in tool_budget.budget_note(40, 39)
    assert "已用完（共 40 次）" in tool_budget.budget_note(40, 40)
    assert "已用完" in tool_budget.budget_note(40, 45)
    # A small budget still hears of its last three calls.
    assert tool_budget.budget_note(8, 4) is None
    assert "还剩 3 次" in tool_budget.budget_note(8, 5)


def test_only_the_current_questions_calls_count():
    messages = [
        HumanMessage(content="first"),
        _call(1),
        ToolMessage(content="r1", tool_call_id="call-1"),
        AIMessage(content="answer"),
        HumanMessage(content="second"),
        _call(2),
        ToolMessage(content="r2", tool_call_id="call-2"),
    ]
    assert tool_budget.calls_this_question(messages) == 1


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
    capped = tools.apply_tool_call_cap([lookup], 4)
    graph = create_agent(
        model,
        capped,
        system_prompt="You answer." + tool_budget.budget_instruction(4),
        middleware=tool_budget.budget_middleware(4),
    )

    result = asyncio.run(graph.ainvoke({"messages": [HumanMessage(content="Look things up")]}))

    def note(request):
        last = request[-1]
        return last.content if isinstance(last, HumanMessage) and "平台提示" in last.content else None

    assert len(SEEN) == 5
    assert "最多调用工具 4 次" in SEEN[0][0].content
    # Four calls, and the note starts at three left: after the first result.
    assert note(SEEN[0]) is None
    assert ["还剩 3 次" in note(SEEN[1]), "还剩 2 次" in note(SEEN[2]), "还剩 1 次" in note(SEEN[3])] == [True] * 3
    assert "已用完" in note(SEEN[4])
    assert calls == ["1", "2", "3", "4"]
    # The notes were in the requests only, and the tool results are untouched.
    saved = result["messages"]
    assert not any(isinstance(m, HumanMessage) and "平台提示" in m.content for m in saved)
    assert [m.content for m in saved if isinstance(m, ToolMessage)] == ["value-1", "value-2", "value-3", "value-4"]
