# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Keep what the model receives bounded on the public surface.

Three layers, all driven by the merged AgentLimits:

- ToolResultBudget caps each tool result of the current question in the copy
  sent to the model and reduces earlier questions' results to placeholders. The
  checkpointed ToolMessage keeps the full content, so evidence hashed over the
  real result still matches.
- ContextEditingMiddleware (LangChain) replaces all but the most recent tool
  results with a placeholder, again only in the request.
- SummarizationMiddleware (LangChain) rewrites older history into a summary once
  the context grows past a token threshold; this one does change the saved thread.
"""

from typing import Any

from langchain.agents.middleware import (
    AgentMiddleware,
    ClearToolUsesEdit,
    ContextEditingMiddleware,
    SummarizationMiddleware,
)
from langchain_core.messages import HumanMessage, ToolMessage

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

_EARLIER_TURN = (
    "[Tool result from an earlier question omitted. Its answer above already "
    "summarizes it; query again if exact data is needed.]"
)

_CLEARED = (
    "[Earlier tool result omitted to save context. Query again with narrower "
    "filters if it is still needed.]"
)


def _text_of(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            block.get("text", "") if isinstance(block, dict) else str(block)
            for block in content
        )
    return str(content)


def cap_tool_message(
    message: ToolMessage, data_chars: int, schema_chars: int
) -> ToolMessage:
    limit = schema_chars if message.name in SCHEMA_TOOLS else data_chars
    text = _text_of(message.content)
    if len(text) <= limit:
        return message
    dropped = len(text) - limit
    note = (
        f"\n\n...[Result truncated: about {dropped} characters omitted. Query again "
        "with narrower filters, a smaller LIMIT, and only the fields you need; do not "
        "treat this as the complete data.]"
    )
    return message.model_copy(update={"content": text[:limit] + note})


class ToolResultBudget(AgentMiddleware):
    """Truncate oversized tool results in the request only."""

    def __init__(self, data_chars: int, schema_chars: int):
        super().__init__()
        self.data_chars = data_chars
        self.schema_chars = schema_chars

    def _request(self, request):
        # Earlier questions keep their answers; their raw tool results would only
        # grow every later turn's input, so they become placeholders.
        current = max(
            (i for i, m in enumerate(request.messages) if isinstance(m, HumanMessage)),
            default=-1,
        )
        messages = []
        for i, m in enumerate(request.messages):
            if isinstance(m, ToolMessage):
                m = (
                    m.model_copy(update={"content": _EARLIER_TURN})
                    if i < current
                    else cap_tool_message(m, self.data_chars, self.schema_chars)
                )
            messages.append(m)
        return request.override(messages=messages)

    def wrap_model_call(self, request, handler):
        return handler(self._request(request))

    async def awrap_model_call(self, request, handler):
        return await handler(self._request(request))


def context_middleware(limits: AgentLimits, summary_model) -> list[AgentMiddleware]:
    """Middleware for one public execution, from the merged limits."""
    return [
        SummarizationMiddleware(
            summary_model,
            trigger=("tokens", limits.summarize_after_tokens),
            keep=("messages", 20),
        ),
        ContextEditingMiddleware(
            edits=[
                ClearToolUsesEdit(
                    trigger=0, keep=limits.keep_tool_results, placeholder=_CLEARED
                )
            ]
        ),
        ToolResultBudget(limits.data_result_chars, limits.schema_result_chars),
    ]
