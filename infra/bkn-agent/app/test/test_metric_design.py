# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Object Metric Designer request and proposal boundaries."""

import asyncio
import json

import pytest
from jsonschema import ValidationError as JsonSchemaValidationError, validate as validate_json
from langchain_core.messages import AIMessage, HumanMessage
from pydantic import ValidationError

from app import dao
from app.core.structured import structured_extract_with_path
from app.metric_design import (
    MetricDesignRequest, MetricProposalError, model_message, proposal_instructions,
    proposal_schema, repair_proposal, validate_proposal,
)
from app.models import AgentRow, TaskRow
from app.routers import executions
from app.test.test_public_execution import AUTH, environment, parse_sse

URL = "/api/bkn-agent/v1/metric-design/executions"


def request(**overrides):
    data = {
        "client_request_id": "request-1",
        "scope": {
            "kn_id": "network-1",
            "owner_object_type_id": "order",
            "metric_type": "atomic",
            "mode": "create",
        },
        "base_form_revision": "rev-1",
        "form_snapshot": {"name": "Current", "aggregation": "count_rows"},
        "candidates": {
            "properties": [{"name": "amount", "label": "Amount"}],
            "published_metrics": [],
        },
        "message": "Count orders",
    }
    data.update(overrides)
    return MetricDesignRequest.model_validate(data)


def proposal(field="name", after="Proposed"):
    return {
        "summary": "Update the metric",
        "questions": [],
        "changes": [{"field": field, "after": after, "reason": "User requested it"}],
    }


def test_metric_design_scope_accepts_maximum_length_network_id():
    maximum = "n" * 128
    assert request(scope={
        "kn_id": maximum,
        "owner_object_type_id": "order",
        "metric_type": "atomic",
        "mode": "create",
    }).scope.kn_id == maximum
    with pytest.raises(ValidationError):
        request(scope={
            "kn_id": maximum + "x",
            "owner_object_type_id": "order",
            "metric_type": "atomic",
            "mode": "create",
        })


def test_metric_design_accepts_maximum_length_object_type_ids():
    maximum = "o" * 128
    value = request(
        scope={
            "kn_id": "network-1",
            "owner_object_type_id": maximum,
            "metric_type": "derived",
            "mode": "create",
        },
        candidates={
            "properties": [],
            "published_metrics": [{
                "id": "metric-1",
                "version": 1,
                "name": "Metric",
                "type": "atomic",
                "scope": "object_set",
                "object_type_id": maximum,
            }],
        },
    )
    assert value.scope.owner_object_type_id == maximum
    assert value.candidates.published_metrics[0].object_type_id == maximum


@pytest.mark.parametrize("field", ["scope", "candidate"])
def test_metric_design_rejects_overlong_object_type_ids(field):
    overlong = "o" * 129
    overrides = {}
    if field == "scope":
        overrides["scope"] = {
            "kn_id": "network-1",
            "owner_object_type_id": overlong,
            "metric_type": "atomic",
            "mode": "create",
        }
    else:
        overrides["candidates"] = {
            "properties": [],
            "published_metrics": [{
                "id": "metric-1",
                "version": 1,
                "name": "Metric",
                "type": "atomic",
                "scope": "object_set",
                "object_type_id": overlong,
            }],
        }
    with pytest.raises(ValidationError):
        request(**overrides)


def test_proposal_uses_server_captured_before_value_and_revision():
    result = validate_proposal(proposal(), request())
    assert result["kind"] == "metric_design_proposal"
    assert result["base_form_revision"] == "rev-1"
    assert result["changes"] == [{
        "field": "name", "operation": "replace", "before": "Current",
        "after": "Proposed", "reason": "User requested it",
    }]


