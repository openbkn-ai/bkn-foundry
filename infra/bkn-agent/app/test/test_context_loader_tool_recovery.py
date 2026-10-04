# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Real MCP adapter + context binding + agent graph, with deterministic model turns."""

import asyncio
import json

import pytest
from langchain.agents import create_agent
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage, ToolMessage
from langchain_mcp_adapters.tools import convert_mcp_tool_to_langchain_tool
from mcp.types import CallToolResult, TextContent, Tool

from app.core.context_loader import ContextLoaderSession, _bind_context
from app.core.tools import apply_tool_call_cap, instrument_tool_calls


class ScriptedModel(FakeMessagesListChatModel):
    def bind_tools(self, tools, **kwargs):
        return self


class RejectedSession:
    def __init__(self, detail, transport_failure=False):
        self.detail = detail
        self.transport_failure = transport_failure
        self.calls = []

    async def call_tool(self, name, arguments, **kwargs):
        self.calls.append((name, arguments))
        if self.transport_failure:
            raise RuntimeError("MCP transport unavailable")
        return CallToolResult(
            isError=True,
            content=[TextContent(type="text", text=json.dumps({
                "code": "Public.BadRequest", "details": self.detail,
            }))],
        )


def bound_tool(name, session):
    tool = convert_mcp_tool_to_langchain_tool(session, Tool(
        name=name,
        description="Read-only schema tool",
        inputSchema={
            "type": "object",
            "properties": {
                "kn_id": {"type": "string"},
                "skill_id": {"type": "string"},
                "search_scope": {"type": "object"},
                "bkn_context": {"type": "object"}},
            "required": ["kn_id", "bkn_context"],
        },
    ))
    return _bind_context(tool, ContextLoaderSession("conv_test", "int_test"))


@pytest.mark.parametrize("name,args,detail", [
    ("get_skill_content", {"skill_id": "bkn-agent-smart-qa-summary"},
     "This Skill is not mounted on the knowledge network, so it cannot be read or run here."),
    ("search_schema", {"search_scope": {f"include_{kind}_types": False for kind in
        ("action", "metric", "object", "relation")}},
     "search_scope must enable at least one concept type"),
])
def test_mcp_rejection_reaches_model_and_analysis_finishes(name, args, detail):
    session = RejectedSession(detail)
    tool = bound_tool(name, session)
    tools = apply_tool_call_cap(instrument_tool_calls([tool], "acct", "user"), 2)
    conclusion = json.dumps({"decision": "not_evaluable", "summary": detail, "recommendations": []})
    graph = create_agent(ScriptedModel(responses=[
        AIMessage(content="", tool_calls=[{"name": name, "args": {"kn_id": "kn_test", **args}, "id": "call_1"}]),
        AIMessage(content=conclusion),
    ]), tools)
    output = asyncio.run(graph.ainvoke({"messages": [{"role": "user", "content": "Analyze recorded facts"}]}))
    feedback = [m for m in output["messages"] if isinstance(m, ToolMessage)]
    assert len(feedback) == 1
    assert feedback[0].status == "error"
    assert detail in str(feedback[0].content)
    assert json.loads(output["messages"][-1].content)["decision"] == "not_evaluable"
    assert len(session.calls) == 1
    assert session.calls[0][1] == {"kn_id": "kn_test", **args, "bkn_context": {"conversation_id": "conv_test", "interaction_id": "int_test"}}


def test_mcp_transport_failure_still_propagates():
    tool = bound_tool("search_schema", RejectedSession("", transport_failure=True))
    with pytest.raises(RuntimeError, match="MCP transport unavailable"):
        asyncio.run(tool.ainvoke({"type": "tool_call", "name": "search_schema", "args": {"kn_id": "kn_test"}, "id": "call_1"}))


def test_repeated_rejections_are_bounded_by_existing_tool_budget():
    session = RejectedSession("search_scope must enable at least one concept type")
    tool = bound_tool("search_schema", session)
    tools = apply_tool_call_cap(instrument_tool_calls([tool], "acct", "user"), 1)
    args = {"kn_id": "kn_test", "search_scope": {"include_object_types": False}}
    graph = create_agent(ScriptedModel(responses=[
        AIMessage(content="", tool_calls=[{"name": "search_schema", "args": args, "id": "call_1"}]),
        AIMessage(content="", tool_calls=[{"name": "search_schema", "args": args, "id": "call_2"}]),
        AIMessage(content='{"decision":"not_evaluable","summary":"Tool budget exhausted","recommendations":[]}'),
    ]), tools)
    output = asyncio.run(graph.ainvoke({"messages": [{"role": "user", "content": "Analyze"}]}))
    feedback = [m for m in output["messages"] if isinstance(m, ToolMessage)]
    assert feedback[0].status == "error"
    assert "tool call budget exhausted" in str(feedback[1].content)
    assert len(session.calls) == 1
    assert json.loads(output["messages"][-1].content)["decision"] == "not_evaluable"


@pytest.mark.parametrize("scope", [None, {"include_object_types": True, "include_metric_types": False}])
def test_context_binding_preserves_default_and_explicit_valid_scope(scope):
    session = RejectedSession("")

    async def successful_call(name, arguments, **kwargs):
        session.calls.append((name, arguments))
        return CallToolResult(
            content=[TextContent(type="text", text='{"object_types":[]}')],
            structuredContent={"object_types": []},
        )

    session.call_tool = successful_call
    tool = bound_tool("search_schema", session)
    args = {"kn_id": "kn_test"}
    if scope is not None:
        args["search_scope"] = scope
    result = asyncio.run(tool.ainvoke({"type": "tool_call", "name": "search_schema", "args": args, "id": "call_1"}))
    assert result.status == "success"
    assert result.artifact == {"structured_content": {"object_types": []}}
    assert session.calls[0][1] == {**args, "bkn_context": {"conversation_id": "conv_test", "interaction_id": "int_test"}}
