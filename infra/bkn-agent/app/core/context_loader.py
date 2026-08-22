"""Loading the Context Loader MCP surface (ToolRef type=context_loader).

How this differs from the toolbox path, and why this module exists at all:

- The Context Loader MCP JSON-RPC endpoint is mounted only on the **public**
  route, and that route runs `middlewareIntrospectVerify`, which accepts a real
  credential only: an OAuth access token or a bak_ AppKey. It does not honour
  the `/in` style `x-account-id` header identity, so an Authorization header is
  mandatory here.
- Context Loader guards every business tool with a lifecycle check:
  `bkn_context` (conversation_id plus interaction_id) is a **required**
  argument, and omitting it returns `conversation_required`.
- That pair of ids **cannot be minted locally**. In practice a locally minted
  `int_...` is rejected with `resource_not_disclosed`, because the MCP-side
  owner tuple is derived from the token and does not match a self-minted id.
  The correct path is to call `bkn_start_interaction` first for a real pair and
  then use it for the business tools. The ids it returns are bound to the token
  identity rather than to the MCP session, so one handshake per turn is enough
  and the pair can be reused.

There is exactly one source of credentials: the Authorization header forwarded
by the caller, `auth.caller_token()`.

There is deliberately no service-credential fallback. Standing in with a service
AppKey would show Context Loader the issuer of that AppKey rather than the real
caller, collapsing per-user authorization into "whatever the issuer can see" —
a switch that is off by default but exists, and sooner or later somebody flips
it to "make it work". Without a token no tools are mounted, and a warning is
logged rather than an empty tool set returned silently, so the failure stays
where it can be seen."""

import logging
from contextvars import ContextVar
from typing import Any, Optional

from langchain_core.tools import StructuredTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from app import observability
from app.auth import caller_token
from app.commons import locale
from app.config import config

logger = logging.getLogger("bkn-agent.context-loader")

# The lifecycle tools are not business tools: they need no bkn_context and must
# not be exposed to the model, which should not decide when a turn begins.
_LIFECYCLE_TOOLS = {"bkn_start_interaction", "bkn_finish_interaction"}

_CONNECTION_NAME = "context_loader"
# Metadata originating from an MCP server is not a trust boundary.  This token
# is created only by _bind_context and checked by tools.instrument_tool_calls.
_MCP_RECEIPT_TRUST_TOKEN = object()

# The session for this execution. load_tools sets it while loading, and the
# runner or graph takes the real ids out of it for evidence.begin_interaction so
# the evidence chain and Context Loader share one pair of ids; otherwise a
# single conversation splits into two traces.
_current: ContextVar[Optional["ContextLoaderSession"]] = ContextVar(
    "bkn_agent_context_loader_session", default=None
)


def current_session() -> Optional["ContextLoaderSession"]:
    return _current.get()


def set_current(session: Optional["ContextLoaderSession"]):
    return _current.set(session)


def reset_current(token) -> None:
    _current.reset(token)


def wanted(tool_refs: list[dict]) -> bool:
    return any(ref.get("type") == "context_loader" for ref in tool_refs)


def _credential() -> Optional[str]:
    """The raw Authorization header forwarded by the current caller, or None."""
    return caller_token()


# The server groups turns into one managed conversation by this key. Without it
# every turn mints a new one, so a multi-turn conversation is stored as N
# separate sessions holding one interaction each, and they cannot be merged
# afterwards. See adp/context-loader/.../mcp/host_lifecycle_hints.go:22.
_HOST_CONVERSATION_KEY_HEADER = "X-OpenBKN-Host-Conversation-Key"


def _client(
    authorization: str, host_conversation_key: Optional[str] = None
) -> MultiServerMCPClient:
    headers = locale.internal_request_headers(
        {"Authorization": authorization, **observability.outbound_headers()}
    )
    if host_conversation_key:
        headers[_HOST_CONVERSATION_KEY_HEADER] = host_conversation_key
    return MultiServerMCPClient(
        {
            _CONNECTION_NAME: {
                "transport": "streamable_http",
                "url": config.CONTEXT_LOADER_MCP_URL,
                "headers": headers,
                "timeout": config.CONTEXT_LOADER_MCP_TIMEOUT_S,
            }
        }
    )


