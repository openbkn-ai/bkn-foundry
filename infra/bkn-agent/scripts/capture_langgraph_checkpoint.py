# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Capture an offline compatibility fixture under the selected legacy version.

Run from the service directory in an isolated environment. The fake model and
tool never contact a provider, database or business service.
"""

import asyncio
import importlib.metadata
import json
import sys
from pathlib import Path

from langchain.agents import create_agent
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage, HumanMessage
from langchain_core.tools import tool
from langgraph.checkpoint.memory import MemorySaver


class ToolModel(FakeMessagesListChatModel):
    def bind_tools(self, tools, **kwargs):
        return self


async def capture():
    @tool
    def lookup_customer(customer_id: str) -> str:
        """Read a fixture customer, without accessing business data."""
        return f"{customer_id}: active"

    saver = MemorySaver()
    model = ToolModel(
        responses=[
            AIMessage(
                content="",
                tool_calls=[
                    {
                        "name": "lookup_customer",
                        "args": {"customer_id": "customer-7"},
                        "id": "legacy-call-7",
                        "type": "tool_call",
                    }
                ],
            ),
            AIMessage(content="First answer"),
        ]
    )
    graph = create_agent(model, [lookup_customer], checkpointer=saver)
    config = {"configurable": {"thread_id": "legacy-thread"}}
    await graph.ainvoke({"messages": [HumanMessage(content="First question")]}, config)
    saved = await saver.aget_tuple(config)
    kind, payload = saver.serde.dumps_typed(saved.checkpoint)
    return {
        "format": "langgraph-checkpoint-fixture/v1",
        "versions": {
            name: importlib.metadata.version(name)
            for name in (
                "langgraph",
                "langchain",
                "langchain-core",
                "langgraph-checkpoint",
            )
        },
        "serializer_type": kind,
        "payload_hex": payload.hex(),
        "metadata": saved.metadata,
    }


if __name__ == "__main__":
    output = Path(sys.argv[1])
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(asyncio.run(capture()), indent=2) + "\n")
