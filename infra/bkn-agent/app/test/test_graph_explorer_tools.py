# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""The graph explorer provider's query tools against a stand-in Context Loader.

The id, row and Cypher rules mirror the EE explorer's
(studio/src/graph-explorer/graph-explorer.service.ts, cypher-pattern.ts): a node
the server plans must carry the id the Web already gives the same instance.
"""

import asyncio
import json
from types import SimpleNamespace

import pytest
from langchain_core.tools import ToolException

from app.core import builtin_tools
from app.core import graph_explorer_data as data
from app.core import graph_explorer_tools as graph

TEAMS = data.ObjectTypeMeta("teams", "球队", ["key_id"], {"key_id": "integer", "team_name": "string"})


def test_instance_ids_follow_ontology_query():
    assert data.instance_id("teams", ["key_id"], {"key_id": 9}) == "teams-9"
    assert data.instance_id("a", ["x", "y"], {"x": "1"}) == "a-1___NULL__"
    assert data.instance_id("a", [], {"x": 1}) is None
    assert data.identity_key(["x", "y"], {"x": 1}) is None
    assert data.key_value_for(TEAMS, "9") == 9
    assert data.key_value_for(data.ObjectTypeMeta("p", "p", ["code"], {"code": "string"}), "09") == "09"


def test_ids_split_on_the_longest_object_type_prefix():
    items, unknown = data.parse_ids(
        ["team_appearances-6", "teams-9", "teams-9", "ghost-1"], ["teams", "team_appearances"]
    )
    assert items == [("team_appearances", "6"), ("teams", "9")]
    assert unknown == ["ghost-1"]


def test_rows_and_search_hits_become_nodes():
    [node] = data.nodes_from_rows(TEAMS, {"datas": [{"key_id": 9, "team_name": "Brazil", "_display": "Brazil", "_score": 1}]})
    assert node["id"] == "teams-9" and node["identity"] == {"key_id": 9}
    assert node["label"] == "Brazil" and "_score" not in node["properties"]
    hits = data.nodes_from_search(
        {
            "nodes": [
                {"object_type_id": "teams", "instance_name": "Brazil", "unique_identities": {"key_id": 9}, "properties": {}},
                {"object_type_id": "unknown", "properties": {}},
            ]
        },
        {"teams": TEAMS},
    )
    assert [(n["id"], n["label"]) for n in hits] == [("teams-9", "Brazil")]


def _obj(ot, key, **props):
    return {"kind": "object", "object_type_id": ot, "key": {"key_id": {"type": "integer", "value": str(key)}}, "properties": props}


def test_cypher_rows_become_nodes_relations_and_computed_values():
    result = {
        "entries": [
            {
                "t": _obj("teams", 9),
                "r": {"kind": "relation", "relation_type_id": "rel_home", "source": _obj("matches", 1), "target": _obj("teams", 9)},
                "m": _obj("matches", 1),
                "t_goals": 3,
                "note": "x",
            }
        ]
    }
    out = data.graph_from_cypher(result, {"teams": TEAMS}, {"rel_home": "主队"})
    assert out.holds_graph
    assert {n["id"] for n in out.nodes} == {"teams-9", "matches-1"}
    assert [(e["id"], e["relation_type_name"]) for e in out.edges] == [("matches-1|rel_home|teams-9", "主队")]
    # With two node columns, `t_goals` belongs to t and `note` to nobody.
    assert next(n for n in out.nodes if n["id"] == "teams-9")["computed"] == {"t_goals": 3}
    assert out.table == {"columns": ["t", "m", "note"], "rows": [{"t": "teams-9", "m": "matches-1", "note": "x"}]}


def test_cypher_derived_edges_count_their_evidence():
    rows = [
        {"source": _obj("teams", 9), "relationType": "参加过", "target": _obj("matches", m), "n": 2}
        for m in (1, 1)
    ] + [{"source": _obj("teams", 9), "relationType": "参加过", "target": _obj("matches", 2), "evidence": [_obj("players", 7)]}]
    out = data.graph_from_cypher({"entries": rows}, {}, {})
    derived = {e["id"]: e for e in out.edges if e.get("virtual")}
    assert derived["virtual|teams-9|参加过|matches-1"]["evidence_count"] == 4
    assert derived["virtual|teams-9|参加过|matches-1"]["computed"] == {"n": 4}
    assert derived["virtual|teams-9|参加过|matches-2"]["evidence_count"] == 1
    assert "evidence|players-7|teams-9" in derived and "evidence|players-7|matches-2" in derived


def test_scalar_only_cypher_holds_no_graph():
    out = data.graph_from_cypher({"entries": [{"n": 3}]}, {}, {})
    assert not out.holds_graph and out.nodes == [] and out.table == {"columns": ["n"], "rows": [{"n": 3}]}


# ---- tools against a stand-in Context Loader ----

DETAIL = {
    "object_types": [
        {"id": "teams", "name": "球队", "properties": [{"name": "team_name", "type": "string"}]},
        {"id": "matches", "name": "比赛"},
    ],
    "relation_types": [
        {"id": "rel_home", "name": "主队", "source_object_type_id": "matches", "target_object_type_id": "teams"}
    ],
}
OBJECT_TYPES = {
    "object_types": [
        {"id": "teams", "name": "球队", "primary_keys": ["key_id"], "data_properties": [{"name": "key_id", "type": "integer"}]},
        {"id": "matches", "name": "比赛", "primary_keys": ["key_id"], "data_properties": [{"name": "key_id", "type": "integer"}]},
    ]
}
ROWS = {
    "teams": [{"key_id": 9, "team_name": "Brazil", "_display": "Brazil"}],
    "matches": [{"key_id": 1, "_display": "M-1"}, {"key_id": 2, "_display": "M-2"}],
}


def _tool(name, calls, answer):
    async def coroutine(**kwargs):
        calls.append((name, kwargs))
        value = answer(kwargs) if callable(answer) else answer
        return json.dumps(value, ensure_ascii=False), None

    return SimpleNamespace(name=name, coroutine=coroutine)


def _rows(kwargs):
    wanted = set(kwargs.get("condition", {}).get("value") or [])
    return {"datas": [r for r in ROWS[kwargs["ot_id"]] if not wanted or r["key_id"] in wanted]}


def _subgraph(kwargs):
    return {
        "entries": [
            {
                "relation_paths": [
                    {
                        "relations": [
                            {"source_object_id": "matches-1", "target_object_id": "teams-9", "relation_type_id": "rel_home"},
                            {"source_object_id": "matches-2", "target_object_id": "teams-1", "relation_type_id": "rel_home"},
                        ]
                    }
                ]
            }
        ]
    }


def _run(nodes=(), cypher=None):
    run = builtin_tools.Run(
        provider=builtin_tools.get("graph_explorer"),
        kn_id="network-1",
        context=graph.CanvasContext(canvas_id="c1", revision=2, nodes=list(nodes)),
        account_id="user-1",
        account_type="user",
    )
    calls: list = []
    run.state["context_loader"] = {
        name: _tool(name, calls, answer)
        for name, answer in {
            "get_kn_detail": DETAIL,
            "get_object_types": OBJECT_TYPES,
            "search_instance": {
                "nodes": [{"object_type_id": "teams", "unique_identities": {"key_id": 9}, "properties": {"team_name": "Brazil"}}]
            },
            "query_object_instance": _rows,
            "query_instance_subgraph": _subgraph,
            "run_cypher": cypher or {"entries": []},
        }.items()
    }
    tools = {t.name: t for t in graph._tools(run)}
    return run, tools, calls


def _invoke(tool, **arguments):
    return json.loads(asyncio.run(tool.ainvoke(arguments)))


def test_search_finds_candidates_without_planning_them():
    run, tools, calls = _run()
    found = _invoke(tools["search_instances"], query="巴西")
    assert found["items"][0]["id"] == "teams-9"
    assert "teams-9" in run.state["candidates"] and not run.state.get("actions")
    # Every call is made as the run's network, in JSON.
    assert all(c[1]["kn_id"] == "network-1" and c[1]["response_format"] == "json" for c in calls)


def test_show_plans_instances_and_the_relations_among_them():
    run, tools, calls = _run()
    shown = _invoke(tools["show_instances"], ids=["teams-9", "matches-1", "nobody-3"])
    assert shown["planned_added"] == {"nodes": 2, "edges": 1}
    assert shown["not_found"] == ["nobody-3"]
    query = next(c[1] for c in calls if c[0] == "query_object_instance" and c[1]["ot_id"] == "teams")
    assert query["condition"] == {"field": "key_id", "operation": "in", "value": [9]}
    result = builtin_tools.get("graph_explorer").result(run)
    assert [a["type"] for a in result["actions"]] == ["merge_subgraph"]
    # The edge to teams-1, which was not asked for, is left out.
    assert [e["id"] for e in result["edges"]] == ["matches-1|rel_home|teams-9"]
    # A second look at the canvas sees the plan.
    state = _invoke(tools["canvas_state"])
    assert state["total_nodes"] == 2 and all(n["planned"] for n in state["nodes"]["items"])


def test_nodes_already_on_the_canvas_are_not_planned_again():
    run, tools, _ = _run(nodes=[{"id": "teams-9", "object_type_id": "teams"}])
    shown = _invoke(tools["show_instances"], ids=["teams-9", "matches-1"])
    assert shown["planned_added"] == {"nodes": 1, "edges": 1}
    result = builtin_tools.get("graph_explorer").result(run)
    assert [n["id"] for n in result["nodes"]] == ["matches-1"]


def test_composite_keys_need_a_found_identity():
    run, tools, _ = _run()
    run.state["metas"] = {"pair": data.ObjectTypeMeta("pair", "pair", ["a", "b"])}
    run.state["network"] = {"object_types": {"pair": {"name": "pair", "properties": []}}, "relation_types": {}}
    with pytest.raises(Exception, match="composite key"):
        asyncio.run(tools["show_instances"].coroutine(ids=["pair-1_2"]))


def test_cypher_plans_graph_results_and_fills_in_key_only_nodes():
    cypher = {
        "entries": [
            {
                "t": _obj("teams", 9),
                "r": {"kind": "relation", "relation_type_id": "rel_home", "source": _obj("matches", 1), "target": _obj("teams", 9)},
                "m": _obj("matches", 1),
            }
        ]
    }
    run, tools, _ = _run(cypher=cypher)
    out = _invoke(tools["run_cypher"], query="MATCH (m:matches)-[r:rel_home]->(t:teams) RETURN t, r, m LIMIT 5")
    assert out["planned_added"] == {"nodes": 2, "edges": 1}
    labels = {n["id"]: n["label"] for n in builtin_tools.get("graph_explorer").result(run)["nodes"]}
    assert labels == {"teams-9": "Brazil", "matches-1": "M-1"}


def test_cypher_scalars_come_back_as_a_table_and_plan_nothing():
    run, tools, _ = _run(cypher={"columns": [{"name": "n"}], "entries": [{"n": 3}]})
    out = _invoke(tools["run_cypher"], query="MATCH (t:teams) RETURN count(t) AS n LIMIT 1")
    assert out["table"] == {"columns": ["n"], "rows": [{"n": 3}], "total": 1}
    assert "planned_added" not in out and not run.state.get("actions")


def test_cypher_must_be_complete():
    _, tools, calls = _run()
    with pytest.raises(Exception, match="complete query"):
        asyncio.run(tools["run_cypher"].coroutine(query="MATCH (t:teams)"))
    assert not any(c[0] == "run_cypher" for c in calls)


def test_cypher_without_limit_runs_and_says_when_the_platform_cut_it():
    # The canvas limit, not a row limit, bounds what is planned.
    _, tools, calls = _run(cypher={"entries": [], "execution": {"conclusions_limited": True}})
    out = _invoke(tools["run_cypher"], query="MATCH (t:teams) RETURN t.team_name AS name")
    assert any(c[0] == "run_cypher" for c in calls)
    assert "default limit" in out["limited"]


def test_the_canvas_limit_cuts_and_warns():
    run, tools, _ = _run(nodes=[{"id": f"x-{i}", "object_type_id": "x"} for i in range(graph.NODE_LIMIT - 1)])
    shown = _invoke(tools["show_instances"], ids=["teams-9", "matches-1"])
    assert shown["planned_added"]["nodes"] == 1 and shown["left_out"] == 1
    assert run.state["warnings"]


def test_the_prompt_carries_the_network_schema():
    run, _, _ = _run()
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "- teams (球队)" in text and "team_name:string" in text
    assert "- rel_home (主队): matches -> teams" in text


def test_without_context_loader_the_tools_say_so():
    run, tools, _ = _run()
    run.state.pop("context_loader")
    with pytest.raises(Exception, match="Context Loader is unavailable"):
        asyncio.run(tools["search_instances"].coroutine(query="x"))


def test_a_relation_path_covers_every_pair_of_its_ends():
    """Regression: the path limit capped the relations returned in total, so one
    team and thirty matches came back with ten edges."""
    run, tools, calls = _run()
    ROWS["matches"] = [{"key_id": i, "_display": f"M-{i}"} for i in range(1, 31)]
    try:
        relations = [
            {"source_object_id": f"matches-{i}", "target_object_id": "teams-9", "relation_type_id": "rel_home"}
            for i in range(1, 31)
        ]
        run.state["context_loader"]["query_instance_subgraph"] = _tool(
            "query_instance_subgraph",
            calls,
            lambda kwargs: {"entries": [{"relation_paths": [{"relations": relations[: kwargs["relation_type_paths"][0]["limit"]]}]}]},
        )
        shown = _invoke(tools["show_instances"], ids=["teams-9", *[f"matches-{i}" for i in range(1, 31)]])
    finally:
        ROWS["matches"] = [{"key_id": 1, "_display": "M-1"}, {"key_id": 2, "_display": "M-2"}]
    path = next(c[1] for c in calls if c[0] == "query_instance_subgraph")["relation_type_paths"][0]
    assert path["limit"] == 30
    assert shown["planned_added"]["edges"] == 30
    # Every pair was asked for, so a full answer is not a truncated one.
    assert not any("may be incomplete" in w for w in run.state.get("warnings", []))


def test_a_path_cut_at_the_backend_limit_says_it_may_be_incomplete(monkeypatch):
    monkeypatch.setattr(graph, "PATH_LIMIT", 1)
    run, tools, _ = _run()
    _invoke(tools["show_instances"], ids=["teams-9", "matches-1", "matches-2"])
    assert any("reached the 1 limit" in w for w in run.state["warnings"])


def test_a_context_loader_transport_failure_is_the_tools_error():
    run, tools, _ = _run()

    async def broken(**kwargs):
        raise TimeoutError("read timed out")

    run.state["context_loader"]["search_instance"] = SimpleNamespace(name="search_instance", coroutine=broken)
    with pytest.raises(ToolException, match="could not reach Context Loader: TimeoutError"):
        asyncio.run(tools["search_instances"].coroutine(query="x"))


def test_large_networks_list_object_types_by_page_and_say_relations_are_unlisted():
    run, tools, calls = _run()
    run.state["context_loader"]["get_kn_detail"] = _tool(
        "get_kn_detail", calls, {"object_types": None, "relation_types": None, "notice": "too large"}
    )
    pages = {0: {"object_types": OBJECT_TYPES["object_types"][:1], "next_offset": 5}, 5: {"object_types": OBJECT_TYPES["object_types"][1:]}}
    run.state["context_loader"]["get_object_types"] = _tool(
        "get_object_types", calls, lambda kwargs: pages[kwargs["offset"]] if "offset" in kwargs else OBJECT_TYPES
    )
    shown = _invoke(tools["show_instances"], ids=["teams-9"])
    assert shown["planned_added"] == {"nodes": 1, "edges": 0}
    # Paged by next_offset, not by how many came back.
    assert [c[1]["offset"] for c in calls if c[0] == "get_object_types" and "offset" in c[1]] == [0, 5]
    assert set(run.state["network"]["object_types"]) == {"teams", "matches"}
    assert any("too large to list its relation types" in w for w in run.state["warnings"])
    text = asyncio.run(builtin_tools.get("graph_explorer").instructions(run))
    assert "- teams (球队)" in text and "too large to list its relation types" in text
# ---- expand_neighbours ----


def _expand_run(counts: dict[int, int], threshold=20, max_nodes=500, nodes=None):
    """A run whose run_cypher answers count and fetch queries for teams-9 via rel_home (in)."""

    def cypher(kwargs):
        query = kwargs["query"]
        seeds = list(kwargs.get("parameters", {}).values())
        if "count(" in query:
            return {"entries": [{"s__key_id": s, "n": counts.get(s, 0)} for s in seeds]}
        limit = int(query.rsplit("LIMIT", 1)[1])
        rows = [
            {"s__key_id": s, "t__key_id": str(100 * s + i), "t__match_name": f"M-{s}-{i}"}
            for s in seeds
            for i in range(counts.get(s, 0))
        ]
        return {"entries": rows[:limit]}

    run, tools, calls = _run(
        nodes=nodes if nodes is not None else [{"id": "teams-9", "object_type_id": "teams"}],
        cypher=cypher,
    )
    run.context.limits.expand_threshold = threshold
    run.context.limits.max_nodes = max_nodes
    return run, tools, calls


def test_expand_counts_then_draws_small_groups():
    run, tools, calls = _expand_run({9: 3})
    out = _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")
    assert out["planned_added"] == {"nodes": 3, "edges": 3}
    count, fetch = [c[1] for c in calls if c[0] == "run_cypher"]
    assert count["query"].startswith("MATCH (s:`teams`)<-[:`rel_home`]-(t:`matches`)")
    assert count["parameters"] == {"p0": 9}
    assert "LIMIT 3" in fetch["query"]
    result = builtin_tools.get("graph_explorer").result(run)
    assert {e["id"] for e in result["edges"]} == {f"matches-{900 + i}|rel_home|teams-9" for i in range(3)}
    # Keys come back as text and are typed by their declaration; labels prefer a name.
    node = next(n for n in result["nodes"] if n["id"] == "matches-900")
    assert node["identity"] == {"key_id": 900} and node["label"] == "M-9-0"
    assert result["actions"][0]["anchor_id"] == "teams-9"


def test_expand_folds_large_groups_into_aggregates():
    run, tools, calls = _expand_run({9: 50}, threshold=20)
    out = _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")
    assert out["aggregated"] == [{"seed": "teams-9", "relation": "主队", "target_type": "比赛", "total": 50}]
    assert out["planned_added"] == {"nodes": 0, "edges": 0}
    # Counted, not fetched.
    assert len([c for c in calls if c[0] == "run_cypher"]) == 1
    result = builtin_tools.get("graph_explorer").result(run)
    assert result["aggregates"] == [
        {
            "id": "agg|teams-9|rel_home|in",
            "seed_id": "teams-9",
            "relation_type_id": "rel_home",
            "relation_type_name": "主队",
            "walk": "in",
            "peer_object_type_id": "matches",
            "total": 50,
        }
    ]
    assert result["actions"][0]["aggregate_ids"] == ["agg|teams-9|rel_home|in"]


def test_expand_without_room_falls_back_to_an_aggregate():
    run, tools, _ = _expand_run({9: 5}, threshold=20, max_nodes=3)
    out = _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")
    assert out["aggregated"][0]["total"] == 5 and out["planned_added"]["nodes"] == 0


def test_expand_reports_no_neighbours_and_unknown_seeds():
    _, tools, _ = _expand_run({})
    assert _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")["note"]
    with pytest.raises(Exception, match="Unknown instance ids"):
        asyncio.run(tools["expand_neighbours"].coroutine(ids=["teams-404"], direction="in"))


def test_expanding_a_candidate_puts_it_on_the_canvas_too():
    run, tools, _ = _expand_run({9: 2}, nodes=[])
    _invoke(tools["search_instances"], query="巴西")
    out = _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")
    assert out["planned_added"] == {"nodes": 3, "edges": 2}


def test_plan_space_matches_the_ee_rules():
    rel = data.IncidentRelation("r", "r", "out", "a", "b")
    groups = [data.ExpandGroup("a-1", rel, 30), data.ExpandGroup("a-2", rel, 4), data.ExpandGroup("a-3", rel, 8)]
    plan = data.plan_space(groups, threshold=20, room=10, existing=set())
    assert [g.seed_id for g in plan.aggregate] == ["a-1", "a-3"]
    assert [g.seed_id for g in plan.draw] == ["a-2"]
    refreshed = data.plan_space(groups[:1], threshold=20, room=0, existing={"agg|a-1|r|out"})
    assert [g.seed_id for g in refreshed.refresh] == ["a-1"]


def test_fetch_reads_only_properties_the_caller_may_see_in_full():
    # One masked property in RETURN makes bkn-backend refuse the whole query.
    peer = data.meta_from(
        {
            "id": "matches",
            "primary_keys": ["key_id"],
            "data_properties": [{"name": "key_id", "type": "integer"}, {"name": "match_name"}, {"name": "phone"}],
            "effective_permissions": {"key_id": "full", "match_name": "full", "phone": "masked"},
        }
    )
    rel = data.IncidentRelation("rel_home", "主队", "in", "teams", "matches")
    query, _ = data.fetch_query(rel, TEAMS, peer, [{"key_id": 9}], 10)
    assert "match_name" in query and "phone" not in query
    # Without permissions in the answer every property is read, as before.
    assert data.meta_from({"id": "x", "data_properties": [{"name": "a"}]}).readable is None


def test_keys_beyond_exact_numbers_stay_text():
    meta = data.ObjectTypeMeta("orders", "订单", ["id"], {"id": "integer"})
    assert data.typed(meta, "id", "1234567890123456789") == "1234567890123456789"
    assert data.typed(meta, "id", "42") == 42


def test_expand_refuses_when_the_network_lists_no_relations():
    run, tools, _ = _expand_run({9: 3})
    run.state["network"] = {"object_types": {}, "relation_types": {}, "relations_unlisted": True}
    with pytest.raises(ToolException, match="too large"):
        asyncio.run(tools["expand_neighbours"].coroutine(ids=["teams-9"], direction="in"))


def test_expand_says_which_ids_it_left_out():
    run, tools, _ = _expand_run({9: 3})
    out = _invoke(tools["expand_neighbours"], ids=["teams-9", "teams-404"], direction="in")
    assert out["not_found"] == ["teams-404"]


def test_the_canvas_reads_aggregates_this_run_planned():
    run, tools, _ = _expand_run({9: 50}, threshold=20)
    _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")
    aggregates = _invoke(tools["canvas_state"])["aggregates"]
    assert aggregates["total"] == 1
    assert aggregates["items"][0]["total"] == 50 and aggregates["items"][0]["planned"]


# ---- canvas tools ----


def _canvas_run(**context):
    run, tools, _ = _run(
        nodes=[
            {"id": "teams-1", "object_type_id": "teams"},
            {"id": "teams-9", "object_type_id": "teams"},
            {"id": "matches-1", "object_type_id": "matches"},
        ]
    )
    for key, value in context.items():
        setattr(run.context, key, value)
    return run, tools


def _result(run):
    return builtin_tools.get("graph_explorer").result(run)


def test_remove_plans_removal_and_the_canvas_reads_without_them():
    run, tools = _canvas_run()
    removed = _invoke(tools["remove_instances"], ids=["teams-1", "teams-404"])
    assert removed == {"planned_removed": 1, "not_on_canvas": ["teams-404"]}
    state = _invoke(tools["canvas_state"])
    assert sorted(n["id"] for n in state["nodes"]["items"]) == ["matches-1", "teams-9"]
    assert _result(run)["actions"] == [{"action_id": "1", "type": "remove_nodes", "node_ids": ["teams-1"]}]


def test_a_removed_node_shown_again_comes_back():
    run, tools = _canvas_run()
    _invoke(tools["remove_instances"], ids=["teams-9"])
    _invoke(tools["show_instances"], ids=["teams-9"])
    assert "teams-9" in {n["id"] for n in _invoke(tools["canvas_state"])["nodes"]["items"]}
    result = _result(run)
    assert [a["type"] for a in result["actions"]] == ["remove_nodes", "merge_subgraph"]
    assert "teams-9" in {n["id"] for n in result["nodes"]}


def test_view_tools_record_their_patches():
    run, tools = _canvas_run(object_types=[graph.ObjectTypeRef(id="teams"), graph.ObjectTypeRef(id="matches")])
    _invoke(tools["configure_display"], labels=[{"object_type": "teams", "label": "{team_name}"}], node_size_by="")
    _invoke(tools["configure_analysis"], chart={"kind": "category", "propertyByOt": {"teams": "region"}})
    _invoke(tools["arrange_canvas"], layout="radial")
    with pytest.raises(ToolException, match="Unknown object types"):
        asyncio.run(tools["configure_display"].coroutine(labels=[{"object_type": "nope", "label": None}]))
    with pytest.raises(Exception):
        asyncio.run(tools["arrange_canvas"].ainvoke({"layout": "spiral"}))
    actions = _result(run)["actions"]
    assert [a["type"] for a in actions] == ["set_display", "set_analysis", "set_layout"]
    assert actions[0]["patch"] == {"labels": [{"object_type": "teams", "label": "{team_name}"}], "node_size_by": None}
    assert actions[2]["layout"] == "radial"
    assert len(_invoke(tools["view_state"])["planned_view_actions"]) == 3


def test_canvas_control_checks_ids_and_tracks_the_selection():
    run, tools = _canvas_run()
    with pytest.raises(ToolException, match="Not on the canvas"):
        asyncio.run(tools["canvas_control"].coroutine(action="select", ids=["teams-404"]))
    _invoke(tools["canvas_control"], action="select", ids=["teams-1", "teams-9"])
    _invoke(tools["canvas_control"], action="arrange_selection", layout="grid")
    assert _invoke(tools["canvas_control"], action="remove_selected") == {"planned_removed": 2}
    assert _invoke(tools["canvas_state"])["total_nodes"] == 1
    with pytest.raises(ToolException, match="No nodes are selected"):
        asyncio.run(tools["canvas_control"].coroutine(action="remove_selected"))
    assert [a["type"] for a in _result(run)["actions"]] == ["select", "arrange_selection", "remove_nodes"]


def test_undo_takes_back_only_this_runs_view_actions():
    run, tools = _canvas_run()
    with pytest.raises(ToolException, match="nothing to undo"):
        asyncio.run(tools["canvas_control"].coroutine(action="undo"))
    _invoke(tools["remove_instances"], ids=["teams-1"])
    _invoke(tools["canvas_control"], action="undo")
    assert _invoke(tools["canvas_state"])["total_nodes"] == 3
    # The page may already show the removal, so the undo is an action too.
    assert [(a["type"], a.get("undoes")) for a in _result(run)["actions"]] == [("remove_nodes", None), ("undo", "1")]
    with pytest.raises(ToolException, match="nothing to undo"):
        asyncio.run(tools["canvas_control"].coroutine(action="undo"))
    run.state["actions"].append({"action_id": "9", "type": "merge_subgraph", "node_ids": [], "edge_ids": []})
    with pytest.raises(ToolException, match="remove_instances"):
        asyncio.run(tools["canvas_control"].coroutine(action="undo"))


def test_clear_takes_everything_off_and_is_left_to_the_web():
    run, tools = _canvas_run()
    cleared = _invoke(tools["canvas_control"], action="clear")
    assert cleared["planned"] == "clear"
    assert _invoke(tools["canvas_state"])["total_nodes"] == 0
    assert [a["type"] for a in _result(run)["actions"]] == ["clear"]


def test_manual_groups_get_ids_the_run_can_use_again():
    run, tools = _canvas_run(view={"canvas": {"manual_groups": [{"id": "g1", "name": "old"}]}})
    created = _invoke(tools["manual_group"], action="create", name="南美", ids=["teams-1", "teams-9"], color="#3366ff")
    group = created["planned"]["id"]
    _invoke(tools["manual_group"], action="rename", id=group, name="南美球队")
    _invoke(tools["manual_group"], action="collapse", id="g1")
    with pytest.raises(ToolException, match="Unknown manual group"):
        asyncio.run(tools["manual_group"].coroutine(action="dissolve", id="g404"))
    with pytest.raises(ToolException, match="hex"):
        asyncio.run(tools["manual_group"].coroutine(action="recolor", id="g1", color="blue"))
    actions = _result(run)["actions"]
    assert [a["action"] for a in actions] == ["create", "rename", "collapse"]
    assert actions[0]["ids"] == ["teams-1", "teams-9"] and actions[1]["id"] == group


def test_progress_sends_each_new_plan_once_with_its_data():
    run, tools = _canvas_run()
    provider = builtin_tools.get("graph_explorer")
    assert provider.progress(run) is None
    run.context.nodes = []
    _invoke(tools["show_instances"], ids=["teams-9", "matches-1"])
    _invoke(tools["arrange_canvas"], layout="grid")
    first = provider.progress(run)
    assert [a["type"] for a in first["actions"]] == ["merge_subgraph", "set_layout"]
    assert {n["id"] for n in first["nodes"]} == {"teams-9", "matches-1"} and len(first["edges"]) == 1
    assert provider.progress(run) is None
    _invoke(tools["remove_instances"], ids=["teams-9"])
    assert [a["type"] for a in provider.progress(run)["actions"]] == ["remove_nodes"]


def test_removing_an_aggregate_keeps_the_result_valid():
    # Regression: removal named the aggregate, and the result checked it as a node.
    run, tools, _ = _expand_run({9: 50}, threshold=20)
    _invoke(tools["expand_neighbours"], ids=["teams-9"], direction="in")
    assert _invoke(tools["remove_instances"], ids=["agg|teams-9|rel_home|in"])["planned_removed"] == 1
    result = builtin_tools.get("graph_explorer").result(run)
    assert result["actions"][-1] == {"action_id": "2", "type": "remove_nodes", "node_ids": ["agg|teams-9|rel_home|in"]}


def test_scalar_rows_cut_at_the_default_limit_say_so():
    # bkn-backend marks it at the top level; execution only comes with graph values.
    _, tools, _ = _run(cypher={"entries": [{"name": "Messi"}], "limited": True})
    out = _invoke(tools["run_cypher"], query="MATCH (p:players) RETURN p.name AS name")
    assert "default limit" in out["limited"]


def test_a_node_shown_again_takes_room_and_stays_off_when_there_is_none():
    run, tools = _canvas_run()
    run.context.limits.max_nodes = 3
    _invoke(tools["remove_instances"], ids=["teams-9"])
    # A full canvas again before teams-9 comes back: it stays off, and is not in the merge.
    run.state["nodes"]["x-1"] = {"id": "x-1", "object_type_id": "x", "properties": {}, "identity": {}}
    out = _invoke(tools["show_instances"], ids=["teams-9"])
    assert out.get("left_out") == 1
    merges = [a for a in builtin_tools.get("graph_explorer").result(run)["actions"] if a["type"] == "merge_subgraph"]
    assert all("teams-9" not in a["node_ids"] for a in merges)


def test_manual_group_ids_do_not_repeat_across_runs():
    run, tools = _canvas_run(view={"canvas": {"manual_groups": [{"id": "g-ai-1", "name": "上一轮"}]}})
    created = _invoke(tools["manual_group"], action="create", name="南美", ids=["teams-1"])
    assert created["planned"]["id"] != "g-ai-1"


def test_cypher_fills_in_only_nodes_the_canvas_has_room_for():
    cypher = {"entries": [{"t": _obj("teams", i)} for i in (1, 9, 21, 22, 23)]}
    run, tools, calls = _run(cypher=cypher)
    run.context.limits.max_nodes = 2
    out = _invoke(tools["run_cypher"], query="MATCH (t:teams) RETURN t")
    looked_up = [v for c in calls if c[0] == "query_object_instance" for v in c[1]["condition"]["value"]]
    # Two fit; the other three are left out, so they are not looked up either.
    assert looked_up == [1, 9] and out["left_out"] == 3
