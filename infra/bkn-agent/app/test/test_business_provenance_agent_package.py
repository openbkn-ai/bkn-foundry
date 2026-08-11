"""业务溯源优化 Agent 的部署合同。"""

import json
from pathlib import Path


PACKAGE = (
    Path(__file__).resolve().parents[2]
    / "deploy"
    / "agents"
    / "business-provenance-optimizer.json"
)


def _agent_package():
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    assert package["format"] == "bkn-agent/v1"
    assert len(package["items"]) == 1
    return package["items"][0]


def test_business_provenance_agent_has_only_readonly_schema_tools():
    item = _agent_package()
    spec = item["spec"]

    assert spec["mode"] == "chat"
    assert spec["status"] == "published"
    assert spec["skills"] == []
    assert spec["tools"] == [
        {
            "type": "context_loader",
            "allowed_tools": [
                "search_schema",
                "get_kn_detail",
                "get_object_types",
                "get_relation_types",
                "list_skills",
                "get_skill_content",
                "read_skill_file",
            ],
        }
    ]

    serialized = json.dumps(spec["tools"], ensure_ascii=False)
    for forbidden in (
        "query_object_instance",
        "run_sql",
        "execute_action",
        "execute_skill",
        "run_skill",
    ):
        assert forbidden not in serialized


def test_business_provenance_agent_prompt_matches_the_runtime_output_contract():
    prompt = _agent_package()["prompt"]["content"]

    assert "\\n" not in prompt
    for required in (
        '"decision": "change_required|no_change|not_evaluable"',
        '"scope": "bkn|bkn_trace|mcp|sdk|agent"',
        '"location"',
        '"problem"',
        '"change"',
        '"trace_evidence_operation_ids"',
        '"bkn_schema_evidence"',
        '"acceptance"',
    ):
        assert required in prompt
    assert '"verification"' not in prompt
    assert "资源 ID 不是知识网络 ID" in prompt
    assert "不得把 Markdown 中“资源”字段的值作为 kn_id" in prompt