def test_derived_contract_is_typed_and_excludes_other_metric_types():
    current = request(scope={
        "kn_id": "network-1", "owner_object_type_id": "order",
        "metric_type": "derived", "mode": "create",
    })
    schema = proposal_schema(current)
    validate_json(proposal("name", "分维度订单总金额"), schema)
    for invalid in (
        proposal("name", {"value": "分维度订单总金额"}),
        proposal("code", "order_amount_by_dimension"),
        proposal("composite.expression", {"node_type": "literal", "value": 0}),
        proposal("derived.base_metric_version", True),
        {**proposal(), "changes": [{"field": "name", "value": "分维度订单总金额", "reason": "requested"}]},
    ):
        with pytest.raises(JsonSchemaValidationError):
            validate_json(invalid, schema)
        with pytest.raises(MetricProposalError):
            validate_proposal(invalid, current)
    instructions = proposal_instructions(current)
    assert "field, after, reason" in instructions
    assert "When the user only asks about the name" in instructions
    assert "composite.expression" not in instructions
    with pytest.raises(MetricProposalError, match="value instead of after"):
        validate_proposal({**proposal(), "changes": [{
            "field": "name", "value": "分维度订单总金额", "reason": "requested",
        }]}, current)
    with pytest.raises(MetricProposalError, match="does not match metric type"):
        validate_proposal(proposal("composite.expression", {}), current)


def test_server_filters_cross_type_snapshot_before_the_first_model_call():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"},
        form_snapshot={
            "name": "", "baseMetric": "amount@2", "aggregation": "count_rows",
            "atomic.expression": {"node_type": "literal", "value": 0},
            "composite.dependencies": [],
            "composite.expression": {"node_type": "literal", "value": 0},
            "groupByDimensions": ["user_id"], "code": "server-owned",
        },
    )
    context = json.loads(model_message(current).split("\n", 1)[1])
    assert context["form_snapshot"] == {
        "name": "", "baseMetric": "amount@2", "groupByDimensions": ["user_id"],
    }


def test_one_correction_call_validates_repaired_name_without_other_type_fields():
    current = request(scope={
        "kn_id": "network-1", "owner_object_type_id": "order",
        "metric_type": "derived", "mode": "create",
    }, form_snapshot={"name": ""}, message="为什么没有生成对象指标名称？")

    class Model:
        calls = 0

        async def ainvoke(self, messages):
            self.calls += 1
            assert "composite.expression" not in messages[0].content
            assert "proposal field does not match metric type" in messages[1].content
            return AIMessage(content=json.dumps(proposal("name", "分维度订单总金额"), ensure_ascii=False))

    model = Model()
    result = asyncio.run(repair_proposal(
        model, proposal("composite.expression", {}), current,
        MetricProposalError("proposal field does not match metric type", field="composite.expression"),
    ))
    assert model.calls == 1
    assert [item["field"] for item in result["changes"]] == ["name"]

    class StillInvalid(Model):
        async def ainvoke(self, messages):
            self.calls += 1
            return AIMessage(content=json.dumps(proposal("composite.expression", {})))

    model = StillInvalid()
    with pytest.raises(MetricProposalError) as exc:
        asyncio.run(repair_proposal(
            model, proposal("composite.expression", {}), current,
            MetricProposalError("proposal field does not match metric type", field="composite.expression"),
        ))
    assert model.calls == 1
    assert exc.value.stage == "repair"
    assert exc.value.field == "composite.expression"


def test_creation_requires_an_explicitly_supplied_business_description():
    current = request(
        form_snapshot={"name": "", "description": ""},
        message="创建风险订单数量指标，名称为风险订单数。业务定义：统计当前风险订单对象类中的订单总数。",
    )
    name_only = proposal("name", "风险订单数")
    with pytest.raises(MetricProposalError, match="omitted a business description") as exc:
        validate_proposal(name_only, current)
    assert exc.value.field == "description"

    description_only = proposal("description", "统计当前风险订单对象类中的订单总数。")
    with pytest.raises(MetricProposalError, match="omitted a name") as exc:
        validate_proposal(description_only, current)
    assert exc.value.field == "name"

    complete = proposal("name", "风险订单数")
    complete["changes"].append({
        "field": "description", "after": "统计当前风险订单对象类中的订单总数。",
        "reason": "user supplied a business definition",
    })
    assert len(validate_proposal(complete, current)["changes"]) == 2

    name_only_request = current.model_copy(update={"message": "名称改为风险订单总数"})
    assert len(validate_proposal(name_only, name_only_request)["changes"]) == 1


