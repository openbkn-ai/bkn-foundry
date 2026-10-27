# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Object Metric Designer request and proposal boundaries."""

import asyncio
import json

import pytest
from pydantic import ValidationError

from app import dao
from app.metric_design import MetricDesignRequest, validate_proposal
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
    with pytest.raises(ValueError, match="fixed metric version"):
        validate_proposal(proposal("derived.base_metric_id", "base-1"), derived)
    value = proposal("derived.base_metric_id", "base-1")
    value["changes"].append({"field": "derived.base_metric_version", "after": 2, "reason": "Fixed version"})
    assert len(validate_proposal(value, derived)["changes"]) == 2


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

            async def chat(session, agent, req, account_id, account_type, *, public):
                calls.append(req)
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
