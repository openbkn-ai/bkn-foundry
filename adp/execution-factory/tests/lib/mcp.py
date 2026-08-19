# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import allure
import requests

from common.get_content import GetContent
from common.request import Request

class MCP():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/agent-operator-integration/v1/mcp"

    '''Parsing SSE MCPServer.'''
    def ParseSSE(self, data, headers):
        url = f"{self.base_url}/parse/sse"
        # Use the method with timeout and retry, timeout 60 seconds, retry up to 2 times.
        return Request.post_with_retry(self, url, data, headers, timeout=60, max_retries=2)

    '''Add MCP Server configuration.'''
    def RegisterMCP(self, data, headers):
        url = f"{self.base_url}"
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Body")
        resp = requests.post(url, json=data, headers=headers, verify=False)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")
        # print(resp.status_code, resp.text)

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            return [resp.status_code, resp.json()]

    '''Delete MCP Server configuration.'''
    def DeleteMCP(self, mcp_id, headers):
        url = f"{self.base_url}/{mcp_id}"
        return Request.delete(self, url, None, headers)

    '''Get MCP Server list.'''
    def GetMCPList(self, params, headers):
        url = f"{self.base_url}/list"
        return Request.query(self, url, params, headers)

    '''Get MCP Server details.'''
    def GetMCPDetail(self, mcp_id, headers):
        url = f"{self.base_url}/{mcp_id}"
        return Request.get(self, url, headers)

    '''Edit MCP Server configuration.'''
    def EditMCP(self, mcp_id, data, headers):
        url = f"{self.base_url}/{mcp_id}"
        return Request.put(self, url, data, headers)

    '''MCP service release operation.'''
    def MCPReleaseAction(self, mcp_id, data, headers):
        url = f"{self.base_url}/{ mcp_id}/status"
        return Request.post(self, url, data, headers)

    '''MCP tool debugging.'''
    def MCPToolDebug(self, mcp_id, name, data, headers):
        url = f"{self.base_url}/{mcp_id}/tool/{name}/debug"
        # Use the method with timeout and retry, timeout 60 seconds, retry up to 2 times.
        return Request.post_with_retry(self, url, data, headers, timeout=60, max_retries=2)

    '''Get a list of published MCPs.'''
    def GetMCPMarketList(self, params, headers):
        url = f"{self.base_url}/market/list"
        return Request.query(self, url, params, headers)

    '''Get published MCP service market details.'''
    def GetMCPMarketDetail(self, mcp_id, headers):
        url = f"{self.base_url}/market/{mcp_id}"
        return Request.get(self, url, headers)

    '''Get the tool list under the specified MCP service.'''
    def GetMCPToolList(self, mcp_id, headers):
        url = f"{self.base_url}/proxy/{mcp_id}/tools"
        # Use the method with timeout and retry, timeout 60 seconds, retry up to 2 times.
        return Request.get_with_retry(self, url, headers, timeout=60, max_retries=2)

    '''Call the tool under the specified MCP service.'''
    def CallMCPtool(self, mcp_id, data, headers):
        url = f"{self.base_url}/proxy/{mcp_id}/tool/call"
        # Use the method with timeout and retry, timeout 60 seconds, retry up to 2 times.
        return Request.post_with_retry(self, url, data, headers, timeout=60, max_retries=2)

    '''Obtain published MCP service market details in batches.'''
    def BatchGetMCPMarketDetail(self, mcp_ids, fields, headers):
        url = f"{self.base_url}/market/batch/{mcp_ids}/{fields}"
        return Request.get(self, url, headers)