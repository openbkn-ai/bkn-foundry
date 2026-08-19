# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import os

from common.get_content import GetContent
from common.request import Request

class Impex():
    def __init__(self):
        file = GetContent("./config/env.ini")
        self.config = file.config()
        self.base_url = self.config["requests"]["protocol"] + "://" + self.config["server"]["host"] + ":" + self.config["server"]["port"] + "/api/agent-operator-integration/v1/impex"

    '''Export.'''
    def export(self, component_type, component_id, headers):
        url = f"{self.base_url}/export/{component_type}/{component_id}"
        return Request.get(self, url, headers)


    '''Import from file path (automatically handles file opening and closing)'''
    def import_from_file(self, type, file_path, data, headers):
        """
        Unified import portal: completely simulates the sequence and structure of WebKit messages.
        1. The data part comes first, including filename and Content-Type.
        2. The mode part comes after and does not include Content-Type.
        """
        form_data = data.copy() if data else {}
        mode = form_data.pop("mode", "create")
        
        with open(file_path, "rb") as f:
            # Use a list of tuples to ensure order: data first, mode last.
            files = [
                ("data", (os.path.basename(file_path), f, "application/octet-stream")),
                ("mode", (None, mode))
            ]
            return self.importation(type, files, {}, headers)

    '''Import underlying calls.'''
    def importation(self, type, files, data, headers, params=None):
        url = f"{self.base_url}/import/{type}"
        return Request.post_multipart(self, url, files, data, headers, params=params)