# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Tell the model its tool call budget (AgentLimits.max_tool_calls).

apply_tool_call_cap refuses calls once the budget is spent, but the model never
heard how large the budget was, so it spent calls on probing and ran out before
the question was answered. The system prompt now states the budget, and once
few calls are left, each model request ends with a note of how many remain.
The calls left are the cap's own count: a read the repeated-read cache answers
never reaches the cap and does not count.

The note goes into the request only, never into the saved thread, and tool
results stay as they are: the evidence chain matches them by the hash of their
content.
"""

import math

from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import HumanMessage

# The note starts once this share of the budget is left, and no later than at
# LOW_MIN calls left.
LOW_SHARE = 0.25
LOW_MIN = 3


def budget_instruction(max_tool_calls: int | None) -> str:
    """The system prompt's line on the budget; nothing without one."""
    if not max_tool_calls or max_tool_calls < 1:
        return ""
    return (
        f"\n\nTool call budget: this run may call tools at most {max_tool_calls} times; after that"
        " every tool call is refused. Work out the data the answer needs first, combine the"
        " queries that can be combined, and do not query the same data twice. When few calls"
        " are left you will be told how many."
    )


def low_mark(max_tool_calls: int) -> int:
    """Calls left at which the note starts."""
    return max(LOW_MIN, math.ceil(max_tool_calls * LOW_SHARE))


def budget_note(max_tool_calls: int, used: int) -> str | None:
    """What the model is told about the calls left; None while plenty are."""
    left = max_tool_calls - used
    if left > low_mark(max_tool_calls):
        return None
    if left <= 0:
        return (
            f"[Platform note, not from the user] The {max_tool_calls} tool calls of this run are"
            " used up. Do not call tools again: answer with what you have, and say in the answer"
            " what you could not find."
        )
    return (
        f"[Platform note, not from the user] {left} of the {max_tool_calls} tool calls of this"
        " run are left. Get what the answer needs most, then answer, and say in the answer what"
        " you could not look up."
    )


class ToolBudgetNotice(AgentMiddleware):
    """Ends a model request with the calls left, once few are (see budget_note)."""

    def __init__(self, max_tool_calls: int, budget: dict):
        super().__init__()
        self.max_tool_calls = max_tool_calls
        # The cap's counter (apply_tool_call_cap); unfilled, no call has been made.
        self.budget = budget

    def _request(self, request):
        used = self.max_tool_calls - self.budget.get("left", self.max_tool_calls)
        note = budget_note(self.max_tool_calls, used)
        if note is None:
            return request
        return request.override(messages=[*request.messages, HumanMessage(content=note)])

    def wrap_model_call(self, request, handler):
        return handler(self._request(request))

    async def awrap_model_call(self, request, handler):
        return await handler(self._request(request))


def budget_middleware(max_tool_calls: int | None, budget: dict) -> list[AgentMiddleware]:
    """The notice for an agent with a budget, reading the calls left from `budget`,
    the dict passed to apply_tool_call_cap. It goes innermost, after any middleware
    that rewrites the messages, so it sees what the model will."""
    if not max_tool_calls or max_tool_calls < 1:
        return []
    return [ToolBudgetNotice(max_tool_calls, budget)]
