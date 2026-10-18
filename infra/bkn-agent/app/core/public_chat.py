# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Public protocol projection and network guard around the existing chat loop."""

import json
import re
import time
from dataclasses import dataclass, field
from typing import Any

from langchain_core.messages import AIMessage, SystemMessage, ToolMessage
from langchain_core.tools import StructuredTool, ToolException

from app.commons.i18n import build_error_content

# Appended to the system prompt for the closing call when the tool loop ran
# out of steps or ended without an answer.
_FINALIZE_INSTRUCTION = (
    "The tool-step limit was reached or no final answer was produced. Using only "
    "the information already obtained, answer the user now in the conversation's "
    "language without calling tools. State plainly what remains unknown."
)

# These dispatch arbitrary code or nested calls; an outer kn_id cannot bind the
# operations they perform. The public surface does not offer them to the model
# until they can be taken over with an enforced network scope; offering them
# only produced calls that were refused, wasting steps.
_INDIRECT_TOOLS = {
    "run_code",
    "run_shell",
    "execute_tool",
    "execute_skill",
    "execute_native_tool",
}

REASONING_FLUSH_CHARS = 200
REASONING_FLUSH_S = 0.3


@dataclass
class PublicChat:
    run_id: str
    kn_id: str
    sequence: int = 0
    prompt_version: int | None = None
    answer: list[str] = field(default_factory=list)
    # The last assistant message without tool calls. Text spoken before a tool
    # call is progress narration and streams as tokens, but is not the answer.
    final: str | None = None
    # Network and user layers: appended instructions and the narrowed tool set
    # (None keeps every tool the shared definition loads).
    instructions: list[tuple[str, str]] = field(default_factory=list)
    allowed_tools: set[str] | None = None
    usage: dict = field(default_factory=dict)
    tool_starts: dict[str, float] = field(default_factory=dict)
    # Reasoning arrives a token or two per chunk; it is sent in pieces instead.
    reasoning_parts: list[str] = field(default_factory=list)
    reasoning_since: float = 0.0

    def event(self, name: str, data: dict) -> str:
        # Any other event first sends the reasoning held so far, so order holds.
        pending = self._flush_reasoning()
        self.sequence += 1
        payload = {**data, "run_id": self.run_id, "sequence": self.sequence}
        return pending + f"event: {name}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"

    def reasoning(self, text: str) -> str:
        """Buffer reasoning; return an event once enough text or time has gathered."""
        if not self.reasoning_parts:
            self.reasoning_since = time.monotonic()
        self.reasoning_parts.append(text)
        if (
            sum(map(len, self.reasoning_parts)) < REASONING_FLUSH_CHARS
            and time.monotonic() - self.reasoning_since < REASONING_FLUSH_S
        ):
            return ""
        return self._flush_reasoning()

    def _flush_reasoning(self) -> str:
        if not self.reasoning_parts:
            return ""
        content = "".join(self.reasoning_parts)
        self.reasoning_parts.clear()
        self.sequence += 1
        payload = {"content": content, "run_id": self.run_id, "sequence": self.sequence}
        return f"event: reasoning\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"

    @staticmethod
    def text(content) -> str:
        if isinstance(content, str):
            return content
        return "".join(
            block.get("text", "")
            for block in content
            if isinstance(block, dict) and block.get("type", "text") == "text"
        )

    @staticmethod
    def interrupted_messages(messages: list) -> list[ToolMessage]:
        # A cancelled tools node may have saved assistant calls without their
        # results. Complete the protocol history, never replay those calls.
        pending = {}
        for message in messages:
            if isinstance(message, AIMessage):
                pending.update({call["id"]: call for call in message.tool_calls})
            elif isinstance(message, ToolMessage):
                pending.pop(message.tool_call_id, None)
        return [
            ToolMessage(
                content="The previous execution was interrupted. This tool's outcome is unavailable; do not treat it as a successful result.",
                tool_call_id=call["id"],
                name=call["name"],
                status="error",
            )
            for call in pending.values()
        ]

    async def finalize(self, graph, cfg: dict, model, system_prompt: str):
        """Close a turn that has no answer with one tool-free model call.

        The browser loop this replaces did the same when steps ran out. The
        answer is saved to the thread so the next turn sees it.
        """
        state = await graph.aget_state(cfg)
        messages = state.values.get("messages", [])
        # A step limit can stop right after the model asked for tools.
        fills = self.interrupted_messages(messages)
        final = None
        async for chunk in model.astream(
            [
                SystemMessage(content=f"{system_prompt}\n\n{_FINALIZE_INSTRUCTION}"),
                *messages,
                *fills,
            ]
        ):
            final = chunk if final is None else final + chunk
            content = self.text(chunk.content)
            if content:
                yield content
        if final is not None:
            if final.usage_metadata:
                for key in ("input_tokens", "output_tokens", "total_tokens"):
                    value = final.usage_metadata.get(key)
                    if isinstance(value, int):
                        self.usage[key] = self.usage.get(key, 0) + value
            answer = self.text(final.content)
            self.final = answer
            await graph.aupdate_state(
                cfg, {"messages": [*fills, AIMessage(content=answer)]}
            )

    def updates(self, update: dict):
        for state in update.values():
            if not isinstance(state, dict):
                continue
            for message in state.get("messages", []):
                if isinstance(message, AIMessage):
                    if message.usage_metadata:
                        for key in ("input_tokens", "output_tokens", "total_tokens"):
                            value = message.usage_metadata.get(key)
                            if isinstance(value, int):
                                self.usage[key] = self.usage.get(key, 0) + value
                    self.final = (
                        None if message.tool_calls else self.text(message.content)
                    )
                    for call in message.tool_calls:
                        self.tool_starts[call["id"]] = time.monotonic()
                        yield self.event(
                            "tool_started",
                            {
                                "tool_call_id": call["id"],
                                "name": call["name"],
                                "input": project(call["args"]),
                            },
                        )
                elif isinstance(message, ToolMessage):
                    yield self.event(
                        "tool_finished",
                        {
                            "tool_call_id": message.tool_call_id,
                            "name": message.name,
                            "status": "failed"
                            if message.status == "error"
                            else "succeeded",
                            "output": project(message.content),
                            "duration_ms": round(
                                (
                                    time.monotonic()
                                    - self.tool_starts.pop(
                                        message.tool_call_id, time.monotonic()
                                    )
                                )
                                * 1000
                            ),
                            **(
                                {
                                    "error": project(message.content),
                                    # unavailable: the call did not go through;
                                    # error: the tool ran and returned an error.
                                    "error_kind": "unavailable"
                                    if _TOOL_UNAVAILABLE in _text_of(message.content)
                                    else "error",
                                }
                                if message.status == "error"
                                else {}
                            ),
                        },
                    )

    def guard_tools(self, tools: list) -> list:
        guarded = []
        for tool in tools:
            # Only the Context Loader wrapper carries this unforgeable local
            # receipt marker; an arbitrary MCP/toolbox schema is not a boundary.
            from app.core.context_loader import _MCP_RECEIPT_TRUST_TOKEN

            metadata = tool.metadata or {}
            schema = (
                tool.args_schema
                if isinstance(tool.args_schema, dict)
                else tool.args_schema.model_json_schema()
            )
            scoped = (
                tool.name.lower() not in _INDIRECT_TOOLS
                and metadata.get("bkn_context_loader_trust_token")
                is _MCP_RECEIPT_TRUST_TOKEN
                and "kn_id" in schema.get("properties", {})
            )
            if not scoped:
                continue
            if self.allowed_tools is not None and tool.name not in self.allowed_tools:
                continue

            # Bind closure state outside model-controlled keyword arguments.
            def bind(inner):
                async def call(**kwargs):
                    if kwargs.get("kn_id", self.kn_id) != self.kn_id:
                        raise ToolException(
                            json.dumps(
                                build_error_content(
                                    "BknAgent.Execution.ToolScopeMismatch"
                                ),
                                ensure_ascii=False,
                            )
                        )
                    kwargs["kn_id"] = self.kn_id
                    try:
                        return await inner(**kwargs)
                    except ToolException:
                        raise
                    except Exception as exc:
                        # The call itself did not go through (unreachable, timed
                        # out, or a result that fails its own schema). The model
                        # can work around it, so the turn goes on, but the event
                        # reports it apart from errors the tool itself returned.
                        raise ToolException(
                            json.dumps(
                                {
                                    **build_error_content("BknAgent.Tool.Unavailable"),
                                    "cause": _redact(f"{type(exc).__name__}: {exc}")[
                                        :500
                                    ],
                                },
                                ensure_ascii=False,
                            )
                        ) from exc

                return call

            call = bind(tool.coroutine)
            guarded.append(
                StructuredTool.from_function(
                    coroutine=call,
                    name=tool.name,
                    description=tool.description,
                    args_schema=tool.args_schema,
                    response_format=tool.response_format,
                    metadata=metadata,
                    handle_tool_error=True,
                )
            )
        return guarded