def test_prefilled_creation_name_still_requires_description_for_business_changes():
    current = request(
        scope={
            "kn_id": "network-1", "owner_object_type_id": "order",
            "metric_type": "derived", "mode": "create",
        },
        form_snapshot={"name": "评测用销售订单指标", "description": ""},
        candidates={
            "properties": [{"name": "order_status", "label": "订单状态"}],
            "published_metrics": [],
        },
        message="只统计已完成订单，允许按订单状态分析。",
    )
    dimensions = proposal("query.analysis_dimensions", ["order_status"])
    with pytest.raises(MetricProposalError, match="omitted a business description") as exc:
        validate_proposal(dimensions, current)
    assert exc.value.field == "description"

    dimensions["changes"].append({
        "field": "description", "after": "统计已完成订单，并允许按订单状态分析。",
        "reason": "document the requested business definition",
    })
    assert len(validate_proposal(dimensions, current)["changes"]) == 2

    preserve_description = current.model_copy(update={
        "message": "只调整订单状态维度，名称和描述保持原样。",
    })
    assert len(validate_proposal(proposal("query.analysis_dimensions", ["order_status"]), preserve_description)["changes"]) == 1


def test_clear_creation_intent_requires_a_name_and_name_only_followup_stays_focused():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"},
        form_snapshot={"name": ""},
        candidates={
            "properties": [{"name": "user_id", "label": "下单用户ID"}],
            "published_metrics": [{
                "id": "amount-total", "version": 2, "name": "订单总金额", "type": "atomic",
                "scope": "object_set", "object_type_id": "order",
            }],
        },
        message="根据下单用户ID统计订单总金额",
    )
    missing_name = proposal("derived.base_metric_id", "amount-total")
    missing_name["changes"].extend([
        {"field": "derived.base_metric_version", "after": 2, "reason": "fixed version"},
        {"field": "query.group_by_dimensions", "after": ["user_id"], "reason": "requested dimension"},
    ])
    with pytest.raises(MetricProposalError, match="omitted a name") as exc:
        validate_proposal(missing_name, current)
    assert exc.value.field == "name"

    followup = current.model_copy(update={"message": "为什么没有生成对象指标名称？"})
    unrelated = proposal("name", "分维度订单总金额")
    unrelated["changes"].append({
        "field": "derived.filters", "after": {
            "node_type": "predicate",
            "left": {"source": "property", "property_ref": {"object_type_id": "order", "property_name": "user_id"}},
            "comparison_operator": "is_not_null",
        }, "reason": "not requested",
    })
    with pytest.raises(MetricProposalError, match="name-only") as exc:
        validate_proposal(unrelated, followup)
    assert exc.value.field == "derived.filters"
    assert [item["field"] for item in validate_proposal(proposal("name", "分维度订单总金额"), followup)["changes"]] == ["name"]


def test_editing_filter_while_preserving_name_is_not_a_name_only_request():
    current = request(
        scope={
            "kn_id": "network-1", "owner_object_type_id": "shipment",
            "metric_type": "derived", "mode": "edit", "metric_id": "shipment-draft",
            "draft_version": 2,
        },
        form_snapshot={"name": "已签收发货数"},
        candidates={
            "properties": [{"name": "shipment_status", "label": "发货状态"}],
            "published_metrics": [],
        },
        message="只把已签收状态 signed 改为运输中 in_transit；保持当前名称、描述和其他配置不变。",
    )
    changed_filter = proposal("derived.filters", {
        "node_type": "predicate",
        "left": {"source": "property", "property_ref": {
            "object_type_id": "shipment", "property_name": "shipment_status",
        }},
        "comparison_operator": "eq",
        "right": [{"source": "literal", "value_type": "string", "value": "in_transit"}],
    })
    assert [item["field"] for item in validate_proposal(changed_filter, current)["changes"]] == ["derived.filters"]


def test_replays_the_incident_through_native_and_fallback_model_stubs():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"},
        form_snapshot={"name": ""},
        message="为什么没有生成对象指标名称？",
    )
    bad_value = {**proposal(), "changes": [{
        "field": "name", "value": "分维度订单总金额", "reason": "requested",
    }]}
    bad_type = proposal("composite.expression", {"node_type": "literal", "value": 0})
    corrected = proposal("name", "分维度订单总金额")

    class Model:
        calls = 0

        def with_structured_output(self, schema):
            class Native:
                async def ainvoke(self, messages):
                    return bad_value
            return Native()

        async def ainvoke(self, messages):
            self.calls += 1
            return AIMessage(content=json.dumps(bad_type if self.calls == 1 else corrected, ensure_ascii=False))

    model = Model()
    output, path = asyncio.run(structured_extract_with_path(
        model,
        [HumanMessage(content=model_message(current)), AIMessage(content=json.dumps(bad_value, ensure_ascii=False))],
        proposal_schema(current),
        proposal_instructions(current),
    ))
    assert path == "fallback"
    assert model.calls == 2
    assert [item["field"] for item in validate_proposal(output, current)["changes"]] == ["name"]


