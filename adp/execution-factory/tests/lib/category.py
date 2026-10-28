# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.get_content import GetContent
from common.request import Request


class Category():
    '''Categories shared by toolboxes, MCP servers and skills.'''
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/agent-operator-integration/v1/category"

    '''Get category list.'''
    def GetCategory(self, headers):
        return Request.get(self, self.base_url, headers)


class InternalCategory():
    def __init__(self):
        self.base_url = "http://agent-operator-integration:9000/api/agent-operator-integration/internal-v1/category"

    '''Get category list.'''
    def GetCategory(self, headers):
        return Request.get(self, self.base_url, headers)

    '''Create category.'''
    def CreateCategory(self, data, headers):
        return Request.post(self, self.base_url, data, headers)

    '''Update category.'''
    def UpdateCategory(self, category_type, data, headers):
        url = f"{self.base_url}/{category_type}"
        return Request.put(self, url, data, headers)

    '''Delete category.'''
    def DeleteCategory(self, category_type, headers):
        url = f"{self.base_url}/{category_type}"
        return Request.pathdelete(self, url, headers)