class ContextLoaderSession:
    """One Context Loader session for the span of a single agent execution.

    It holds the real ids and the loaded tools. `bkn_context` is injected by this
    class at call time and never enters the model-visible parameter table: what
    counts as one interaction is a runtime concern, not the model's decision.
    """

    def __init__(self, conversation_id: str, interaction_id: str):
        self.conversation_id = conversation_id
        self.interaction_id = interaction_id
        # Cleanup runs once early on the normal path and again in the finally,
        # so it has to be idempotent.
        self.closed = False
        self._tools: list[Any] = []
        self._finish: Any = None

    @property
    def bkn_context(self) -> dict[str, str]:
        return {
            "conversation_id": self.conversation_id,
            "interaction_id": self.interaction_id,
        }

    def tools(self, allowed_tools: set[str] | None = None) -> list[Any]:
        """Return the session tools. An allowlist only narrows the set; it changes
        neither the lifecycle nor the context injection."""
        if allowed_tools is None:
            return self._tools
        return [tool for tool in self._tools if getattr(tool, "name", None) in allowed_tools]


def _strip_bkn_context(tool: Any) -> None:
    """Strip bkn_context out of the model-visible parameter table, including the
    required list.

    The MCP side declares it required, and passing that through unchanged invites
    the model to invent ids; the real value is injected by ContextLoaderSession
    at call time.
    """
    schema = getattr(tool, "args_schema", None)
    if not isinstance(schema, dict):
        return
    props = schema.get("properties")
    if isinstance(props, dict):
        props.pop("bkn_context", None)
    required = schema.get("required")
    if isinstance(required, list) and "bkn_context" in required:
        schema["required"] = [r for r in required if r != "bkn_context"]


def _bind_context(tool: Any, session: ContextLoaderSession) -> Any:
    """A thin wrapper that fills in bkn_context before delegating to the original
    tool."""
    inner = getattr(tool, "coroutine", None)
    if inner is None:
        return tool

    async def call(**kwargs):
        kwargs["bkn_context"] = session.bkn_context
        return await inner(**kwargs)

    _strip_bkn_context(tool)
    return StructuredTool.from_function(
        coroutine=call,
        name=tool.name,
        description=tool.description,
        args_schema=tool.args_schema,
        # Context Loader MCP tools return (content, artifact).  Keep that
        # contract through the context-injection wrapper so LangChain emits the
        # same ToolMessage content the evidence receipt was hashed against.
        response_format=getattr(tool, "response_format", "content"),
        metadata={
            **(getattr(tool, "metadata", None) or {}),
            "bkn_context_loader": True,
            "bkn_context_loader_trust_token": _MCP_RECEIPT_TRUST_TOKEN,
        },
    )


async def open_session(
    question: str = "",
    *,
    agent_name: str | None = None,
    host_conversation_key: str | None = None,
) -> Optional[ContextLoaderSession]:
    """Handshake and load the tools. Returns None when the credential is missing
    or the handshake fails, and the caller decides whether that is fatal.

    question is required by bkn_start_interaction and means "what the user asked
    this turn", so pass the real input rather than an empty string: the lifecycle
    service archives the turn under it.

    host_conversation_key anchors multi-turn continuity. chat passes thread_id,
    and the server groups the turns of one thread into a single conversation.
    Without it every turn mints a new one, so a five-turn conversation is stored
    as five unrelated single-turn sessions — persisted data that cannot be
    repaired afterwards.
    """
    authorization = _credential()
    if not authorization:
        logger.warning(
            "[ContextLoader] the caller forwarded no Authorization header; "
            "skipping context_loader tool loading. The Context Loader MCP surface "
            "accepts real credentials only, so a call to bkn-agent must carry the "
            "end user's token"
        )
        return None

    client = _client(authorization, host_conversation_key)
    try:
        tools = await client.get_tools()
    except Exception as e:
        # str() on an ExceptionGroup yields only "unhandled errors in a
        # TaskGroup" while the real cause sits in the sub-exceptions. The first
        # investigation on the VM stalled on exactly this log line (the real
        # cause was a 401), so unpack it.
        detail = "; ".join(f"{type(x).__name__}: {x}" for x in getattr(e, "exceptions", ()) or ()) or f"{type(e).__name__}: {e}"
        logger.warning(
            "[ContextLoader] connecting to the MCP surface failed; "
            "skipping tool loading: %s", detail
        )
        return None

    by_name = {getattr(t, "name", ""): t for t in tools}
    start = by_name.get("bkn_start_interaction")
    if start is None:
        logger.warning(
            "[ContextLoader] the MCP surface does not expose "
            "bkn_start_interaction; skipping tool loading"
        )
        return None

    try:
        # bkn-agent has no local conversation_id to resume. A chat supplies a
        # host key, which the lifecycle service resolves authoritatively; a
        # one-shot execution intentionally starts without a prior conversation.
        args = {"question": question or "(not provided)", "conversation_mode": "new"}
        if agent_name:
            args["agent_name"] = agent_name[:128]
        raw = await start.coroutine(**args)
    except Exception as e:
        logger.warning(
            "[ContextLoader] bkn_start_interaction failed; skipping tool loading: %s", e
        )
        return None

    ids = _parse_ids(raw)
    if not ids:
        logger.warning(
            "[ContextLoader] bkn_start_interaction returned no usable id: %r", raw
        )
        return None

    session = ContextLoaderSession(ids[0], ids[1])
    session._finish = by_name.get("bkn_finish_interaction")
    session._tools = [
        _bind_context(t, session)
        for name, t in by_name.items()
        if name not in _LIFECYCLE_TOOLS
    ]
    logger.info(
        "[ContextLoader] session ready: %d tool(s), interaction %s",
        len(session._tools),
        session.interaction_id,
    )
    return session