def test_proposal_prefers_field_specific_before_snapshot():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"},
        form_snapshot={"baseMetric": "base-1@2", "derived.base_metric_id": "base-1", "derived.base_metric_version": 2},
        candidates={"properties": [], "published_metrics": [
            {"id": "base-2", "version": 3, "name": "New base", "type": "atomic", "scope": "object_set", "object_type_id": "order"},
        ]},
    )
    value = proposal("derived.base_metric_id", "base-2")
    value["changes"].append({"field": "derived.base_metric_version", "after": 3, "reason": "Fixed version"})
    result = validate_proposal(value, current)
    assert [item["before"] for item in result["changes"]] == ["base-1", 2]


@pytest.mark.parametrize("field", ["code", "lifecycle.status", "metric_id", "logic_property.enabled"])
def test_rejects_non_editable_or_server_owned_fields(field):
    with pytest.raises(ValueError):
        validate_proposal(proposal(field), request())


def test_rejects_wrong_metric_type_and_unavailable_references():
    with pytest.raises(ValueError):
        validate_proposal(proposal("derived.event_time", "amount"), request())
    with pytest.raises(ValueError):
        validate_proposal(proposal("atomic.property_name", "unknown"), request())
    with pytest.raises(ValueError):
        validate_proposal(proposal("atomic.expression", {"property_ref": {"property_name": "unknown"}}), request())


def test_rejects_duplicate_changes():
    value = proposal()
    value["changes"].append(value["changes"][0])
    with pytest.raises(ValueError, match="same field"):
        validate_proposal(value, request())


def test_rejects_invalid_value_types_and_non_fixed_metric_reference():
    with pytest.raises(ValueError):
        validate_proposal(proposal("atomic.aggregation", "nonsense"), request())
    with pytest.raises(ValueError):
        validate_proposal(proposal("name", {"text": "not a string"}), request())
    derived = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"},
        candidates={"properties": [], "published_metrics": [{
            "id": "base-1", "version": 2, "name": "Base", "type": "atomic",
            "scope": "object_set", "object_type_id": "order",
        }]},
    )
    with pytest.raises(ValueError, match="published atomic metric"):
        validate_proposal(proposal("derived.base_metric_id", "base-1"), derived)
    value = proposal("derived.base_metric_id", "base-1")
    value["changes"].append({"field": "derived.base_metric_version", "after": 2, "reason": "Fixed version"})
    assert len(validate_proposal(value, derived)["changes"]) == 2


def test_v3_filter_contract_accepts_real_nodes_and_rejects_alternate_dsl():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "edit",
               "metric_id": "draft-1", "draft_version": 1},
        candidates={"properties": [
            {"name": "order_status", "label": "Order status"},
            {"name": "actual_amount", "label": "Amount"},
        ], "published_metrics": []},
    )
    def predicate(name, operator, right=None):
        node = {"node_type": "predicate", "left": {"source": "property", "property_ref": {
            "object_type_id": "order", "property_name": name,
        }}, "comparison_operator": operator}
        if right is not None:
            node["right"] = [{"source": "literal", "value_type": "string" if isinstance(right, str) else "decimal", "value": right}]
        return node

    valid = {"node_type": "group", "boolean_operator": "not", "children": [{
        "node_type": "group", "boolean_operator": "and", "children": [
            predicate("order_status", "eq", "cancelled"),
            predicate("actual_amount", "gt", 1000),
        ],
    }]}
    assert validate_proposal(proposal("derived.filters", valid), current)["changes"][0]["after"] == valid
    for invalid in (
        {"node_type": "logical", "operator": "and", "operands": []},
        {"node_type": "group", "boolean_operator": "and", "children": [
            predicate("order_status", "eq", "cancelled"),
            {"node_type": "comparison", "field": "actual_amount", "operator": "gt", "value": 1000},
        ]},
        {"node_type": "group", "boolean_operator": "not", "children": [
            predicate("order_status", "eq", "cancelled"), predicate("actual_amount", "gt", 1000),
        ]},
        predicate("order_status", "eq"),
        predicate("unknown", "eq", "cancelled"),
    ):
        with pytest.raises(MetricProposalError) as exc:
            validate_proposal(proposal("derived.filters", invalid), current)
        assert exc.value.field.startswith("derived.filters")


