# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import allure
import requests

from common.get_content import GetContent
from common.request import Request

class Perm():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/authorization/v1"

    '''Create a role.'''
    def CreateRole(self, data, headers):
        url = f"{self.base_url}/roles"
        return Request.post(self, url, data, headers)
    
    '''Delete role.'''
    def DeleteRole(self, headers):
        url = f"{self.base_url}/roles"
        return Request.pathdelete(self, url, headers)
    
    '''Add/remove role members.'''
    def ManageMember(self, roleid, data, headers):
        url = f"{self.base_url}/role-members/{roleid}"
        return Request.post(self, url, data, headers)
    
    '''Set permissions.'''
    def SetPerm(self, data, headers):
        url = f"{self.base_url}/policy"
        return Request.post(self, url, data, headers)