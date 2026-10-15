"""知识网络优化 Agent 的部署合同。"""

import json

from app.test.test_shared_agent_seeds import seed_package


def _agent_package():
    return seed_package()["items"][0]


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
    item = _agent_package()
    prompt = item["prompt"]["content"]

    assert "\\n" not in prompt
    assert item["spec"]["name"] == "知识网络优化Agent"
    assert "知识网络优化 Agent" in prompt
    assert "不包含 Claim Agent 生成的证据链判断" in prompt
    assert "不得评价页面布局、证据链文案或交互体验" in prompt
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


def test_optimizer_prompt_constrains_skill_identity_scope_and_recovery():
    prompt = _agent_package()["prompt"]["content"]
    for required in (
        "Agent 名称/ID 不是 Skill ID",
        "先调用 list_skills",
        "全局可发现不代表已在知识网络挂载",
        "search_scope",
        "至少启用一类概念",
        "不得静默扩大显式范围",
        "同一拒绝不得重复调用",
        "not_evaluable",
    ):
        assert required in prompt


def test_optimizer_has_a_600_second_budget_without_changing_other_limits():
    item = _agent_package()
    assert item["agent_id"] == "business_provenance_optimizer"
    assert item["spec"]["limits"] == {
        "max_turns": 12,
        "max_tool_calls": 12,
        "timeout_s": 600,
        "max_output_tokens": 8192,
    }
    claim_package = seed_package("business_provenance_claim_attribution")
    assert claim_package["items"][0]["spec"]["limits"]["timeout_s"] == 75