def test_v3_expression_contract_preserves_nested_formula_and_rejects_fake_nodes():
    current = request(candidates={"properties": [
        {"name": "price", "label": "Price"}, {"name": "cost", "label": "Cost"},
        {"name": "quantity", "label": "Quantity"},
    ], "published_metrics": []})
    def prop(name):
        return {"node_type": "property", "property_ref": {"object_type_id": "order", "property_name": name}}

    expression = {"node_type": "binary", "operator": "multiply", "left": {
        "node_type": "call", "function": "greatest", "arguments": [{
            "node_type": "binary", "operator": "subtract", "left": prop("price"), "right": prop("cost"),
        }, {"node_type": "literal", "value_type": "int64", "value": 0}],
    }, "right": prop("quantity")}
    assert validate_proposal(proposal("atomic.expression", expression), current)["changes"][0]["after"] == expression
    for invalid in (
        {"node_type": "property", "property_name": "price"},
        {"node_type": "binary_op", "operator": "multiply", "left": prop("price"), "right": prop("quantity")},
        {"node_type": "binary", "operator": "multiply", "left": prop("price"),
         "right": {"node_type": "property", "property_ref": {"object_type_id": "other", "property_name": "cost"}}},
        {"node_type": "call", "function": "greatest", "arguments": [prop("price")]},
        {"node_type": "call", "function": "round", "arguments": [prop("price"), prop("cost"), prop("quantity")]},
    ):
        with pytest.raises(MetricProposalError) as exc:
            validate_proposal(proposal("atomic.expression", invalid), current)
        assert exc.value.field.startswith("atomic.expression")


def test_currency_result_requires_a_matching_explicit_unit_code():
    current = request(form_snapshot={
        "name": "订单数", "unitCode": "1", "unitSystem": "UCUM", "quantityKind": "count",
    })
    incomplete = proposal("result.quantity_kind", "currency")
    incomplete["changes"].append({"field": "result.unit_system", "after": "ISO_4217", "reason": "currency"})
    with pytest.raises(MetricProposalError) as exc:
        validate_proposal(incomplete, current)
    assert exc.value.field == "result.unit_code"
    incomplete["changes"].append({"field": "result.unit_code", "after": "CNY", "reason": "currency"})
    assert len(validate_proposal(incomplete, current)["changes"]) == 3
    registered = proposal("result.quantity_kind", "currency")
    registered["changes"].extend([
        {"field": "result.unit_system", "after": "registered", "reason": "custom currency"},
        {"field": "result.unit_code", "after": "LOYALTY_CREDIT", "reason": "custom currency"},
    ])
    assert len(validate_proposal(registered, current)["changes"]) == 3


def test_derived_base_rejects_non_atomic_and_other_object_candidates():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "edit",
               "metric_id": "draft-1", "draft_version": 1},
        candidates={"properties": [], "published_metrics": [
            {"id": "derived-base", "version": 1, "name": "Derived", "type": "derived",
             "scope": "object_set", "object_type_id": "order"},
            {"id": "other-base", "version": 1, "name": "Other", "type": "atomic",
             "scope": "object_set", "object_type_id": "other"},
        ]},
    )
    for metric_id in ("derived-base", "other-base"):
        value = proposal("derived.base_metric_id", metric_id)
        value["changes"].append({"field": "derived.base_metric_version", "after": 1, "reason": "fixed"})
        with pytest.raises(MetricProposalError, match="published atomic metric"):
            validate_proposal(value, current)


def test_composite_formula_uses_only_selected_dependency_aliases():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "composite", "mode": "edit",
               "metric_id": "draft-1", "draft_version": 1},
        form_snapshot={"composite.dependencies": [{"alias": "paid", "metric_ref": {"metric_id": "paid-1", "version": 1}}]},
    )
    valid = proposal("composite.expression", {"node_type": "dependency", "dependency_alias": "paid"})
    assert validate_proposal(valid, current)["changes"][0]["after"]["dependency_alias"] == "paid"
    invalid = proposal("composite.expression", {"node_type": "dependency", "dependency_alias": "invented"})
    with pytest.raises(MetricProposalError, match="unavailable dependency alias"):
        validate_proposal(invalid, current)


