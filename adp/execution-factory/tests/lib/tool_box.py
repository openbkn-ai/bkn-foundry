# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.get_content import GetContent
from common.request import Request

class ToolBox():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/agent-operator-integration/v1/tool-box"

    '''Create toolbox.'''
    def CreateToolbox(self, data, headers):
        url = self.base_url
        return Request.post(self, url, data, headers)

    '''Create toolbox (Multipart)'''
    def CreateToolboxMultipart(self, files, data, headers):
        url = self.base_url
        return Request.post_multipart(self, url, files, data, headers)

    '''Update toolbox.'''
    def UpdateToolbox(self, box_id, data, headers):
        url = f"{self.base_url}/{box_id}"
        return Request.post(self, url, data, headers)

    '''Update Toolbox (Multipart)'''
    def UpdateToolboxMultipart(self, box_id, files, data, headers):
        url = f"{self.base_url}/{box_id}"
        return Request.post_multipart(self, url, files, data, headers)

    '''Get toolbox information.'''
    def GetToolbox(self, box_id, headers):
        url = f"{self.base_url}/{box_id}"
        return Request.get(self, url, headers)

    '''Delete toolbox.'''
    def DeleteToolbox(self, box_id, headers):
        url = f"{self.base_url}/{box_id}"
        return Request.pathdelete(self, url, headers)

    '''Get toolbox list.'''
    def GetToolboxList(self, params, headers):
        url = f"{self.base_url}/list"
        return Request.query(self, url, params, headers)

    '''Update toolbox status.'''
    def UpdateToolboxStatus(self, box_id, data, headers):
        url = f"{self.base_url}/{box_id}/status"
        return Request.post(self, url, data, headers)

    '''Create tools.'''
    def CreateTool(self, box_id, data, headers):
        url = f"{self.base_url}/{box_id}/tool"
        return Request.post(self, url, data, headers)

    '''Creation tool (Multipart)'''
    def CreateToolMultipart(self, box_id, files, data, headers):
        url = f"{self.base_url}/{box_id}/tool"
        return Request.post_multipart(self, url, files, data, headers)

    '''Update tool.'''
    def UpdateTool(self, box_id, tool_id, data, headers):
        url = f"{self.base_url}/{box_id}/tool/{tool_id}"
        return Request.post(self, url, data, headers)

    '''Update tool (Multipart)'''
    def UpdateToolMultipart(self, box_id, tool_id, files, data, headers):
        url = f"{self.base_url}/{box_id}/tool/{tool_id}"
        return Request.post_multipart(self, url, files, data, headers)

    '''Get tool information.'''
    def GetTool(self, box_id, tool_id, headers):
        url = f"{self.base_url}/{box_id}/tool/{tool_id}"
        return Request.get(self, url, headers)

    '''Batch removal tool.'''
    def BatchDeleteTools(self, box_id, data, headers):
        url = f"{self.base_url}/{box_id}/tools/batch-delete"
        return Request.post(self, url, data, headers)

    '''Get the list of tools in the toolbox.'''
    def GetBoxToolsList(self, box_id, params, headers):
        url = f"{self.base_url}/{box_id}/tools/list"
        return Request.query(self, url, params, headers)

    '''Update tool status.'''
    def UpdateToolStatus(self, box_id, data, headers):
        url = f"{self.base_url}/{box_id}/tools/status"
        return Request.post(self, url, data, headers)

    '''Get a list of all tools.'''
    def GetMarketToolsList(self, params, headers):
        url = f"{self.base_url}/market/tools"
        return Request.query(self, url, params, headers)

    '''Tool debugging.'''
    def DebugTool(self, box_id, tool_id, data, headers, params=None):
        url = f"{self.base_url}/{box_id}/tool/{tool_id}/debug"
        if params:
            return Request.query_post(self, url, params, data, headers)
        return Request.post(self, url, data, headers)

    '''Tool execution agent API.'''
    def ProxyTool(self, box_id, tool_id, data, headers, params=None):
        url = f"{self.base_url}/{box_id}/proxy/{tool_id}"
        if params:
            return Request.query_post(self, url, params, data, headers)
        return Request.post(self, url, data, headers)

    '''Operators converted into tools.'''
    def ConvertOperatorToTool(self, data, headers):
        url = f"{self.base_url.replace('/tool-box', '/operator/convert/tool')}"
        return Request.post(self, url, data, headers)

    '''Get Toolbox Market Details.'''
    def GetMarketDetail(self, box_id, fields, headers):
        url = f"{self.base_url}/market/{box_id}/{fields}"
        return Request.get(self, url, headers)

    '''Get market toolbox information.'''
    def GetMarketToolbox(self, box_id, headers):
        url = f"{self.base_url}/market/{box_id}"
        return Request.get(self, url, headers)

    '''Get a list of Market Toolboxes.'''
    def GetMarketToolboxList(self, params, headers):
        url = f"{self.base_url}/market"
        return Request.query(self, url, params, headers)

    '''Get code template.'''
    def GetTemplate(self, template_type, headers):
        """
        Get code template.
        According to the latest API documentation: /v1/template/{template_type}
        :param template_type: template type, such as "python".
        :param headers: request headers.
        :return: (status_code, response_data) response contains template_type and code_template.
        """
        url = f"{self.base_url.replace('/tool-box', '/template')}"
        if template_type:
            url = f"{url}/{template_type}"
        return Request.get(self, url, headers)

    '''Execute function.'''
    def ExecuteFunction(self, data, headers):
        """
        Execute function block.
        According to the latest API documentation: /v1/function/execute.
        :param data: request data, including code (string) and event (object)
        :param headers: request headers.
        :return: (status_code, response_data) The response contains stdout, stderr, result, metrics.
        """
        url = f"{self.base_url.replace('/tool-box', '/function/execute')}"
        return Request.post(self, url, data, headers)