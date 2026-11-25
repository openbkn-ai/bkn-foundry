# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""The graph explorer's metric tools against a stand-in Context Loader.

The stand-in answers query_metric the way ontology-query does for what the tools
use: group_by over the metric's dimensions, a time-series metric one bucket per
month, order_by time refused for any other, pages by cursor, and a drilled level
asked for with the query of the level above and an opaque key from its answer.
"""

import asyncio
import json
from datetime import datetime
from types import SimpleNamespace

import pytest
from langchain_core.tools import ToolException

from app.core import builtin_tools
from app.core import graph_explorer_metrics as gm
from app.core import graph_explorer_tools as graph

TZ = gm.TZ
REGION = {"object_type_id": "orders", "property_name": "region"}
PRODUCT = {"object_type_id": "orders", "property_name": "product"}
LEVEL = {"object_type_id": "customers", "property_name": "level", "relation_path_alias": "buyer"}
ORDER_DATE = {"object_type_id": "orders", "property_name": "order_date"}


def _ms(y, m, d=1):
    return int(datetime(y, m, d, tzinfo=TZ).timestamp() * 1000)


ORDERS = [
    {"key_id": 1, "region": "华东", "product": "A", "level": "金", "amount": 50, "at": _ms(2026, 2, 3)},
    {"key_id": 2, "region": "华东", "product": "A", "level": "银", "amount": 30, "at": _ms(2026, 3, 9)},
    {"key_id": 3, "region": "华东", "product": "B", "level": "金", "amount": 20, "at": _ms(2026, 3, 20)},
    {"key_id": 4, "region": "华南", "product": "A", "level": "金", "amount": 40, "at": _ms(2026, 3, 1)},
    {"key_id": 5, "region": "华南", "product": "C", "level": "银", "amount": 5, "at": _ms(2026, 2, 28)},
]
FIELDS = {"region": "region", "product": "product", "level": "level"}

METRICS = [
    {
        "id": "sales", "version": 2, "name": "销售额", "comment": "已完成订单的金额合计", "unit": "CNY",
        "owner_object_type_id": "orders",
        "analysis_dimensions": [REGION, PRODUCT, LEVEL], "time_dimensions": [ORDER_DATE],
    },
    {
        "id": "monthly", "version": 1, "name": "月销售额", "owner_object_type_id": "orders",
        "analysis_dimensions": [REGION, PRODUCT], "time_dimensions": [ORDER_DATE],
    },
]

DETAIL = {
    "object_types": [
        {"id": "orders", "name": "订单", "primary_keys": ["key_id"], "related_metric_count": 2},
        {"id": "customers", "name": "客户", "primary_keys": ["key_id"]},
    ],
    "relation_types": [],
}


def _types(kwargs):
    entries = {
        "orders": {
            "id": "orders", "name": "订单", "primary_keys": ["key_id"],
            "data_properties": [{"name": "key_id", "type": "integer"}, {"name": "region", "type": "string"}],
            "related_metrics": METRICS,
        },
        "customers": {"id": "customers", "name": "客户", "primary_keys": ["key_id"], "data_properties": []},
    }
    return {"object_types": [entries[i] for i in kwargs.get("ids") or [] if i in entries]}


def _month(at):
    start = datetime.fromtimestamp(at / 1000, TZ).replace(day=1, hour=0, minute=0, second=0, microsecond=0)
    after = start.replace(year=start.year + (start.month == 12), month=start.month % 12 + 1)
    return int(start.timestamp() * 1000), int(after.timestamp() * 1000) - 1


def _name(ref):
    return FIELDS[ref["property_name"]]


def _buckets(metric_id, query):
    """The rows of a level, each with its key: the rows of the bucket picked above."""
    rows = ORDERS
    if query.get("time"):
        rows = [r for r in rows if query["time"]["start"] <= r["at"] <= query["time"]["end"]]
    selection = query.get("bucket_selection")
    if selection:
        parent = _buckets(metric_id, selection["parent_query"])
        picked = next((b for b in parent if b["bucket_key"] == selection["bucket_key"]), None)
        if picked is None:
            raise ValueError("bucket_key does not belong to the parent query")
        rows = [r for r in rows if r["key_id"] in picked["members"]]
    dims = query.get("group_by") or []
    groups: dict = {}
    for row in rows:
        period = _month(row["at"]) if metric_id == "monthly" else None
        cells = tuple(row[_name(d)] for d in dims)
        groups.setdefault((period, cells), []).append(row)
    out = []
    for (period, cells), members in groups.items():
        entry = {
            "dimension_cells": [{"ref": d, "value": v} for d, v in zip(dims, cells)],
            "value": sum(m["amount"] for m in members),
            "status": "ok",
            "bucket_key": json.dumps([selection and selection["bucket_key"], period, cells], ensure_ascii=False),
            "available_actions": ["drill", "participants"],
            "members": [m["key_id"] for m in members],
        }
        if period:
            entry["time"] = period[0]
            entry["time_bucket"] = {"start": period[0], "end": period[1]}
        out.append(entry)
    for term in reversed(query.get("order_by") or []):
        if term["field"] == "time":
            out.sort(key=lambda e: e["time"], reverse=term["direction"] == "desc")
        else:
            out.sort(key=lambda e: e["value"], reverse=term["direction"] == "desc")
    return out


def _query_metric(kwargs):
    query = kwargs["query"]
    if kwargs["metric_id"] != "monthly" and any(t["field"] == "time" for t in query.get("order_by") or []):
        return {"error": "order_by time requires a time-granular result"}
    entries = _buckets(kwargs["metric_id"], query)
    start = int(query.get("cursor") or 0)
    size = query.get("page_size") or len(entries)
    page = entries[start : start + size]
    answer = {
        "model": {
            "metric_id": kwargs["metric_id"], "version": kwargs["version"],
            "output_shape": "time_series" if kwargs["metric_id"] == "monthly" else "grouped",
        },
        "entries": [{k: v for k, v in e.items() if k != "members"} for e in page],
        "status": "ok",
        "total_count": len(entries),
    }
    if start + size < len(entries):
        answer["next_cursor"] = str(start + size)
    return answer


def _drill_through(kwargs):
    bucket = next(b for b in _buckets(kwargs["metric_id"], kwargs["query"]) if b["bucket_key"] == kwargs["bucket_key"])
    people = [{"instance_identity": {"key_id": k}, "role": "owner"} for k in bucket["members"]]
    return {"participants": people + [{"instance_identity": {"key_id": 99}, "role": "buyer"}], "status": "ok"}


def _rows(kwargs):
    wanted = set(kwargs.get("condition", {}).get("value") or [])
    return {"datas": [{**r, "_display": f"订单{r['key_id']}"} for r in ORDERS if r["key_id"] in wanted]}


def _tool(name, calls, answer):
    async def coroutine(**kwargs):
        calls.append((name, kwargs))
        return json.dumps(answer(kwargs) if callable(answer) else answer, ensure_ascii=False), None

    return SimpleNamespace(name=name, coroutine=coroutine)


def _run(analysis=True, detail=DETAIL, types=_types, max_nodes=None, cards=None, asked_from=None):
    context = {"canvas_id": "c1", "revision": 1}
    if analysis:
        context["analysis_cards"] = cards or []
    if asked_from is not None:
        context["analysis_from"] = asked_from
    if max_nodes:
        context["limits"] = {"max_nodes": max_nodes}
    run = builtin_tools.Run(
        provider=builtin_tools.get("graph_explorer"),
        kn_id="network-1",
        context=graph.CanvasContext(**context),
        account_id="user-1",
        account_type="user",
    )
    calls: list = []
    run.state["context_loader"] = {
        name: _tool(name, calls, answer)
        for name, answer in {
            "get_kn_detail": detail,
            "get_object_types": types,
            "query_metric": _query_metric,
            "drill_through_metric": _drill_through,
            "query_object_instance": _rows,
            "query_instance_subgraph": {"entries": []},
        }.items()
    }
    return run, {t.name: t for t in graph._tools(run)}, calls


def _invoke(tool, **arguments):
    return json.loads(asyncio.run(tool.ainvoke(arguments)))


def _metric_calls(calls):
    return [c[1] for c in calls if c[0] == "query_metric"]


def test_the_instructions_list_published_metrics_with_their_dimensions():
    run, _, calls = _run()
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert (
        "- sales v2 销售额 — counts orders (订单); unit CNY; split by: region, product, customers.level@buyer;"
        " time: order_date; 已完成订单的金额合计"
    ) in text
    assert "- monthly v1 月销售额 — counts orders (订单); split by: region, product; time: order_date" in text
    assert "do not work the same figure out again with run_cypher" in text
    assert "Call show_metric_participants only when the user asks which instances make up a figure" in text
    assert "each query_metric becomes a metric card" in text
    # The definitions read for the schema's properties carry the metrics: no call of their own.
    assert [c[0] for c in calls] == ["get_kn_detail", "get_object_types"]
    assert calls[1][1]["ids"] == ["orders", "customers"]


def test_outside_an_analysis_the_figures_stay_in_the_answer():
    run, _, _ = _run(analysis=False)
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "Here the figures of a metric are in your answer only" in text
    assert "becomes a metric card" not in text


def test_a_network_without_metrics_gets_no_metric_section():
    detail = {**DETAIL, "object_types": [{**t, "related_metric_count": 0} for t in DETAIL["object_types"]]}
    run, _, _ = _run(detail=detail)
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "Published metrics" not in text and "- orders (订单)" in text


def test_a_failed_metric_lookup_leaves_the_schema():
    # Properties come with the detail, so the definitions are read for the metrics alone, and fail.
    detail = {
        **DETAIL,
        "object_types": [{**t, "properties": [{"name": "key_id", "type": "integer"}]} for t in DETAIL["object_types"]],
    }
    run, _, _ = _run(detail=detail, types={"error": "bkn-backend unavailable"})
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "Published metrics" not in text and "- orders (订单)" in text and "key_id:integer" in text


def test_metrics_of_object_types_past_the_bound_are_said_to_be_unlisted(monkeypatch):
    monkeypatch.setattr(graph, "METRIC_TYPES", 1)
    detail = {**DETAIL, "object_types": [{**t, "related_metric_count": 2} for t in DETAIL["object_types"]]}
    run, _, _ = _run(detail=detail)
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "- sales v2 销售额" in text
    assert "(Metrics of 1 more object types are not listed, and query_metric cannot read them;" in text


def test_past_sixty_metrics_only_names_are_listed():
    many = [gm.Metric(id=f"m{i}", version=1, name=f"指标{i}", owner="orders", dimensions=[REGION]) for i in range(61)]
    lines = gm.catalog(many, {"orders": "订单"})
    assert lines[0] == "- m0 v1 指标0 — counts orders (订单)"
    assert "only their names are listed" in lines[-1]


def test_a_metric_splits_by_its_dimensions_and_keeps_the_query_for_the_card():
    run, tools, calls = _run()
    out = _invoke(tools["query_metric"], metric="sales", group_by=["region"])
    assert out["split_by"] == ["region"] and out["unit"] == "CNY"
    assert out["rows"] == [
        {"region": "华东", "value": "100", "drill": True, "participants": True},
        {"region": "华南", "value": "45", "drill": True, "participants": True},
    ]
    assert out["can_split_further_by"] == ["product", "customers.level@buyer"]
    [asked] = _metric_calls(calls)
    assert asked["version"] == 2 and asked["query"] == {
        "group_by": [REGION], "order_by": [{"field": "value", "direction": "desc"}], "page_size": 200,
    }
    progress = builtin_tools.get("graph_explorer").progress(run)
    assert progress["metrics"] == [
        {
            "tool": "query_metric",
            "input": {"metric": "sales", "group_by": ["region"]},
            "metric_id": "sales", "version": 2, "owner_object_type_id": "orders", "group_by": [REGION],
        }
    ]
    # Nothing is planned for the canvas.
    assert progress["actions"] == [] and builtin_tools.get("graph_explorer").result(run)["actions"] == []


def test_a_drill_goes_down_level_by_level_by_bucket_values():
    run, tools, calls = _run()
    out = _invoke(
        tools["query_metric"],
        metric="sales",
        group_by=["region"],
        drill=[{"bucket": {"region": "华东"}, "by": "product"}, {"bucket": {"product": "A"}, "by": "customers.level"}],
    )
    assert out["split_by"] == ["region", "product", "customers.level@buyer"]
    assert [(r["customers.level@buyer"], r["value"]) for r in out["rows"]] == [("金", "50"), ("银", "30")]
    asked = _metric_calls(calls)
    assert len(asked) == 3
    # Each level is asked with the query above it and the key its answer gave the bucket.
    second, third = asked[1]["query"], asked[2]["query"]
    assert second["bucket_selection"]["parent_query"] == asked[0]["query"]
    assert third["bucket_selection"]["parent_query"] == second
    assert third["group_by"] == [REGION, PRODUCT, LEVEL]
    [step] = builtin_tools.get("graph_explorer").progress(run)["metrics"]
    assert step["drill"] == [{"cells": ["华东"], "by": PRODUCT}, {"cells": ["华东", "A"], "by": LEVEL}]


def test_a_total_can_be_drilled_into():
    _, tools, _ = _run()
    out = _invoke(tools["query_metric"], metric="sales", drill=[{"by": "region"}])
    assert [r["value"] for r in out["rows"]] == ["100", "45"]


def test_a_metric_read_by_period_names_periods_and_drills_into_one():
    run, tools, calls = _run()
    out = _invoke(tools["query_metric"], metric="monthly", group_by=["region"])
    assert out["split_by"] == ["period", "region"]
    assert [(r["period"], r["region"], r["value"]) for r in out["rows"]] == [
        ("2026-02", "华东", "50"), ("2026-02", "华南", "5"), ("2026-03", "华东", "50"), ("2026-03", "华南", "40"),
    ]
    # Asked once more, by time, once the answer says it is read by period.
    first, second = _metric_calls(calls)
    assert "order_by" in first["query"] and first["query"]["order_by"] == [{"field": "value", "direction": "desc"}]
    assert second["query"]["order_by"] == [{"field": "time", "direction": "desc"}, {"field": "value", "direction": "desc"}]
    builtin_tools.get("graph_explorer").progress(run)

    calls.clear()
    out = _invoke(
        tools["query_metric"], metric="monthly", group_by=["region"],
        drill=[{"bucket": {"region": "华东"}, "period": "2026-03", "by": "product"}], chart="bar",
    )
    assert [(r["period"], r["product"], r["value"]) for r in out["rows"]] == [("2026-03", "A", "30"), ("2026-03", "B", "20")]
    # The run remembers the metric is read by period.
    assert len(_metric_calls(calls)) == 2
    [step] = builtin_tools.get("graph_explorer").progress(run)["metrics"]
    start, end = _month(_ms(2026, 3))
    assert step["drill"] == [{"cells": [(start + end) // 2, "华东"], "by": PRODUCT}]
    assert step["chart"] == "bar"


def _card_reads(text, no):
    [line] = [line for line in text.splitlines() if line.startswith(f"- #{no} ")]
    return json.loads(line.split("; reads query_metric ", 1)[1]) if "; reads query_metric " in line else None


def test_a_later_question_goes_on_from_what_a_metric_card_reads():
    # A question reads a metric by period, drilled into one period, within some days.
    run, tools, _ = _run()
    _invoke(
        tools["query_metric"], metric="monthly", group_by=["region"],
        drill=[{"bucket": {"region": "华东"}, "period": "2026-03", "by": "product"}],
        time={"start": "2026-01-01", "end": "2026-03-31"},
    )
    [step] = builtin_tools.get("graph_explorer").progress(run)["metrics"]
    card = {k: step[k] for k in ("metric_id", "version", "owner_object_type_id", "group_by", "drill", "time")}

    # The next question is a run of its own: it knows the card by what the page sends of it.
    cards = [
        {"no": 1, "title": "华东订单", "kind": "query"},
        {"no": 2, "title": "月销售额", "kind": "metric", "metric": {**card, "time_grain": "month"}},
    ]
    run, tools, _ = _run(cards=cards, asked_from=2)
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "- #1 华东订单 (query)" in text.splitlines() and "The user asks this question from card #2." in text
    assert "give those arguments and add to them" in text
    reads = _card_reads(text, 2)
    assert reads == {
        "metric": "monthly", "version": 1, "group_by": ["region"],
        "drill": [{"period": "2026-03", "bucket": {"region": "华东"}, "by": "product"}],
        "time": {"start": "2026-01-01", "end": "2026-03-31"},
    }
    # Given back as it is listed, it reads the card's level again.
    _invoke(tools["query_metric"], **reads)
    [again] = builtin_tools.get("graph_explorer").progress(run)["metrics"]
    assert {k: again[k] for k in card} == card


def test_a_metric_card_lists_dimensions_as_the_tools_take_them():
    card = {
        "metric_id": "sales", "version": 2, "owner_object_type_id": "orders", "group_by": [REGION],
        "drill": [{"cells": ["华东"], "by": LEVEL}, {"cells": ["华东", "金"], "by": PRODUCT}],
    }
    assert gm.card_query(card) == {
        "metric": "sales", "version": 2, "group_by": ["region"],
        "drill": [
            {"bucket": {"region": "华东"}, "by": "customers.level@buyer"},
            {"bucket": {"region": "华东", "customers.level@buyer": "金"}, "by": "product"},
        ],
    }
    # Cells that do not fit the level above leave the query out.
    assert gm.card_query({**card, "drill": [{"cells": ["华东", "金"], "by": LEVEL}]}) is None


def test_a_card_whose_metric_query_is_not_valid_is_listed_without_it():
    cards = [
        {"no": 1, "title": "月销售额", "kind": "metric", "metric": {"version": 1}},
        {"no": 2, "title": "销售额", "kind": "metric", "metric": {"metric_id": "sales", "version": 2, "drill": [{"cells": []}]}},
        # Times no calendar reaches.
        {
            "no": 3, "title": "月销售额", "kind": "metric",
            "metric": {
                "metric_id": "monthly", "version": 1, "owner_object_type_id": "orders",
                "drill": [{"cells": [10**17], "by": REGION}], "time_grain": "month",
            },
        },
        {
            "no": 4, "title": "销售额", "kind": "metric",
            "metric": {"metric_id": "sales", "version": 2, "time": {"start": 0, "end": 10**17}},
        },
    ]
    run, _, _ = _run(cards=cards, asked_from=7)
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert {
        "- #1 月销售额 (metric)", "- #2 销售额 (metric)", "- #3 月销售额 (metric)", "- #4 销售额 (metric)",
    } <= set(text.splitlines())
    # A card the analysis does not have is not the one the question is asked from.
    assert "asks this question from" not in text


def test_a_period_written_among_the_bucket_values_names_the_period():
    run, tools, _ = _run()
    out = _invoke(
        tools["query_metric"], metric="monthly", group_by=["region"],
        drill=[{"bucket": {"region": "华东", "period": "2026-03"}, "by": "product"}],
    )
    assert [(r["period"], r["product"]) for r in out["rows"]] == [("2026-03", "A"), ("2026-03", "B")]
    out = _invoke(
        tools["show_metric_participants"], metric="monthly", group_by=["region"],
        bucket={"period": "2026-02", "region": "华南"},
    )
    # The order of that month and region, and the buyer the stand-in adds.
    assert out["participants"] == 2 and out["planned_added"]["nodes"] == 1
    assert [n["id"] for n in builtin_tools.get("graph_explorer").result(run)["nodes"]] == ["orders-5"]


def test_a_period_is_needed_to_drill_a_metric_read_by_period():
    _, tools, _ = _run()
    with pytest.raises(ToolException, match="read by period"):
        asyncio.run(tools["query_metric"].coroutine(metric="monthly", drill=[{"by": "region"}]))


def test_days_are_read_in_utc_plus_eight():
    _, tools, calls = _run()
    out = _invoke(tools["query_metric"], metric="sales", group_by=["region"], time={"start": "2026-03-01", "end": "2026-03-31"})
    assert [(r["region"], r["value"]) for r in out["rows"]] == [("华东", "50"), ("华南", "40")]
    assert _metric_calls(calls)[0]["query"]["time"] == {"start": _ms(2026, 3, 1), "end": _ms(2026, 4, 1) - 1}
    with pytest.raises(ToolException, match="YYYY-MM-DD"):
        asyncio.run(tools["query_metric"].coroutine(metric="sales", time={"start": "3月", "end": "2026-03-31"}))


def test_unknown_metrics_dimensions_and_buckets_say_what_to_change():
    _, tools, calls = _run()
    with pytest.raises(ToolException, match="use an id the instructions list"):
        asyncio.run(tools["query_metric"].coroutine(metric="ghost"))
    with pytest.raises(ToolException, match="cannot be split by 'opponent'.*region, product") as refused:
        asyncio.run(tools["query_metric"].coroutine(metric="sales", group_by=["opponent"]))
    assert "run_cypher" in str(refused.value)
    with pytest.raises(ToolException, match="No such bucket.*region=华东; region=华南"):
        asyncio.run(
            tools["query_metric"].coroutine(metric="sales", group_by=["region"], drill=[{"bucket": {"region": "西北"}, "by": "product"}])
        )
    with pytest.raises(ToolException, match="say which region"):
        asyncio.run(tools["query_metric"].coroutine(metric="sales", group_by=["region"], drill=[{"by": "product"}]))
    with pytest.raises(ToolException, match="already split by region"):
        asyncio.run(
            tools["query_metric"].coroutine(metric="sales", group_by=["region"], drill=[{"bucket": {"region": "华东"}, "by": "region"}])
        )
    with pytest.raises(ToolException, match="can be drawn as bar, pie"):
        asyncio.run(tools["query_metric"].coroutine(metric="sales", group_by=["region"], chart="line"))


def test_a_bucket_is_looked_for_past_the_first_page(monkeypatch):
    monkeypatch.setattr(gm, "ROWS", 1)
    _, tools, calls = _run()
    out = _invoke(tools["query_metric"], metric="sales", group_by=["region"], drill=[{"bucket": {"region": "华南"}, "by": "product"}])
    assert [(r["product"], r["value"]) for r in out["rows"]] == [("A", "40")]
    assert "cut" in out
    assert [c["query"].get("cursor") for c in _metric_calls(calls)] == [None, "1", None]


def test_a_chart_outside_an_analysis_is_not_drawn():
    run, tools, _ = _run(analysis=False)
    out = _invoke(tools["query_metric"], metric="sales", chart="number")
    assert "Not drawn" in out["chart"]
    [step] = builtin_tools.get("graph_explorer").progress(run)["metrics"]
    assert "chart" not in step


def test_participants_of_a_bucket_are_planned_like_shown_instances():
    run, tools, calls = _run()
    out = _invoke(
        tools["show_metric_participants"], metric="sales", group_by=["region"],
        drill=[{"bucket": {"region": "华东"}, "by": "product"}], bucket={"product": "A"},
    )
    assert out["participants"] == 3 and out["planned_added"]["nodes"] == 2
    assert "counted, not planned" in out["other_roles"]
    [through] = [c[1] for c in calls if c[0] == "drill_through_metric"]
    assert through["query"] == _metric_calls(calls)[-1]["query"] and through["page_size"] == 100
    result = builtin_tools.get("graph_explorer").result(run)
    assert sorted(n["id"] for n in result["nodes"]) == ["orders-1", "orders-2"]
    assert [a["type"] for a in result["actions"]] == ["merge_subgraph"]
    [step] = builtin_tools.get("graph_explorer").progress(run)["metrics"]
    assert step["tool"] == "show_metric_participants" and step["cells"] == ["华东", "A"]
    assert step["drill"] == [{"cells": ["华东"], "by": PRODUCT}]


def test_participants_past_the_canvas_room_are_not_fetched():
    run, tools, calls = _run(max_nodes=1)
    out = _invoke(tools["show_metric_participants"], metric="sales", group_by=["region"], bucket={"region": "华东"})
    assert out["participants"] == 4 and out["planned_added"]["nodes"] == 1 and out["left_out"] == 2
    [fetch] = [c[1] for c in calls if c[0] == "query_object_instance"]
    assert fetch["condition"]["value"] == [1]
    assert run.state["warnings"]
