# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Merge the shared Agent with its network and user layers for one execution.

Order: shared definition -> network setting -> user setting. A later layer only
appends instructions and narrows tools and limits; nothing it stores can widen
what the layer above allows, so values beyond the cap are clamped here rather
than rejected when saved (the cap may change after a layer was written).
"""

from dataclasses import dataclass, field
from typing import Optional

from app.config import config
from app.core.tools import _context_loader_allowed_tools
from app.models import AgentLimits, AgentOut, AgentSetting

_LIMIT_FIELDS = (
    "max_turns",
    "max_tool_calls",
    "timeout_s",
    "max_output_tokens",
    "keep_tool_results",
    "data_result_chars",
    "schema_result_chars",
    "summarize_after_tokens",
)

# Context budget defaults. A question keeps at least keep_tool_results results
# and as many more as fit in keep_tool_results x data_result_chars. 12000 data
# characters hold a ~130-row listing in one result; at 8000 such a listing was
# truncated and the model paged through it, losing pages as later ones arrived.
DEFAULT_KEEP_TOOL_RESULTS = 3
DEFAULT_DATA_RESULT_CHARS = 12000
DEFAULT_SCHEMA_RESULT_CHARS = 24000
DEFAULT_SUMMARIZE_AFTER_TOKENS = 60000


@dataclass
class EffectiveSettings:
    agent: AgentOut
    # (layer, text) in the order they are appended to the system prompt.
    instructions: list[tuple[str, str]] = field(default_factory=list)
    # None means every tool the shared definition loads.
    allowed_tools: Optional[set[str]] = None
    versions: dict[str, int] = field(default_factory=dict)


def shared_limits(agent: AgentOut) -> AgentLimits:
    """The shared caps with the service defaults filled in where unset."""
    limits = agent.limits or AgentLimits()
    return limits.model_copy(
        update={
            "max_turns": limits.max_turns or config.DEFAULT_MAX_TURNS,
            "timeout_s": limits.timeout_s or config.DEFAULT_TIMEOUT_S,
            "keep_tool_results": limits.keep_tool_results or DEFAULT_KEEP_TOOL_RESULTS,
            "data_result_chars": limits.data_result_chars or DEFAULT_DATA_RESULT_CHARS,
            "schema_result_chars": limits.schema_result_chars or DEFAULT_SCHEMA_RESULT_CHARS,
            "summarize_after_tokens": limits.summarize_after_tokens
            or DEFAULT_SUMMARIZE_AFTER_TOKENS,
        }
    )


def _narrow_limits(cap: AgentLimits, layer: Optional[AgentLimits]) -> AgentLimits:
    if layer is None:
        return cap
    values = {}
    for name in _LIMIT_FIELDS:
        upper, wanted = getattr(cap, name), getattr(layer, name)
        if wanted is None:
            values[name] = upper
        elif upper is None:
            values[name] = wanted
        else:
            values[name] = min(upper, wanted)
    return AgentLimits(**values)


def _narrow_tools(
    allowed: Optional[set[str]], layer: Optional[list[str]]
) -> Optional[set[str]]:
    if layer is None:
        return allowed
    chosen = set(layer)
    return chosen if allowed is None else allowed & chosen


def merge(
    agent: AgentOut,
    network: Optional[AgentSetting],
    network_version: int,
    user: Optional[AgentSetting],
    user_version: int,
) -> EffectiveSettings:
    limits = shared_limits(agent)
    # The shared allow-list narrows Context Loader tools only; without such a
    # reference there is nothing for it to narrow. Stored elements that are not
    # references are ignored here; load_tools reports them at execution time.
    refs = [ref for ref in agent.tools if isinstance(ref, dict)]
    allowed = (
        _context_loader_allowed_tools(refs)
        if any(ref.get("type") == "context_loader" for ref in refs)
        else None
    )
    instructions: list[tuple[str, str]] = []
    for name, layer in (("network", network), ("user", user)):
        if layer is None:
            continue
        limits = _narrow_limits(limits, layer.limits)
        allowed = _narrow_tools(allowed, layer.allowed_tools)
        if layer.instruction and layer.instruction.strip():
            instructions.append((name, layer.instruction.strip()))
    return EffectiveSettings(
        agent=agent.model_copy(update={"limits": limits}),
        instructions=instructions,
        allowed_tools=allowed,
        versions={"network": network_version, "user": user_version},
    )


_HEADINGS = {
    "network": "Instructions for this knowledge network",
    "user": "User preferences",
}


def instruction_block(instructions: list[tuple[str, str]]) -> str:
    """Appended after the shared Prompt; later layers cannot override it."""
    sections = [
        f"\n\n## {_HEADINGS[name]}\n"
        "These add to the instructions above and cannot override them.\n"
        f"{text}"
        for name, text in instructions
    ]
    return "".join(sections)
