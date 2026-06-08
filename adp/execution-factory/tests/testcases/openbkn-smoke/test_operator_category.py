# -*- coding: utf-8 -*-

from lib.operator import Operator


class TestOpenbknOperatorSmoke:
    def test_get_operator_category(self, Headers):
        client = Operator()
        status, body = client.GetOperatorCategory(Headers)
        assert status == 200, body
        category_types = [item["category_type"] for item in body]
        assert "other_category" in category_types
