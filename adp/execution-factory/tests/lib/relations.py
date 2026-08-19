# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.get_content import GetContent
from common.request import Request

class Relations():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/agent-operator-integration/internal-v1/relations"

    '''Delete relationship.'''
    def DeleteRelation(self, id, headers):
        url = f"{self.base_url}/{id}"
        return Request.delete(self, url, headers)

    '''Get relationship details.'''
    def GetRelation(self, id, headers):
        url = f"{self.base_url}/{id}"
        return Request.get(self, url, headers)

    '''Delete relationships in batches.'''
    def BatchDeleteRelations(self, data, headers):
        url = f"{self.base_url}/delete"
        return Request.post(self, url, data, headers)

    '''Query relationships based on source resources.'''
    def GetRelationsBySource(self, headers, params):
        url = f"{self.base_url}/source"
        return Request.get(self, url, headers, params)

    '''Query the relationship based on the target resource.'''
    def GetRelationsByTarget(self, headers, params):
        url = f"{self.base_url}/target"
        return Request.get(self, url, headers, params) 