def _parse_ids(raw: Any) -> Optional[tuple[str, str]]:
    """Pull (conversation_id, interaction_id) out of the bkn_start_interaction
    result.

    The shape langchain-mcp-adapters actually returns, as measured on the VM, is
    a two-tuple:

        ([{"type": "text", "text": "<json>", "id": ...}],
         {"structured_content": {"conversation_id": ..., "interaction_id": ...}})

    An earlier version accepted only a dict or a bare JSON string and returned
    None as soon as it met the real shape: the handshake had actually succeeded
    and the ids had arrived, yet it was judged as "returned no usable id" and the
    whole tool loading was skipped. Parsing now happens in two stages — look for
    structured_content first, then fall back to JSON inside the text blocks —
    while still accepting the two simple shapes, a dict and a bare string.
    """
    for candidate in _id_candidates(raw):
        cid = candidate.get("conversation_id")
        iid = candidate.get("interaction_id")
        if isinstance(cid, str) and cid and isinstance(iid, str) and iid:
            return cid, iid
    return None


def _id_candidates(raw: Any):
    """Yield every dict that might hide the ids, most trustworthy source
    first."""
    import json

    seen: list[Any] = []

    def walk(node: Any, depth: int = 0) -> None:
        if depth > 4 or node is None:
            return
        if isinstance(node, str):
            try:
                walk(json.loads(node), depth + 1)
            except (TypeError, ValueError):
                pass
            return
        if isinstance(node, dict):
            # structured_content / structuredContent is the MCP structured
            # output, so it wins.
            for key in ("structured_content", "structuredContent"):
                if isinstance(node.get(key), dict):
                    seen.append(node[key])
            seen.append(node)
            if isinstance(node.get("text"), str):
                walk(node["text"], depth + 1)
            return
        if isinstance(node, (list, tuple)):
            for item in node:
                walk(item, depth + 1)

    walk(raw)
    return [c for c in seen if isinstance(c, dict)]


# The outcome enum of bkn_finish_interaction, taken from the MCP input_schema.
# An earlier version passed "succeeded", which is not in the enum, and also put
# the id into bkn_context instead of interaction_id. Together those had every
# cleanup rejected with resource_not_disclosed, leaving all interactions stuck
# in active with nobody closing them.
_OUTCOMES = {"completed", "failed", "cancelled", "handed_off"}


async def close_session(
    session: Optional[ContextLoaderSession],
    *,
    outcome: str = "completed",
    answer: str | None = None,
    reason: str | None = None,
) -> None:
    """Close the turn with bkn_finish_interaction. A failure only warns: the turn
    has already run, and a failed cleanup must not turn a successful result into
    a failed one."""
    if session is None or session._finish is None or session.closed:
        return
    session.closed = True
    if outcome not in _OUTCOMES:
        outcome = "completed"
    args: dict[str, Any] = {
        "interaction_id": session.interaction_id,
        "outcome": outcome,
    }
    if answer:
        # The archived answer is persisted data, so truncation has to leave a
        # mark; otherwise the archive and what the user actually received differ
        # silently.
        args["answer"] = (
            answer if len(answer) <= 4000 else answer[:4000] + "…[truncated by bkn-agent]"
        )
    if reason:
        args["reason"] = reason[:1000]
    try:
        await session._finish.coroutine(**args)
    except Exception as e:
        logger.warning(
            "[ContextLoader] bkn_finish_interaction failed (interaction %s): %s",
            session.interaction_id,
            e,
        )