def test_v3_composite_dependency_requires_alias_in_first_model_schema():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "composite", "mode": "edit",
               "metric_id": "draft-1", "draft_version": 1},
        candidates={"properties": [], "published_metrics": [{
            "id": "paid-1", "version": 1, "name": "Paid", "type": "atomic",
            "scope": "object_set", "object_type_id": "order",
        }]},
    )
    valid = proposal("composite.dependencies", [{"alias": "paid", "metric_id": "paid-1", "version": 1}])
    validate_json(valid, proposal_schema(current))
    assert validate_proposal(valid, current)["changes"][0]["after"][0]["alias"] == "paid"
    missing_alias = proposal("composite.dependencies", [{"metric_id": "paid-1", "version": 1}])
    with pytest.raises(JsonSchemaValidationError):
        validate_json(missing_alias, proposal_schema(current))
    with pytest.raises(MetricProposalError) as exc:
        validate_proposal(missing_alias, current)
    assert exc.value.field == "composite.dependencies"
    assert '"alias"' in proposal_instructions(current)


def test_v3_composite_missing_alias_is_corrected_by_bounded_structured_fallback():
    current = request(
        scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "composite", "mode": "edit",
               "metric_id": "draft-1", "draft_version": 1},
        candidates={"properties": [], "published_metrics": [{
            "id": "paid-1", "version": 1, "name": "Paid", "type": "atomic",
            "scope": "object_set", "object_type_id": "order",
        }]},
    )
    incomplete = proposal("composite.dependencies", [{"metric_id": "paid-1", "version": 1}])
    corrected = proposal("composite.dependencies", [{"alias": "paid", "metric_id": "paid-1", "version": 1}])

    class Model:
        calls = 0

        def with_structured_output(self, schema):
            class Native:
                async def ainvoke(self, messages):
                    return incomplete
            return Native()

        async def ainvoke(self, messages):
            self.calls += 1
            return AIMessage(content=json.dumps(corrected))

    model = Model()
    output, path = asyncio.run(structured_extract_with_path(
        model, [HumanMessage(content=model_message(current)), AIMessage(content=json.dumps(incomplete))],
        proposal_schema(current), proposal_instructions(current),
    ))
    assert path == "fallback"
    assert model.calls == 1
    assert validate_proposal(output, current)["changes"][0]["after"][0]["alias"] == "paid"


def test_create_and_edit_scopes_are_distinct():
    with pytest.raises(ValidationError):
        request(scope={
            "kn_id": "network-1", "owner_object_type_id": "order",
            "metric_type": "atomic", "mode": "edit", "metric_id": "metric-1",
        })
    with pytest.raises(ValidationError):
        request(scope={
            "kn_id": "network-1", "owner_object_type_id": "order",
            "metric_type": "atomic", "mode": "create", "metric_id": "metric-1",
            "draft_version": 1,
        })


def test_large_context_is_rejected_before_model_execution():
    with pytest.raises(ValidationError):
        request(form_snapshot={"description": "x" * 25000})


def test_diagnostic_is_bounded_and_bound_to_the_current_form_revision():
    valid = request(diagnostic={
        "kind": "trial", "form_revision": "rev-1", "status": "partial",
        "warnings": ["Data source returned a partial result"], "entry_count": 3,
        "overall_ms": 42,
    })
    assert valid.diagnostic.status == "partial"
    assert request(diagnostic={"kind": "trial", "form_revision": "rev-1", "status": "forbidden"}).diagnostic.status == "forbidden"
    assert request(diagnostic={"kind": "validation", "form_revision": "rev-1", "status": "failed"}).diagnostic.status == "failed"
    with pytest.raises(ValidationError):
        request(diagnostic={"kind": "trial", "form_revision": "rev-old", "status": "ok"})
    with pytest.raises(ValidationError):
        request(diagnostic={"kind": "trial", "form_revision": "rev-1", "status": "valid"})
    with pytest.raises(ValidationError):
        request(diagnostic={"kind": "trial", "form_revision": "rev-1", "status": "ok", "entries": [{"instance_id": "secret"}]})


