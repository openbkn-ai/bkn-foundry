# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from common.get_content import GetContent

class GetCaseAndParams():
    def __init__(self, filename):
        self.filename = filename

    def get_case_and_params(self):
        file = GetContent(self.filename)
        data = file.jsonfile()
        titles = []
        params = []
        for item in data:
            titles.append(item["title"])
            params.append(item["params"])
        return titles, params