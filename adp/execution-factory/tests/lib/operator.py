# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.get_content import GetContent
from common.request import Request

class Operator():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/agent-operator-integration/v1/operator"

    '''Registered operator.'''
    def RegisterOperator(self, data, headers):
        url = self.base_url + "/register"

        return Request.post(self, url, data, headers)

    '''Registered operator (Multipart)'''
    def RegisterOperatorMultipart(self, files, data, headers):
        url = self.base_url + "/register"
        return Request.post_multipart(self, url, files, data, headers)

    '''Get operator list.'''
    def GetOperatorList(self, params, headers):
        url = self.base_url + "/info/list"
        return Request.query(self, url, params, headers)

    '''Get operator information.'''
    def GetOperatorInfo(self, operator_id, headers):
        url = self.base_url + "/info/" + operator_id
        return Request.get(self, url, headers)

    '''Edit operator.'''
    def EditOperator(self, data, headers):
        url = self.base_url + "/info"

        return Request.post(self, url, data, headers)

    '''Edit operator (Multipart)'''
    def EditOperatorMultipart(self, files, data, headers):
        url = self.base_url + "/info"
        return Request.post_multipart(self, url, files, data, headers)

    '''Get operator classification.'''
    def GetOperatorCategory(self, headers):
        url = self.base_url + "/category"

        return Request.get(self, url, headers)

    '''delete operator.'''
    def DeleteOperator(self, data, headers):
        url = self.base_url + "/delete"

        return Request.delete(self, url, data, headers)

    '''Update operator status.'''
    def UpdateOperatorStatus(self, data, headers):
        url = self.base_url + "/status"

        return Request.post(self, url, data, headers)
    
    '''Update operator information.'''
    def UpdateOperatorInfo(self, data, headers):
        url = self.base_url + "/info/update"

        return Request.post(self, url, data, headers)

    '''Operator debugging.'''
    def OperatorDebug(self, data, headers):
        url = self.base_url + "/debug"

        return Request.post(self, url, data, headers)

    '''Get operator historical version details.'''
    def GetOperatorHistoryDetail(self, operator_id, version, headers, tag=None):
        url = self.base_url + f"/history/{operator_id}/{version}"
        params = {}
        if tag is not None:
            params["tag"] = tag
        return Request.query(self, url, params, headers)

    '''Get a list of operator historical versions.'''
    def GetOperatorHistoryList(self, operator_id, headers):
        url = self.base_url + f"/history/{operator_id}"
        return Request.get(self, url, headers)

    '''Get list of operator markets.'''
    def GetOperatorMarketList(self, params, headers):
        url = self.base_url + "/market"
        return Request.query(self, url, params, headers)

    '''Get the details of the designated operator in the operator market.'''
    def GetOperatorMarketDetail(self, operator_id, headers):
        url = self.base_url + f"/market/{operator_id}"
        return Request.get(self, url, headers)
