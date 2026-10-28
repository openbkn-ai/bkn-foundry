# -*- coding: utf-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from lib.category import Category


class TestOpenbknCategorySmoke:
    def test_get_category(self, Headers):
        client = Category()
        status, body = client.GetCategory(Headers)
        assert status == 200, body
        category_types = [item["category_type"] for item in body]
        assert "other_category" in category_types
