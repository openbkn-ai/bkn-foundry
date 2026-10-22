# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Built-in tools: tool sets implemented inside bkn-agent, independent of MCP
and the execution factory.

An Agent references a provider with {"type": "builtin", "provider": ...,
"tools": [...]}. A provider is registered here in code and builds its tools
for one execution from that execution's request context, keeping whatever
state the tools share on the run. When the run succeeds, the provider turns
that state into a fragment of the result (for example the graph explorer's
canvas_result), which must pass its own validation before it is persisted.

Run state belongs to one execution: it hangs off the Run bound in a
ContextVar for that request, never off a module global.
"""

import contextvars
import logging
from dataclasses import dataclass, field
from typing import Any, Callable

from pydantic import BaseModel

logger = logging.getLogger("bkn-agent.builtin-tools")

# Marks tools built here. public_chat.guard_tools admits a tool only when it
# carries this exact object, which a tool schema from MCP or a toolbox cannot
# forge.
BUILTIN_TRUST_TOKEN = object()


@dataclass
class Run:
    """One execution as a provider sees it."""

    provider: "Provider"
    kn_id: str
    context: BaseModel
    account_id: str
    account_type: str
    # Whatever the provider's tools share during this execution.
    state: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class Provider:
    name: str
    # Every tool name this provider can build; an Agent may mount a subset.
    tool_names: frozenset[str]
    # The ExecutionScope field this provider reads (for example "canvas") and
    # the model it must validate against.
    context_key: str
    context_model: type[BaseModel]
    # Builds this execution's tools. Called once per run.
    factory: Callable[[Run], list[Any]]
    # The result field the provider fills on success, and how. result() raises
    # ValueError when the state does not make a valid fragment.
    result_key: str
    result: Callable[[Run], Any]


_providers: dict[str, Provider] = {}
_current: contextvars.ContextVar[Run | None] = contextvars.ContextVar(
    "bkn_agent_builtin_run", default=None
)


def register(provider: Provider) -> None:
    if provider.name in _providers:
        raise ValueError(f"builtin provider {provider.name} is already registered")
    _providers[provider.name] = provider


def get(name: str) -> Provider | None:
    return _providers.get(name)


def unknown_reason(ref: dict) -> str | None:
    """Why a builtin reference cannot be loaded, or None when it can."""
    provider = _providers.get(ref.get("provider") or "")
    if provider is None:
        return f"unknown provider {ref.get('provider')!r}"
    extra = sorted(set(ref.get("tools") or ()) - provider.tool_names)
    if extra:
        return f"provider {provider.name} has no tools {extra}"
    return None


def provider_of(tool_refs: list) -> Provider | None:
    """The provider an Agent's builtin reference names; an Agent mounts at most one."""
    names = {
        ref.get("provider")
        for ref in tool_refs
        if isinstance(ref, dict) and ref.get("type") == "builtin"
    }
    if len(names) > 1:
        raise ValueError(f"an Agent can reference one builtin provider, got {sorted(names)}")
    return _providers.get(names.pop()) if names else None


def set_current(run: Run | None) -> None:
    """Bind the run for this request's context. Like the Context Loader session,
    it is set and never reset: it dies with the request's context, while a reset
    from a generator's finally may run in a different context and raise."""
    _current.set(run)


def current() -> Run | None:
    return _current.get()


def tools_for(ref: dict) -> list[Any]:
    """The tools a builtin reference mounts for the current run; none without one."""
    run = _current.get()
    if run is None or run.provider.name != ref.get("provider"):
        logger.warning(
            "[BuiltinTools] no run for provider %s; its tools are not mounted",
            ref.get("provider"),
        )
        return []
    wanted = set(ref.get("tools") or run.provider.tool_names)
    tools = [t for t in run.provider.factory(run) if t.name in wanted]
    for tool in tools:
        tool.metadata = {
            **(tool.metadata or {}),
            "bkn_builtin_provider": run.provider.name,
            "bkn_builtin_trust_token": BUILTIN_TRUST_TOKEN,
        }
    return tools


def definitions(provider: Provider) -> list[dict]:
    """What each tool tells the model — name, description and parameter schema —
    for display. Built from a placeholder run: the factory may only read the run
    when a tool is called, never while building."""
    placeholder = Run(
        provider=provider,
        kn_id="",
        context=provider.context_model.model_construct(),
        account_id="",
        account_type="",
    )
    out = []
    for tool in provider.factory(placeholder):
        schema = (
            tool.args_schema
            if isinstance(tool.args_schema, dict)
            else tool.args_schema.model_json_schema()
        )
        out.append({"name": tool.name, "description": tool.description, "parameters": schema})
    return out


def is_builtin(tool) -> bool:
    return (getattr(tool, "metadata", None) or {}).get("bkn_builtin_trust_token") is BUILTIN_TRUST_TOKEN


# Providers register themselves on import; keep them loaded with the registry so
# a reference is never validated against an empty one.
from app.core import graph_explorer_tools  # noqa: E402,F401
