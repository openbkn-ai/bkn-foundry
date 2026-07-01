# -*- coding: utf-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from lib.mcp import MCP
from lib.tool_box import ToolBox


class TestOpenbknMcpSmoke:
    def test_get_mcp_list(self, Headers):
        client = MCP()
        status, body = client.GetMCPList({"page": 1, "page_size": 10}, Headers)
        assert status == 200, body


class TestOpenbknFunctionSmoke:
    def test_execute_function_minimal(self, Headers):
        client = ToolBox()
        payload = {
            "code": "def handler(event):\n    return event",
            "event": {"ping": True},
            "timeout": 30000,
        }
        status, body = client.ExecuteFunction(payload, Headers)
        assert status == 200, body
