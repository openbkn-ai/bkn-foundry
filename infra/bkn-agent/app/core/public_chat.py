# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Public protocol projection and network guard around the existing chat loop."""

import json
import re
import time
from dataclasses import dataclass, field
from typing import Any

from langchain_core.messages import AIMessage, ToolMessage
from langchain_core.tools import StructuredTool, ToolException

# These dispatch arbitrary code or nested calls; an outer kn_id cannot bind the
# operations they perform. Register them with MCP, but refuse execution here.
_INDIRECT_TOOLS = {
    "run_code",
    "run_shell",
    "execute_tool",
    "execute_skill",
    "execute_native_tool",
}


@dataclass
class PublicChat:
    run_id: str
    kn_id: str
    sequence: int = 0
    prompt_version: int | None = None
    answer: list[str] = field(default_factory=list)
    usage: dict = field(default_factory=dict)
    tool_starts: dict[str, float] = field(default_factory=dict)

    def event(self, name: str, data: dict) -> str:
        self.sequence += 1
        payload = {**data, "run_id": self.run_id, "sequence": self.sequence}
        return f"event: {name}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"

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
                                {"error": project(message.content)}
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

            # Bind closure state outside model-controlled keyword arguments.
            def bind(inner, supports_scope):
                async def call(**kwargs):
                    if not supports_scope:
                        raise ToolException(
                            "This tool cannot enforce the selected knowledge network; it is unavailable in this execution."
                        )
                    if kwargs.get("kn_id", self.kn_id) != self.kn_id:
                        raise ToolException(
                            "The tool requested a different knowledge network."
                        )
                    kwargs["kn_id"] = self.kn_id
                    return await inner(**kwargs)

                return call

            call = bind(tool.coroutine, scoped)
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


_SENSITIVE = {
    "authorization",
    "token",
    "access_token",
    "api_key",
    "password",
    "secret",
    "bkn_context",
}


def project(value: Any) -> Any:
    """Bound the complete display projection; never alter the tool's real result."""
    result = _project(value)
    return (
        result
        if len(json.dumps(result, ensure_ascii=False)) <= 16000
        else {"truncated": True}
    )


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
