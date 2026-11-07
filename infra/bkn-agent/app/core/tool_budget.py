# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Tell the model its tool call budget (AgentLimits.max_tool_calls).

apply_tool_call_cap refuses calls once the budget is spent, but the model never
heard how large the budget was, so it spent calls on probing and ran out before
the question was answered. The system prompt now states the budget, and once
few calls are left, each model request ends with a note of how many remain.

The note goes into the request only, never into the saved thread, and tool
results stay as they are: the evidence chain matches them by the hash of their
content.
"""

import math

from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import HumanMessage, ToolMessage

# The note starts once this share of the budget is left, and no later than at
# LOW_MIN calls left.
LOW_SHARE = 0.25
LOW_MIN = 3


def budget_instruction(max_tool_calls: int | None) -> str:
    """The system prompt's line on the budget; nothing without one."""
    if not max_tool_calls or max_tool_calls < 1:
        return ""
    return (
        f"\n\n工具调用额度：本次执行最多调用工具 {max_tool_calls} 次，用完后所有工具调用都会被拒绝。"
        "先想好回答需要哪些数据，能合并的查询合并成一次，同一份数据不要重复查；"
        "额度快用完时会提示还剩几次。"
    )


def low_mark(max_tool_calls: int) -> int:
    """Calls left at which the note starts."""
    return max(LOW_MIN, math.ceil(max_tool_calls * LOW_SHARE))


def calls_this_question(messages: list) -> int:
    """Tool results since the current question, one per call made for it.

    A read the repeated-read cache answered counts too, so the count can run
    ahead of the cap's and the note comes early rather than late.
    """
    start = max(
        (i for i, m in enumerate(messages) if isinstance(m, HumanMessage)), default=-1
    )
    return sum(1 for m in messages[start + 1 :] if isinstance(m, ToolMessage))


def budget_note(max_tool_calls: int, used: int) -> str | None:
    """What the model is told about the calls left; None while plenty are."""
    left = max_tool_calls - used
    if left > low_mark(max_tool_calls):
        return None
    if left <= 0:
        return (
            f"[平台提示，不是用户的话] 本次执行的工具调用额度已用完（共 {max_tool_calls} 次），"
            "不要再调用工具，用已经拿到的信息给出答案；没查到的部分在回答里说明。"
        )
    return (
        f"[平台提示，不是用户的话] 本次执行的工具调用还剩 {left} 次（共 {max_tool_calls} 次）。"
        "先补齐回答最需要的部分，然后给出答案；查不完的部分在回答里说明。"
    )


class ToolBudgetNotice(AgentMiddleware):
    """Ends a model request with the calls left, once few are (see budget_note)."""

    def __init__(self, max_tool_calls: int):
        super().__init__()
        self.max_tool_calls = max_tool_calls

    def _request(self, request):
        note = budget_note(self.max_tool_calls, calls_this_question(request.messages))
        if note is None:
            return request
        return request.override(messages=[*request.messages, HumanMessage(content=note)])

    def wrap_model_call(self, request, handler):
        return handler(self._request(request))

    async def awrap_model_call(self, request, handler):
        return await handler(self._request(request))


def budget_middleware(max_tool_calls: int | None) -> list[AgentMiddleware]:
    """The notice for an agent with a budget; it goes innermost, after any
    middleware that rewrites the messages, so it sees what the model will."""
    if not max_tool_calls or max_tool_calls < 1:
        return []
    return [ToolBudgetNotice(max_tool_calls)]
