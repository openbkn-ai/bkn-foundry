# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Keep what the model receives bounded on the public surface.

Three layers, all driven by the merged AgentLimits:

- ToolResultBudget caps each tool result of the current question in the copy
  sent to the model, keeps the most recent results that fit the question's
  result budget and replaces older ones with a placeholder naming the call, and
  reduces earlier questions' results to placeholders. The checkpointed
  ToolMessage keeps the full content, so evidence hashed over the real result
  still matches.
- RepeatedReadCache answers a read call that repeats one already made for the
  current question from that earlier result instead of querying again.
- TurnSummarization (LangChain's SummarizationMiddleware) rewrites earlier
  questions into a summary once the context grows past a token threshold; this
  one does change the saved thread. The current question is never summarized.
"""

import json
from typing import Any

from langchain.agents.middleware import AgentMiddleware, SummarizationMiddleware
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from app.models import AgentLimits

# Schema and discovery results are large but bounded; they get the wider cap.
SCHEMA_TOOLS = frozenset(
    {
        "get_kn_detail",
        "search_schema",
        "get_object_types",
        "get_relation_types",
        "get_action_info",
        "describe_resource",
        "list_resources",
        "search_capabilities",
    }
)

# Calls that only read, so repeating one within a question returns the same data.
READ_TOOLS = SCHEMA_TOOLS | {
    "list_knowledge_networks",
    "search_instance",
    "query_object_instance",
    "query_instance_subgraph",
    "query_metric",
    "drill_through_metric",
    "run_cypher",
    "run_sql",
}

_EARLIER_TURN = (
    "[Tool result from an earlier question omitted. Its answer above already "
    "summarizes it; query again if exact data is needed.]"
)

_SUMMARY_STUB = "[Tool result omitted; the answer that follows summarizes it.]"

_ARGS_CHARS = 300


def _text_of(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            block.get("text", "") if isinstance(block, dict) else str(block)
            for block in content
        )
    return str(content)


def _limit_of(message: ToolMessage, data_chars: int, schema_chars: int) -> int:
    return schema_chars if message.name in SCHEMA_TOOLS else data_chars


def cap_tool_message(
    message: ToolMessage, data_chars: int, schema_chars: int
) -> ToolMessage:
    limit = _limit_of(message, data_chars, schema_chars)
    text = _text_of(message.content)
    if len(text) <= limit:
        return message
    dropped = len(text) - limit
    # Narrower filters would split the rows into pages, and earlier pages are
    # dropped from context before the last one arrives; fewer columns keep them whole.
    note = (
        f"\n\n...[Result truncated: about {dropped} characters omitted; do not treat "
        "this as the complete data. To get every row, query again returning only "
        "the columns you need, in the tool's most compact response format, or "
        "aggregating; do not split the rows into pages.]"
    )
    return message.model_copy(update={"content": text[:limit] + note})


def _call_label(call: dict | None) -> str:
    if not call:
        return "a tool call"
    args = json.dumps(call.get("args", {}), ensure_ascii=False, sort_keys=True)
    if len(args) > _ARGS_CHARS:
        args = args[:_ARGS_CHARS] + "…"
    return f"{call.get('name', 'tool')} {args}"


def cleared_placeholder(call: dict | None, size: int) -> str:
    return (
        f"[Earlier tool result omitted to save context: {_call_label(call)}, about "
        f"{size} characters. Use what you already drew from it; running the identical "
        "call again returns the same data.]"
    )


def budget_messages(
    messages: list, data_chars: int, schema_chars: int, keep: int
) -> list:
    """The copy of messages the model receives.

    The current question keeps its most recent results while their capped size
    fits in keep x data_chars, and always at least keep of them, so one large
    result and several small ones stay visible together. Earlier questions keep
    their answers; their raw tool results would only grow every later turn's
    input, so they become placeholders.
    """
    current = max(
        (i for i, m in enumerate(messages) if isinstance(m, HumanMessage)),
        default=-1,
    )
    calls = {
        call["id"]: call
        for m in messages
        if isinstance(m, AIMessage)
        for call in m.tool_calls
    }
    budget = keep * data_chars
    kept = used = 0
    messages = list(messages)
    for i in range(len(messages) - 1, -1, -1):
        m = messages[i]
        if not isinstance(m, ToolMessage):
            continue
        if i < current:
            messages[i] = m.model_copy(update={"content": _EARLIER_TURN})
            continue
        size = len(_text_of(m.content))
        capped = min(size, _limit_of(m, data_chars, schema_chars))
        if kept < keep or used + capped <= budget:
            kept += 1
            used += capped
            messages[i] = cap_tool_message(m, data_chars, schema_chars)
        else:
            placeholder = cleared_placeholder(calls.get(m.tool_call_id), size)
            messages[i] = m.model_copy(update={"content": placeholder, "artifact": None})
    return messages


class ToolResultBudget(AgentMiddleware):
    """Truncate and drop tool results in the request only (see budget_messages)."""

    def __init__(self, data_chars: int, schema_chars: int, keep: int):
        super().__init__()
        self.data_chars = data_chars
        self.schema_chars = schema_chars
        self.keep = keep

    def _request(self, request):
        return request.override(
            messages=budget_messages(
                request.messages, self.data_chars, self.schema_chars, self.keep
            )
        )

    def wrap_model_call(self, request, handler):
        return handler(self._request(request))

    async def awrap_model_call(self, request, handler):
        return await handler(self._request(request))


def _same_call(a: dict, b: dict) -> bool:
    return a.get("name") == b.get("name") and json.dumps(
        a.get("args", {}), sort_keys=True
    ) == json.dumps(b.get("args", {}), sort_keys=True)


def earlier_result(messages: list, call: dict) -> ToolMessage | None:
    """The successful result of an identical read call made for this question.

    Any other kind of call since that read, a write among them, may have changed
    the data, so the read runs again.
    """
    if call.get("name") not in READ_TOOLS:
        return None
    start = max(
        (i for i, m in enumerate(messages) if isinstance(m, HumanMessage)), default=-1
    )
    turn = messages[start + 1 :]
    ids = {
        c["id"]
        for m in turn
        if isinstance(m, AIMessage)
        for c in m.tool_calls
        if c["id"] != call.get("id") and _same_call(c, call)
    }
    found = next(
        (
            m
            for m in reversed(turn)
            if isinstance(m, ToolMessage) and m.tool_call_id in ids and m.status != "error"
        ),
        None,
    )
    if found is None:
        return None
    # From the step that made the read up to now, including calls run beside it.
    origin = next(
        i
        for i, m in enumerate(turn)
        if isinstance(m, AIMessage)
        and any(c["id"] == found.tool_call_id for c in m.tool_calls)
    )
    for m in turn[origin:]:
        if isinstance(m, AIMessage) and any(
            c["name"] not in READ_TOOLS for c in m.tool_calls
        ):
            return None
    return found


class RepeatedReadCache(AgentMiddleware):
    """Answer a repeated read call from its earlier result in the saved thread."""

    @staticmethod
    def _cached(request) -> ToolMessage | None:
        state = request.state
        messages = state.get("messages", []) if isinstance(state, dict) else []
        found = earlier_result(messages, request.tool_call)
        if found is None:
            return None
        return found.model_copy(update={"tool_call_id": request.tool_call["id"], "id": None})

    def wrap_tool_call(self, request, handler):
        return self._cached(request) or handler(request)

    async def awrap_tool_call(self, request, handler):
        return self._cached(request) or await handler(request)


class TurnSummarization(SummarizationMiddleware):
    """Summarize earlier questions only, never the one being answered.

    LangChain's cutoff may fall inside the current question, which removes the
    question itself from the saved thread. Its summary input must also start at a
    question inside the trim window; with raw tool results in the way none does,
    and the summary degrades to a fixed "too long" line.
    """

    def _determine_cutoff_index(self, messages: list) -> int:
        cutoff = super()._determine_cutoff_index(messages)
        questions = [i for i, m in enumerate(messages) if isinstance(m, HumanMessage)]
        if not questions:
            return 0
        cutoff = min(cutoff, questions[-1])
        # Only an earlier summary before the question: nothing new to fold in.
        earlier = messages[:cutoff]
        if all(m.additional_kwargs.get("lc_source") == "summarization" for m in earlier):
            return 0
        return cutoff

    def _trim_messages_for_summary(self, messages: list) -> list:
        stubbed = [
            m.model_copy(update={"content": _SUMMARY_STUB})
            if isinstance(m, ToolMessage)
            else m
            for m in messages
        ]
        return super()._trim_messages_for_summary(stubbed)


def context_middleware(limits: AgentLimits, summary_model) -> list[AgentMiddleware]:
    """Middleware for one public execution, from the merged limits."""
    return [
        TurnSummarization(
            summary_model,
            trigger=("tokens", limits.summarize_after_tokens),
            keep=("messages", 20),
            trim_tokens_to_summarize=limits.summarize_after_tokens,
        ),
        RepeatedReadCache(),
        ToolResultBudget(
            limits.data_result_chars,
            limits.schema_result_chars,
            limits.keep_tool_results,
        ),
    ]
