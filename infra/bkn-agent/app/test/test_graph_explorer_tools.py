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


@pytest.mark.parametrize(
    "query, message",
    [
        ("MATCH (t:teams)", "complete query"),
        ("MATCH (t:teams) RETURN t", "LIMIT"),
        ("MATCH (t:teams) RETURN t LIMIT 500", "at most"),
    ],
)
def test_cypher_must_be_complete_and_bounded(query, message):
    _, tools, calls = _run()
    with pytest.raises(Exception, match=message):
        asyncio.run(tools["run_cypher"].coroutine(query=query))
    assert not any(c[0] == "run_cypher" for c in calls)


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
