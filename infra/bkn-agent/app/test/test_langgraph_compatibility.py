"""Exercise actual LangGraph execution, not a mocked create_agent adapter."""

import asyncio
import json
from pathlib import Path

from langchain.agents import create_agent
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage
from langchain_core.tools import tool
from langgraph.checkpoint.memory import MemorySaver
from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer


class ToolModel(FakeMessagesListChatModel):
    def bind_tools(self, tools, **kwargs):
        return self


def test_real_tool_loop_preserves_call_id_arguments_and_result():
    @tool
    def lookup_customer(customer_id: str) -> str:
        """Read an authorized customer record."""
        assert customer_id == "customer-7"
        return "customer-7: active"

    model = ToolModel(
        responses=[
            AIMessage(
                content="",
                tool_calls=[
                    {
                        "name": "lookup_customer",
                        "args": {"customer_id": "customer-7"},
                        "id": "call-7",
                        "type": "tool_call",
                    }
                ],
            ),
            AIMessage(content="The customer is active."),
        ]
    )
    graph = create_agent(model, [lookup_customer])

    async def run():
        result = await graph.ainvoke(
            {"messages": [HumanMessage(content="Look up customer 7")]}
        )
        messages = result["messages"]
        tool_result = next(m for m in messages if isinstance(m, ToolMessage))
        assert tool_result.tool_call_id == "call-7"
        assert tool_result.content == "customer-7: active"
        assert messages[-1].content == "The customer is active."

    asyncio.run(run())


def test_checkpoint_serialization_can_restore_and_continue_a_completed_thread():
    async def run():
        original = MemorySaver()
        graph = create_agent(
            ToolModel(responses=[AIMessage(content="First answer")]),
            [],
            checkpointer=original,
        )
        cfg = {"configurable": {"thread_id": "restored-thread"}}
        await graph.ainvoke({"messages": [HumanMessage(content="First question")]}, cfg)
        saved = await original.aget_tuple(cfg)
        assert saved is not None
        serializer = JsonPlusSerializer()
        checkpoint = serializer.loads_typed(serializer.dumps_typed(saved.checkpoint))
        restored = MemorySaver()
        restored_config = {
            "configurable": {
                key: value
                for key, value in saved.config["configurable"].items()
                if key != "checkpoint_id"
            }
        }
        restored.put(
            restored_config, checkpoint, saved.metadata, checkpoint["channel_versions"]
        )
        continuation = create_agent(
            ToolModel(responses=[AIMessage(content="Second answer")]),
            [],
            checkpointer=restored,
        )
        result = await continuation.ainvoke(
            {"messages": [HumanMessage(content="Second question")]}, cfg
        )
        assert [m.content for m in result["messages"]] == [
            "First question",
            "First answer",
            "Second question",
            "Second answer",
        ]

    asyncio.run(run())


def test_checkpoint_captured_with_langgraph_129_continues_on_the_new_baseline():
    fixture = json.loads(
        (
            Path(__file__).resolve().parents[2]
            / "fixtures/langgraph/checkpoint-1.2.9.json"
        ).read_text()
    )
    assert fixture["versions"]["langgraph"] == "1.2.9"
    serializer = JsonPlusSerializer()
    checkpoint = serializer.loads_typed(
        (fixture["serializer_type"], bytes.fromhex(fixture["payload_hex"]))
    )
    old_messages = checkpoint["channel_values"]["messages"]
    old_tool_result = next(m for m in old_messages if isinstance(m, ToolMessage))
    assert old_tool_result.tool_call_id == "legacy-call-7"
    assert old_tool_result.content == "customer-7: active"

    async def run():
        restored = MemorySaver()
        cfg = {"configurable": {"thread_id": "legacy-thread", "checkpoint_ns": ""}}
        restored.put(
            cfg, checkpoint, fixture["metadata"], checkpoint["channel_versions"]
        )
        graph = create_agent(
            ToolModel(responses=[AIMessage(content="Second answer")]),
            [],
            checkpointer=restored,
        )
        result = await graph.ainvoke(
            {"messages": [HumanMessage(content="Second question")]}, cfg
        )
        assert [m.content for m in result["messages"]] == [
            "First question",
            "",
            "customer-7: active",
            "First answer",
            "Second question",
            "Second answer",
        ]

    asyncio.run(run())


def test_streamed_messages_have_model_metadata_and_a_committed_final_state():
    async def run():
        saver = MemorySaver()
        graph = create_agent(
            ToolModel(responses=[AIMessage(content="Streamed answer")]),
            [],
            checkpointer=saver,
        )
        cfg = {"configurable": {"thread_id": "stream-thread"}}
        chunks = [
            item
            async for item in graph.astream(
                {"messages": [HumanMessage(content="Question")]},
                cfg,
                stream_mode="messages",
            )
        ]
        assert chunks
        assert "Streamed answer" in "".join(str(chunk.content) for chunk, _ in chunks)
        assert all(metadata.get("langgraph_node") for _, metadata in chunks)
        state = await graph.aget_state(cfg)
        assert state.values["messages"][-1].content == "Streamed answer"

    asyncio.run(run())