def classify_failure(exc: BaseException) -> tuple[str, dict | None]:
    """A stable code for a failed public turn, plus the upstream's own error.

    Model Factory already returns user-facing error codes and messages; passing
    them on tells the caller what actually failed. Credentials are removed and
    the text is bounded; stack traces and addresses are never included.
    """
    import openai

    if isinstance(exc, openai.APIConnectionError):
        return "BknAgent.Model.Unavailable", None
    if isinstance(exc, openai.APIStatusError):
        body = exc.body if isinstance(exc.body, dict) else {}
        error = body.get("error") if isinstance(body.get("error"), dict) else body
        upstream = {
            key: _redact(str(error[key]))[:500]
            for key in ("code", "message")
            if isinstance(error, dict) and error.get(key)
        } or None
        retryable = exc.status_code >= 500 or exc.status_code == 429
        return (
            "BknAgent.Model.Unavailable" if retryable else "BknAgent.Model.Rejected",
            upstream,
        )
    return "BknAgent.Chat.Failed", None


def _redact(text: str) -> str:
    return re.sub(
        r"(?i)bearer\s+[^\s\"']+|bak_[A-Za-z0-9_-]+|sk-[A-Za-z0-9_-]+",
        "[redacted]",
        text,
    )


_TOOL_UNAVAILABLE = '"code": "BknAgent.Tool.Unavailable"'


