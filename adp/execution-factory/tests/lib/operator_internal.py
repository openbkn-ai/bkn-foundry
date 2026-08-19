# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.request import Request

class InternalOperator():
    def __init__(self):
        self.base_url = "http://agent-operator-integration:9000/api/agent-operator-integration/internal-v1/operator"

    '''Get operator classification.'''
    def GetCategory(self, headers):
        url = f"{self.base_url}/category"
        return Request.get(self, url, headers)
    
    '''Create new operator classification.'''
    def CreateCategory(self, data, headers):
        url = f"{self.base_url}/category"
        return Request.post(self, url, data, headers)
    
    '''Update operator classification.'''
    def UpdateCategory(self, category_type, data, headers):
        url = f"{self.base_url}/category/{category_type}"
        return Request.put(self, url, data, headers)
    
    '''Delete operator classification.'''
    def DeleteCategory(self, category_type, headers):
        url = f"{self.base_url}/category/{category_type}"
        return Request.pathdelete(self, url, headers)

    '''Agent execution operator.'''
    def ProxyOperator(self, operator_id, data, headers):
        url = f"{self.base_url}/proxy/{operator_id}"
        return Request.post(self, url, data, headers)