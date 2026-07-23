"""
Schema inference for nested and composite parameter types.

These are the shapes real tools use — a list of records, a record containing a
list of records — and the ones a flat schema silently loses: the caller is told
"an array" or "an object" with no description of what goes inside.
"""

import sys
from pathlib import Path
from typing import Dict, List, Optional

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[4]))

import sandbox_sdk  # noqa: E402
from sandbox_sdk import tool  # noqa: E402

pydantic = pytest.importorskip("pydantic")


@pytest.fixture(autouse=True)
def clean_registry():
    sandbox_sdk._REGISTRY.clear()
    sandbox_sdk._ENTRY = None
    yield
    sandbox_sdk._REGISTRY.clear()
    sandbox_sdk._ENTRY = None


class Item(pydantic.BaseModel):
    id: str
    qty: int


def only(params, name):
    matches = [p for p in params if p["name"] == name]
    assert len(matches) == 1, f"{name} not found in {[p['name'] for p in params]}"
    return matches[0]


class TestCollectionsOfModels:
    def test_list_of_models_describes_the_element(self):
        """array of object is the most common return shape; elements must be described."""

        @tool
        def f(items: List[Item]) -> dict:
            return {}

        param = f.inputs[0]
        assert param["type"] == "array"
        element = param["sub_parameters"][0]
        assert element["type"] == "object"
        assert {p["name"] for p in element["sub_parameters"]} == {"id", "qty"}

    def test_model_containing_a_list_of_models(self):
        class Order(pydantic.BaseModel):
            no: str
            lines: List[Item]

        @tool
        def f(order: Order) -> dict:
            return {}

        fields = f.inputs[0]["sub_parameters"]
        lines = only(fields, "lines")
        assert lines["type"] == "array"
        element = lines["sub_parameters"][0]
        assert {p["name"] for p in element["sub_parameters"]} == {"id", "qty"}

    def test_three_levels_of_nesting_survive(self):
        class Order(pydantic.BaseModel):
            no: str
            lines: List[Item]

        class Envelope(pydantic.BaseModel):
            order: Order

        @tool
        def f(x: Envelope) -> dict:
            return {}

        order = only(f.inputs[0]["sub_parameters"], "order")
        lines = only(order["sub_parameters"], "lines")
        item = lines["sub_parameters"][0]
        assert only(item["sub_parameters"], "id")["type"] == "string"

    def test_dict_value_type_is_described(self):
        @tool
        def f(m: Dict[str, Item]) -> dict:
            return {}

        param = f.inputs[0]
        assert param["type"] == "object"
        values = param["sub_parameters"][0]
        assert {p["name"] for p in values["sub_parameters"]} == {"id", "qty"}

    def test_bare_dict_stays_permissive(self):
        """Without a value type there is nothing to describe."""

        @tool
        def f(m: dict) -> dict:
            return {}

        assert f.inputs[0]["type"] == "object"
        assert not f.inputs[0].get("sub_parameters")

    def test_nested_lists(self):
        @tool
        def f(m: List[List[int]]) -> dict:
            return {}

        outer = f.inputs[0]
        inner = outer["sub_parameters"][0]
        assert inner["type"] == "array"
        assert inner["sub_parameters"][0]["type"] == "number"


class TestFieldMetadata:
    def test_field_description_and_default_carry_over(self):
        class M(pydantic.BaseModel):
            name: str = pydantic.Field(description="用户名")
            age: int = pydantic.Field(default=18, description="年龄")

        @tool
        def f(m: M) -> dict:
            return {}

        fields = f.inputs[0]["sub_parameters"]
        name = only(fields, "name")
        age = only(fields, "age")
        assert name["description"] == "用户名"
        assert name["required"] is True
        assert age["required"] is False
        assert age["default"] == 18

    def test_optional_model_field_keeps_its_type(self):
        class M(pydantic.BaseModel):
            note: Optional[str] = None

        @tool
        def f(m: M) -> dict:
            return {}

        assert only(f.inputs[0]["sub_parameters"], "note")["type"] == "string"


class TestDispatchWithNestedInput:
    def test_list_of_models_is_built_and_validated(self):
        @tool
        def total(items: List[Item]) -> int:
            return sum(i.qty for i in items)

        event = {"items": [{"id": "a", "qty": 2}, {"id": "b", "qty": 3}]}
        assert sandbox_sdk.dispatch(event) == 5

    def test_nested_model_validation_still_applies(self):
        class Order(pydantic.BaseModel):
            no: str
            lines: List[Item]

        @tool
        def f(order: Order) -> str:
            return order.no

        assert sandbox_sdk.dispatch({"order": {"no": "n1", "lines": []}}) == "n1"

        with pytest.raises(pydantic.ValidationError):
            sandbox_sdk.dispatch({"order": {"no": "n1", "lines": [{"id": "a"}]}})