async def install_agent(factory, *, tools=None):
    async with factory() as session:
        session.add(AgentRow(
            f_agent_id="object_metric_designer", f_name="MetricDesigner", f_mode="chat",
            f_prompt_id=None, f_tools=tools or [], f_skills=[], f_status="published",
            f_create_user="admin", f_update_user="admin", f_create_time=1, f_update_time=1,
        ))
        await session.commit()


def test_scoped_execution_persists_validated_proposal_and_rejects_cross_scope_resume(monkeypatch):
    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, calls):
            await install_agent(factory)

            async def chat(session, agent, req, account_id, account_type, *, public, system_extension):
                calls.append(req)
                assert "field, after, reason" in system_extension
                assert "\"const\": \"name\"" in json.dumps(req.response_format)
                await dao.touch_thread(session, req.thread_id, agent.agent_id, account_id)
                public.answer.append("I suggest a new name")

                async def events():
                    yield public.event("token", {"content": "I suggest a new name"})
                    yield "event: structured\ndata: " + json.dumps({"content": proposal()}) + "\n\n"
                    yield "event: done\ndata: {}\n\n"

                return events()

            monkeypatch.setattr(executions, "stream_chat", chat)
            body = request().model_dump(mode="json")
            response = await client.post(URL, json=body, headers=AUTH)
            assert response.status_code == 200, response.text
            events = parse_sse(response.text)
            assert [name for name, _ in events] == ["meta", "token", "proposal", "usage", "result", "terminal"]
            assert events[-1][1]["status"] == "succeeded"
            result = events[-2][1]["content"]
            assert result["proposal"]["changes"][0]["before"] == "Current"
            run_id = events[0][1]["run_id"]
            thread_id = events[0][1]["thread_id"]
            recovered = await client.get(f"/api/bkn-agent/v1/executions/{run_id}", headers=AUTH)
            assert recovered.json()["result"]["proposal"] == result["proposal"]
            async with factory() as session:
                task = await session.get(TaskRow, run_id)
                assert task.f_input["protocol"] == "metric-design-v1"
                assert task.f_input["metric_request"]["scope"]["owner_object_type_id"] == "order"
            repeat = await client.post(URL, json=body, headers=AUTH)
            assert parse_sse(repeat.text)[-1][1]["status"] == "succeeded"
            assert len(calls) == 1
            next_turn = {**body, "thread_id": thread_id, "client_request_id": "request-2"}
            assert (await client.post(URL, json=next_turn, headers=AUTH)).status_code == 200
            wrong_scope = {**next_turn, "client_request_id": "request-3", "scope": {**body["scope"], "owner_object_type_id": "customer"}}
            assert (await client.post(URL, json=wrong_scope, headers=AUTH)).status_code == 409
            other_user = {**AUTH, "Authorization": "Bearer other"}
            assert (await client.get(f"/api/bkn-agent/v1/executions/{run_id}", headers=other_user)).status_code == 404

    asyncio.run(run())


def test_tool_enabled_metric_agent_cannot_run(monkeypatch):
    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, calls):
            await install_agent(factory, tools=[{"type": "context_loader"}])
            response = await client.post(URL, json=request().model_dump(mode="json"), headers=AUTH)
            assert response.status_code == 409
            assert not calls

    asyncio.run(run())


