# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.get_content import GetContent
from common.request import Request

class AutomationClient():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/automation/v1"

    '''Create a combinatorial operator.'''
    def CreateCombinationOperator(self, data, headers):
        url = self.base_url + "/operators"
        return Request.post(self, url, data, headers)

    '''Get the list of combinatorial operators.'''
    def GetOperatorsList(self, params, headers):
        url = self.base_url + "/operators"
        return Request.query(self, url, params, headers)

    '''Get combinatorial operator details.'''
    def GetOperatorDetail(self, operator_id, headers):
        url = self.base_url + "/operators/" + operator_id   
        return Request.get(self, url, headers)
    

    '''Update combinatorial operator.'''
    def UpdateOperator(self, operator_id, data, headers):
        url = self.base_url + "/operators/" + operator_id
        result = Request.put(self, url, data, headers=headers)
        print(result)
        # Handling 204 No Content response.
        if result[0] == 204:
            return [204, {}]  # Returns an empty object instead of an empty string.
        return result


    '''Delete combinatorial operator.'''
    def DeleteOperator(self, operator_id, headers):
        url = self.base_url + "/operators/" + operator_id
        return Request.delete(self, url, {}, headers)
    

    

    '''Run combinatorial operators.'''
    def RuneOperator(self, dag_id, data , headers):
        url = self.base_url+"/operators/" + dag_id+"/executions" 
        return Request.post(self, url, data, headers)


    '''Get running records (v2)'''
    def GetDagResultsV2(self, dag_id, params, headers):
        base_url_v2 = self.base_url.replace('/v1', '/v2')
        url = f"{base_url_v2}/dag/{dag_id}/results"
        return Request.query(self, url, params, headers)

    '''Get execution logs (v2)'''
    def GetDagResultLogV2(self, dag_id, result_id, params, headers):
        base_url_v2 = self.base_url.replace('/v1', '/v2')
        url = f"{base_url_v2}/dag/{dag_id}/result/{result_id}"
        return Request.query(self, url, params, headers)
    
    
    '''Process panoramic observable API.'''
    def GetFullView(self, params, headers):
        """
        Get process panoramic statistics.
        Args:
            params (dict): query parameters, including start_time, end_time, optional type.
            headers (dict): request headers.
        Returns:
            (status_code, resp_json)
        """
        url = self.base_url + "/observability/full-view"
        return Request.query(self, url, params, headers)

    '''Process execution observable API.'''
    def GetRunView(self, params, headers):
        """
        Get process running statistics.
        Args:
            params (dict): query parameters, including start_time, end_time, optional type.
            headers (dict): request headers.
        Returns:
            (status_code, resp_json)
        """
        url = self.base_url + "/observability/runtime-view"
        return Request.query(self, url, params, headers)