def _text_of(content) -> str:
    if isinstance(content, str):
        return content
    return "".join(
        block.get("text", "") if isinstance(block, dict) else str(block)
        for block in content or []
    )


_SENSITIVE = {
    "authorization",
    "token",
    "access_token",
    "api_key",
    "password",
    "secret",
    "bkn_context",
}


_PROJECTION_LIMIT = 16000


def project(value: Any) -> Any:
    """Bound the complete display projection; never alter the tool's real result."""
    result = _project(value)
    text = json.dumps(result, ensure_ascii=False)
    if len(text) <= _PROJECTION_LIMIT:
        return result
    # Keep a readable prefix: a bare marker left the client nothing to show
    # for any sizeable query result.
    return text[:_PROJECTION_LIMIT] + "…[truncated]"


def _project(value: Any, depth: int = 0) -> Any:
    if depth > 6:
        return {"truncated": True}
    if isinstance(value, dict):
        result = {
            str(k): "[redacted]"
            if str(k).lower() in _SENSITIVE
            else _project(v, depth + 1)
            for k, v in list(value.items())[:100]
        }
        if len(value) > 100:
            result["truncated"] = True
        return result
    if isinstance(value, (list, tuple)):
        return [_project(item, depth + 1) for item in value[:100]] + (
            [{"truncated": True}] if len(value) > 100 else []
        )
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except (ValueError, TypeError):
            # Unstructured text can contain credentials; report only bounded
            # content after removing common token forms.
            value = re.sub(
                r"(?i)bearer\s+[^\s\"']+|bak_[A-Za-z0-9_-]+", "[redacted]", value
            )
            return value[:8000] + ("…[truncated]" if len(value) > 8000 else "")
        return _project(parsed, depth + 1)
    return (
        value
        if value is None or isinstance(value, (int, float, bool))
        else str(value)[:1000]
    )