def test_simulated_agent_proposes_derived_creation_and_draft_edit(monkeypatch):
    """A deterministic model stand-in exercises both scoped execution modes."""
    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, calls):
            await install_agent(factory)

            async def chat(session, agent, req, account_id, account_type, *, public, system_extension):
                calls.append(req)
                await dao.touch_thread(session, req.thread_id, agent.agent_id, account_id)
                assert "derived.base_metric_version" in system_extension
                assert "composite.expression" not in system_extension
                context = json.loads(req.message.split("\n", 1)[1])
                if context["scope"]["mode"] == "create":
                    value = proposal("name", "分维度订单总金额")
                    value["changes"].extend([
                        {"field": "derived.base_metric_id", "after": "amount-total", "reason": "published base"},
                        {"field": "derived.base_metric_version", "after": 2, "reason": "fixed version"},
                        {"field": "query.group_by_dimensions", "after": [
                            "user_id", "address_id", "channel_id", "order_status", "coupon_id",
                        ], "reason": "five requested dimensions"},
                    ])
                else:
                    value = proposal("name", "按五维统计订单总金额")

                async def events():
                    yield "event: structured\ndata: " + json.dumps({"content": value}, ensure_ascii=False) + "\n\n"
                    yield "event: done\ndata: {}\n\n"
                return events()

            monkeypatch.setattr(executions, "stream_chat", chat)
            scope = {"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"}
            candidates = {
                "properties": [{"name": name, "label": name} for name in (
                    "user_id", "address_id", "channel_id", "order_status", "coupon_id",
                )],
                "published_metrics": [{
                    "id": "amount-total", "version": 2, "name": "订单总金额", "type": "atomic",
                    "scope": "object_set", "object_type_id": "order",
                }],
            }
            first = request(
                scope=scope, candidates=candidates, form_snapshot={"name": ""},
                message="根据不同的维度统计订单总金额，包含下单用户、地址、渠道、状态、优惠券",
            ).model_dump(mode="json")
            response = await client.post(URL, json=first, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "succeeded"
            created = events[-2][1]["content"]["proposal"]
            assert {item["field"]: item["after"] for item in created["changes"]} == {
                "name": "分维度订单总金额", "derived.base_metric_id": "amount-total",
                "derived.base_metric_version": 2,
                "query.group_by_dimensions": ["user_id", "address_id", "channel_id", "order_status", "coupon_id"],
            }
            assert created["changes"][0]["before"] == ""

            edit = request(
                client_request_id="request-edit", scope={
                    **scope, "mode": "edit", "metric_id": "draft-1", "draft_version": 3,
                }, candidates=candidates, form_snapshot={"name": "分维度订单总金额"},
                message="只把名称改成按五维统计订单总金额",
            ).model_dump(mode="json")
            response = await client.post(URL, json=edit, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "succeeded"
            edited = events[-2][1]["content"]["proposal"]
            assert [(item["field"], item["before"], item["after"]) for item in edited["changes"]] == [
                ("name", "分维度订单总金额", "按五维统计订单总金额"),
            ]
            assert len(calls) == 2

    asyncio.run(run())


def test_business_repair_is_bounded_and_failed_run_remains_diagnostic(monkeypatch):
    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, calls):
            await install_agent(factory)

            async def chat(session, agent, req, account_id, account_type, *, public, system_extension):
                calls.append(req)
                await dao.touch_thread(session, req.thread_id, agent.agent_id, account_id)

                async def events():
                    yield "event: structured\ndata: " + json.dumps({
                        "content": proposal("composite.expression", {"node_type": "literal", "value": 0}),
                    }) + "\n\n"
                    yield "event: done\ndata: {}\n\n"
                return events()

            class Model:
                calls = 0
                return_invalid = False

                async def ainvoke(self, messages):
                    self.calls += 1
                    value = (
                        proposal("composite.expression", {"node_type": "literal", "value": 0})
                        if self.return_invalid else proposal("name", "分维度订单总金额")
                    )
                    return AIMessage(content=json.dumps(value, ensure_ascii=False))

            model = Model()
            monkeypatch.setattr(executions, "stream_chat", chat)
            monkeypatch.setattr(executions, "build_chat_model", lambda *args, **kwargs: model)
            body = request(
                scope={"kn_id": "network-1", "owner_object_type_id": "order", "metric_type": "derived", "mode": "create"},
                form_snapshot={"name": ""},
            ).model_dump(mode="json")
            response = await client.post(URL, json=body, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "succeeded"
            assert [item["field"] for item in events[-2][1]["content"]["proposal"]["changes"]] == ["name"]
            assert model.calls == 1

            model.return_invalid = True
            response = await client.post(URL, json={**body, "client_request_id": "request-failed"}, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "failed"
            assert events[-1][1]["error"]["code"] == "BknAgent.MetricDesign.ProposalInvalid"
            assert events[-1][1]["error"]["field"] == "composite.expression"
            assert events[-1][1]["error"]["stage"] == "repair"
            run_id = events[0][1]["run_id"]
            assert events[-1][1]["run_id"] == run_id
            recovered = await client.get(f"/api/bkn-agent/v1/executions/{run_id}", headers=AUTH)
            assert recovered.json()["error"] == events[-1][1]["error"]
            assert recovered.json()["result"] is None
            assert model.calls == 2

    asyncio.run(run())
