# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Network and user settings: merge rules, ownership and permissions, and use in runs."""

import asyncio

import pytest

from app import public_auth
from app.core.agent_settings import instruction_block, merge
from app.models import AgentLimits, AgentOut, AgentSetting
from app.routers import executions
from app.test.test_public_execution import AUTH, BODY, URL, environment, parse_sse

SETTING = "/api/bkn-agent/v1/me/agents/studio_smart_qa/setting"
NETWORK = "/api/bkn-agent/v1/networks/network-1/agents/studio_smart_qa/setting"
CONFIG = "/api/bkn-agent/v1/me/agents/studio_smart_qa/config?kn_id=network-1"


def agent(**changes):
    base = {
        "agent_id": "studio_smart_qa",
        "name": "QA",
        "mode": "chat",
        "prompt_id": "qa-prompt",
        "tools": [{"type": "context_loader", "allowed_tools": ["a", "b", "c"]}],
        "limits": {"max_turns": 40, "max_tool_calls": 100, "timeout_s": 300},
        "status": "published",
        "create_user": "admin",
        "update_user": "admin",
        "create_time": 1,
        "update_time": 1,
    }
    base.update(changes)
    return AgentOut.model_validate(base)


def test_layers_append_instructions_and_only_narrow_tools_and_limits():
    network = AgentSetting(
        instruction="Use fiscal quarters.",
        allowed_tools=["a", "b", "x"],
        limits=AgentLimits(max_turns=60, timeout_s=120),
    )
    user = AgentSetting(instruction="Answer with tables.", allowed_tools=["b", "c"])

    effective = merge(agent(), network, 3, user, 7)

    # x is not allowed by the shared definition; c was dropped by the network.
    assert effective.allowed_tools == {"b"}
    # 60 turns cannot widen the shared 40; 120s narrows the shared 300s.
    assert effective.agent.limits.max_turns == 40
    assert effective.agent.limits.timeout_s == 120
    assert effective.agent.limits.max_tool_calls == 100
    assert effective.instructions == [
        ("network", "Use fiscal quarters."),
        ("user", "Answer with tables."),
    ]
    assert effective.versions == {"network": 3, "user": 7}
    block = instruction_block(effective.instructions)
    assert block.index("fiscal quarters") < block.index("tables")
    assert "cannot override" in block


def test_unset_layers_follow_the_layer_above():
    effective = merge(
        agent(tools=[{"type": "context_loader"}], limits=None),
        None,
        0,
        AgentSetting(),
        0,
    )
    assert effective.allowed_tools is None
    # Service defaults stand in for unset shared caps.
    assert effective.agent.limits.max_turns >= 1
    assert effective.agent.limits.timeout_s >= 1
    assert effective.instructions == []


def test_user_setting_belongs_to_its_owner_only(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            saved = await client.put(
                SETTING,
                json={"instruction": "Use tables", "allowed_tools": ["b"]},
                headers=AUTH,
            )
            assert saved.status_code == 200, saved.text
            assert saved.json()["version"] == 1
            again = await client.put(
                SETTING, json={"instruction": "Short"}, headers=AUTH
            )
            assert again.json()["version"] == 2

            other = await client.get(
                SETTING, headers={**AUTH, "Authorization": "Bearer other"}
            )
            assert other.json()["version"] == 0
            assert other.json()["setting"] == {
                "instruction": None,
                "allowed_tools": None,
                "limits": None,
            }

            cleared = await client.delete(SETTING, headers=AUTH)
            assert cleared.json()["version"] == 0
            assert (await client.get(SETTING, headers=AUTH)).json()["version"] == 0

    asyncio.run(run())


def test_setting_rejects_unknown_fields_and_oversized_instructions(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            for body in ({"model": "other"}, {"instruction": "x" * 2001}):
                response = await client.put(SETTING, json=body, headers=AUTH)
                assert response.status_code == 400, response.text

    asyncio.run(run())


def test_network_setting_needs_modify_and_reading_needs_view(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            original = public_auth._post
            denied = []

            async def post(url, **kwargs):
                result = await original(url, **kwargs)
                for decision in result.get("results", []):
                    if decision["operation"] == "modify":
                        decision["allowed"] = False
                        result["allowed"] = False
                        denied.append(decision["resource_id"])
                return result

            monkeypatch.setattr(public_auth, "_post", post)
            write = await client.put(NETWORK, json={"instruction": "x"}, headers=AUTH)
            assert write.status_code == 403
            assert denied == ["network-1"]
            assert (await client.get(NETWORK, headers=AUTH)).status_code == 200
            config = await client.get(CONFIG, headers=AUTH)
            assert config.status_code == 200, config.text
            assert config.json()["can_edit_network"] is False

            monkeypatch.setattr(public_auth, "_post", original)
            write = await client.put(
                NETWORK,
                json={"instruction": "Fiscal quarters", "limits": {"max_turns": 5}},
                headers=AUTH,
            )
            assert write.status_code == 200, write.text
            config = (await client.get(CONFIG, headers=AUTH)).json()
            assert config["can_edit_network"] is True
            assert config["network"]["setting"]["instruction"] == "Fiscal quarters"
            assert config["effective"]["limits"]["max_turns"] == 5

    asyncio.run(run())


def test_settings_are_only_for_public_agents(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            response = await client.get(
                "/api/bkn-agent/v1/me/agents/other-agent/setting", headers=AUTH
            )
            assert response.status_code == 404

    asyncio.run(run())


def test_execution_applies_network_and_user_layers(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            seen = {}
            original = executions.stream_chat

            async def chat(session, agent, req, account_id, account_type, *, public):
                seen["limits"] = agent.limits
                seen["instructions"] = public.instructions
                seen["allowed"] = public.allowed_tools
                return await original(
                    session, agent, req, account_id, account_type, public=public
                )

            # The environment's fake chat is what original refers to here.
            monkeypatch.setattr(executions, "stream_chat", chat)
            await client.put(
                NETWORK,
                json={"instruction": "Fiscal quarters", "limits": {"timeout_s": 60}},
                headers=AUTH,
            )
            await client.put(
                SETTING,
                json={"instruction": "Use tables", "allowed_tools": ["q"]},
                headers=AUTH,
            )
            response = await client.post(URL, json=BODY, headers=AUTH)
            assert parse_sse(response.text)[-1][1]["status"] == "succeeded", (
                response.text
            )
            assert seen["limits"].timeout_s == 60
            assert seen["instructions"] == [
                ("network", "Fiscal quarters"),
                ("user", "Use tables"),
            ]
            # The test Agent declares no Context Loader allow-list, so the user's
            # choice is the narrowest layer.
            assert seen["allowed"] == {"q"}

            async with factory() as session:
                from sqlalchemy import select

                from app.models import TaskRow

                task = (await session.execute(select(TaskRow))).scalar_one()
                assert task.f_input["setting_versions"] == {"network": 1, "user": 1}

    asyncio.run(run())


ADMIN = "/api/bkn-agent/v1/admin/agents/studio_smart_qa"


@pytest.mark.parametrize(
    ("original", "fields", "expected"),
    [
        ({"type": "object", "required": ["question"]}, {}, {"type": "object", "required": ["question"]}),
        ({"type": "object"}, {"vars_schema": {"type": "string"}}, {"type": "string"}),
        ({"type": "object"}, {"vars_schema": None}, None),
        ({"type": "object"}, {"vars_schema": {}}, {}),
        ({}, {}, {}),
        (None, {}, None),
    ],
    ids=["omitted", "replaced", "cleared", "empty-replacement", "empty-preserved", "absent"],
)
def test_shared_prompt_publish_preserves_only_omitted_schema(monkeypatch, original, fields, expected):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            seeded = await client.put(
                f"{ADMIN}/prompt",
                json={"content": "Original body", "vars_schema": original},
                headers=AUTH,
            )
            assert seeded.status_code == 200, seeded.text
            published = await client.put(
                f"{ADMIN}/prompt",
                json={"content": "Edited body", **fields},
                headers=AUTH,
            )
            assert published.status_code == 200, published.text
            assert published.json()["prompt"]["vars_schema"] == expected
            current = (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()["prompt"]
            assert current["content"] == "Edited body"
            assert current["vars_schema"] == expected
            assert current["current_version"] == 3

            # Publishing must not mutate either historical Prompt version.
            from app import dao

            async with factory() as session:
                history = await dao.list_prompt_versions(session, "qa-prompt")
            assert [(v.version, v.vars_schema) for v in history] == [(3, expected), (2, original), (1, None)]

    asyncio.run(run())


def test_shared_prompt_publish_inherits_active_schema_after_version_switch(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            schema = {"type": "object", "properties": {"question": {"type": "string"}}}
            for content, value in (("With schema", schema), ("Cleared schema", None)):
                response = await client.put(
                    f"{ADMIN}/prompt", json={"content": content, "vars_schema": value}, headers=AUTH
                )
                assert response.status_code == 200, response.text
            versions = (await client.get(f"{ADMIN}/versions", headers=AUTH)).json()
            active = next(v["version"] for v in versions if v["prompt_version"] == 2)
            switched = await client.post(f"{ADMIN}/versions/{active}/activate", headers=AUTH)
            assert switched.status_code == 200, switched.text
            assert (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()["prompt"]["vars_schema"] == schema

            published = await client.put(
                f"{ADMIN}/prompt", json={"content": "Edit the restored body"}, headers=AUTH
            )
            assert published.status_code == 200, published.text
            assert published.json()["prompt"]["current_version"] == 4
            assert published.json()["prompt"]["vars_schema"] == schema

    asyncio.run(run())


def test_body_only_prompt_publish_keeps_runtime_variable_validation(monkeypatch):
    from fastapi import HTTPException

    from app import dao
    from app.core.prompt import resolve_prompt

    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            seeded = await client.put(
                f"{ADMIN}/prompt",
                json={"content": "Answer {question}", "vars_schema": {"required": ["question"]}},
                headers=AUTH,
            )
            assert seeded.status_code == 200, seeded.text
            edited = await client.put(
                f"{ADMIN}/prompt", json={"content": "Explain {question}"}, headers=AUTH
            )
            assert edited.status_code == 200, edited.text
            async with factory() as session:
                current_agent = await dao.get_agent(session, "studio_smart_qa")
                with pytest.raises(HTTPException) as missing:
                    await resolve_prompt(session, current_agent, "user-1", None, {}, shared_only=True)
                assert missing.value.status_code == 400
                assert missing.value.detail["code"] == "BknAgent.ParamError.PromptVars"
                resolved = await resolve_prompt(
                    session, current_agent, "user-1", None, {"question": "orders"}, shared_only=True
                )
                assert resolved == ("Explain orders", "default", 3)

    asyncio.run(run())


def _deny_agent_admin(monkeypatch):
    """bkn-safe refuses admin-agent:manage; everything else stays allowed."""
    original = public_auth._post

    async def post(url, **kwargs):
        result = await original(url, **kwargs)
        for decision in result.get("results", []):
            if decision["resource_type"] == "admin-agent":
                decision["allowed"] = False
                result["allowed"] = False
        return result

    monkeypatch.setattr(public_auth, "_post", post)
    return original


def test_changing_built_in_agents_needs_admin_agent_manage(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            original = _deny_agent_admin(monkeypatch)
            assert (
                await client.put(ADMIN, json={"model": "m"}, headers=AUTH)
            ).status_code == 403
            assert (
                await client.post(f"{ADMIN}/versions/1/activate", headers=AUTH)
            ).status_code == 403
            assert (await client.post(f"{ADMIN}/restore", headers=AUTH)).status_code == 403
            assert (await client.get(CONFIG, headers=AUTH)).json()[
                "can_edit_shared"
            ] is False

            # Prompt content and version history must not be exposed to an
            # authenticated account without the management permission.
            for path in (
                "/api/bkn-agent/v1/admin/agents",
                ADMIN,
                f"{ADMIN}/versions",
                f"{ADMIN}/prompt",
            ):
                assert (await client.get(path, headers=AUTH)).status_code == 403
            assert (await client.get(ADMIN)).status_code == 401

            # The permission, not authorship, decides: the seeded Agent belongs to
            # "admin" and the caller is still "user-1".
            monkeypatch.setattr(public_auth, "_post", original)
            assert (await client.get(CONFIG, headers=AUTH)).json()[
                "can_edit_shared"
            ] is True
            shared = (await client.get(ADMIN, headers=AUTH)).json()
            assert shared["status"] == "published"
            assert shared["can_edit"] is True
            assert (await client.get(f"{ADMIN}/versions", headers=AUTH)).status_code == 200
            assert (await client.get(f"{ADMIN}/prompt", headers=AUTH)).status_code == 200
            updated = await client.put(
                ADMIN,
                json={
                    "model": "DeepSeek",
                    "limits": {"max_turns": 12},
                    "status": "published",
                },
                headers=AUTH,
            )
            assert updated.status_code == 200, updated.text
            assert updated.json()["model"] == "DeepSeek"
            assert updated.json()["limits"]["max_turns"] == 12
            # Unset fields stay as they were.
            again = await client.put(ADMIN, json={"status": "published"}, headers=AUTH)
            assert again.json()["model"] == "DeepSeek"

    asyncio.run(run())


def test_built_in_agents_cannot_be_unpublished_or_deleted(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            draft = await client.put(ADMIN, json={"status": "draft"}, headers=AUTH)
            assert draft.status_code == 400, draft.text

            internal = "/api/bkn-agent/v1/agents/studio_smart_qa"
            # The seeded owner, so only the built-in rule can refuse these.
            owner = {"x-account-id": "admin", "x-account-type": "user"}
            got = await client.get(internal, headers=owner)
            assert got.status_code == 200, got.text
            spec = got.json()
            spec["status"] = "draft"
            for key in ("create_user", "update_user", "create_time", "update_time"):
                spec.pop(key)
            refused = await client.put(internal, json=spec, headers=owner)
            assert refused.status_code == 400, refused.text
            deleted = await client.delete(internal, headers=owner)
            assert deleted.status_code == 403, deleted.text
            assert (await client.get(internal, headers=owner)).json()["status"] == "published"

    asyncio.run(run())


def test_restore_returns_a_built_in_agent_to_what_it_shipped_with(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            shipped = (await client.get(ADMIN, headers=AUTH)).json()

            changed = await client.put(
                ADMIN,
                json={"model": "DeepSeek", "limits": {"max_turns": 3}},
                headers=AUTH,
            )
            # Its state before the first change became v1, the initial version.
            assert changed.json()["initial_version"] == 1
            assert changed.json()["version"] == 2
            assert changed.json()["modified"] is True
            await client.put(f"{ADMIN}/prompt", json={"content": "Edited"}, headers=AUTH)

            restored = await client.post(f"{ADMIN}/restore", headers=AUTH)
            assert restored.status_code == 200, restored.text
            body = restored.json()
            assert body["model"] == shipped["model"]
            assert body["limits"] == shipped["limits"]
            assert body["prompt_version"] == shipped["prompt_version"]
            assert body["version"] == 1 and body["modified"] is False
            # History is kept.
            versions = (await client.get(f"{ADMIN}/versions", headers=AUTH)).json()
            assert [v["version"] for v in versions] == [3, 2, 1]

    asyncio.run(run())



def test_admin_lists_only_seeded_built_in_agents(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            from app.models import AgentRow

            async with factory() as session:
                qa = await session.get(AgentRow, "studio_smart_qa")
                qa.f_tools = [{"type": "context_loader"}]
                for agent_id, name in (
                    ("catalog-semantic-understanding", "Catalog"),
                    ("user-made", "Not built in"),
                ):
                    session.add(
                        AgentRow(
                            f_agent_id=agent_id,
                            f_name=name,
                            f_mode="task",
                            f_prompt_id=None,
                            f_model="",
                            f_tools=[],
                            f_skills=[],
                            f_limits={"max_turns": 5},
                            f_status="published",
                            f_create_user="user-1",
                            f_update_user="user-1",
                            f_create_time=qa.f_create_time,
                            f_update_time=qa.f_update_time,
                        )
                    )
                await session.commit()

            listed = await client.get("/api/bkn-agent/v1/admin/agents", headers=AUTH)
            assert listed.status_code == 200, listed.text
            items = {item["agent_id"]: item for item in listed.json()}
            # Seeded order, and Agents created through the API are not listed.
            assert list(items) == ["studio_smart_qa", "catalog-semantic-understanding"]
            qa, catalog = items.values()
            assert qa["layered"] is True and qa["uses_context_loader"] is True
            assert qa["can_edit"] is True and qa["prompt_version"] == 1
            assert catalog["layered"] is False
            assert catalog["uses_context_loader"] is False
            assert [t["type"] for t in qa["tools"]] == ["context_loader"]
            assert catalog["tools"] == []
            assert catalog["can_edit"] is True

            # Built-ins outside the public surface are editable here too.
            catalog_url = "/api/bkn-agent/v1/admin/agents/catalog-semantic-understanding"
            updated = await client.put(
                catalog_url, json={"limits": {"max_turns": 3}}, headers=AUTH
            )
            assert updated.status_code == 200, updated.text
            assert updated.json()["limits"]["max_turns"] == 3
            assert (
                await client.get("/api/bkn-agent/v1/admin/agents/user-made", headers=AUTH)
            ).status_code == 404

    asyncio.run(run())


def test_every_change_becomes_an_agent_version_that_can_be_switched_to(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            await client.put(ADMIN, json={"limits": {"max_turns": 7}}, headers=AUTH)
            published = await client.put(
                f"{ADMIN}/prompt", json={"content": "Revised instructions"}, headers=AUTH
            )
            assert published.status_code == 200, published.text
            assert published.json()["prompt"]["current_version"] == 2

            versions = (await client.get(f"{ADMIN}/versions", headers=AUTH)).json()
            assert [v["version"] for v in versions] == [3, 2, 1]
            latest, runtime, first = versions
            assert latest["current"] and latest["changes"] == ["prompt_version"]
            assert latest["prompt_content"] == "Revised instructions"
            assert runtime["changes"] == ["limits"] and runtime["limits"]["max_turns"] == 7
            assert first["initial"] and first["changes"] == []
            # Saving the same configuration again writes nothing.
            await client.put(ADMIN, json={"limits": {"max_turns": 7}}, headers=AUTH)
            assert len((await client.get(f"{ADMIN}/versions", headers=AUTH)).json()) == 3

            # Switching brings runtime and Prompt back together, without a new version.
            switched = await client.post(f"{ADMIN}/versions/2/activate", headers=AUTH)
            assert switched.status_code == 200, switched.text
            assert switched.json()["version"] == 2
            assert switched.json()["limits"]["max_turns"] == 7
            assert switched.json()["prompt_version"] == 1
            prompt = (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()
            assert prompt["prompt"]["current_version"] == 1
            assert len((await client.get(f"{ADMIN}/versions", headers=AUTH)).json()) == 3
            assert (
                await client.post(f"{ADMIN}/versions/9/activate", headers=AUTH)
            ).status_code == 404

    asyncio.run(run())



def test_mcp_profile_switches_the_context_loader_surface(monkeypatch):
    from app.config import config
    from app.core import context_loader

    monkeypatch.setattr(
        config, "CONTEXT_LOADER_MCP_URL", "http://cl:30779/api/agent-retrieval/v1/mcp/"
    )
    assert context_loader.mcp_url() == "http://cl:30779/api/agent-retrieval/v1/mcp/"
    assert (
        context_loader.mcp_url("compact")
        == "http://cl:30779/api/agent-retrieval/v1/mcp-compact/"
    )
    monkeypatch.setattr(
        config,
        "CONTEXT_LOADER_MCP_URL",
        "http://cl/api/agent-retrieval/v1/mcp-compact/",
    )
    assert context_loader.mcp_url("full") == "http://cl/api/agent-retrieval/v1/mcp/"

    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            from app.models import AgentRow

            async with factory() as session:
                row = await session.get(AgentRow, "studio_smart_qa")
                row.f_tools = [{"type": "context_loader", "allowed_tools": ["a"]}]
                await session.commit()
            saved = await client.put(
                ADMIN, json={"mcp_profile": "compact"}, headers=AUTH
            )
            assert saved.json()["mcp_profile"] == "compact"
            assert (await client.get(CONFIG, headers=AUTH)).json()[
                "mcp_profile"
            ] == "compact"
            async with factory() as session:
                row = await session.get(AgentRow, "studio_smart_qa")
                # Other fields of the reference are kept.
                assert row.f_tools == [
                    {
                        "type": "context_loader",
                        "allowed_tools": ["a"],
                        "profile": "compact",
                    }
                ]
            reset = await client.put(ADMIN, json={"mcp_profile": None}, headers=AUTH)
            assert reset.json()["mcp_profile"] is None

    asyncio.run(run())


def test_versions_except_initial_and_current_can_be_deleted(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            for turns in (5, 6):
                await client.put(ADMIN, json={"limits": {"max_turns": turns}}, headers=AUTH)
            assert (await client.get(ADMIN, headers=AUTH)).json()["version"] == 3

            in_use = await client.delete(f"{ADMIN}/versions/3", headers=AUTH)
            assert in_use.status_code == 400, in_use.text
            initial = await client.delete(f"{ADMIN}/versions/1", headers=AUTH)
            assert initial.status_code == 400, initial.text

            deleted = await client.delete(f"{ADMIN}/versions/2", headers=AUTH)
            assert deleted.status_code == 200, deleted.text
            assert [v["version"] for v in deleted.json()] == [3, 1]
            assert (await client.delete(f"{ADMIN}/versions/2", headers=AUTH)).status_code == 404
            assert (
                await client.post(f"{ADMIN}/versions/2/activate", headers=AUTH)
            ).status_code == 404

            # A deleted number is never handed out again, even the newest one.
            await client.post(f"{ADMIN}/versions/1/activate", headers=AUTH)
            assert (await client.delete(f"{ADMIN}/versions/3", headers=AUTH)).status_code == 200
            again = await client.put(ADMIN, json={"limits": {"max_turns": 9}}, headers=AUTH)
            assert again.json()["version"] == 4

            _deny_agent_admin(monkeypatch)
            assert (await client.delete(f"{ADMIN}/versions/4", headers=AUTH)).status_code == 403

    asyncio.run(run())



def test_admin_views_survive_tool_entries_that_are_not_references(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            from app.models import AgentRow

            async with factory() as session:
                row = await session.get(AgentRow, "studio_smart_qa")
                # Hand-edited data: AgentOut lets scalar elements through.
                row.f_tools = ["stray", {"type": "context_loader", "profile": "compact"}]
                await session.commit()

            listed = await client.get("/api/bkn-agent/v1/admin/agents", headers=AUTH)
            assert listed.status_code == 200, listed.text
            assert listed.json()[0]["mcp_profile"] == "compact"
            assert (await client.get(CONFIG, headers=AUTH)).status_code == 200
            switched = await client.put(ADMIN, json={"mcp_profile": "full"}, headers=AUTH)
            assert switched.status_code == 200, switched.text
            assert switched.json()["mcp_profile"] == "full"

    asyncio.run(run())


def test_internal_changes_and_new_agents_are_versioned_too(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            from app import dao

            owner = {"x-account-id": "user-1", "x-account-type": "user"}
            created = await client.post(
                "/api/bkn-agent/v1/agents",
                json={"name": "custom", "mode": "chat", "model": "m1"},
                headers=owner,
            )
            assert created.status_code == 200, created.text
            agent_id = created.json()["agent_id"]
            spec = {"name": "custom", "mode": "chat", "model": "m2"}
            updated = await client.put(
                f"/api/bkn-agent/v1/agents/{agent_id}", json=spec, headers=owner
            )
            assert updated.status_code == 200, updated.text

            async with factory() as session:
                versions = await dao.list_versions(session, agent_id)
            assert [(v.f_version, v.f_model, v.f_current) for v in versions] == [
                (2, "m2", True),
                (1, "m1", False),
            ]
            # Not a built-in: there is no initial version to restore to.
            assert not any(v.f_initial for v in versions)

    asyncio.run(run())


def test_execution_records_the_agent_version_it_ran(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await client.put(ADMIN, json={"limits": {"max_turns": 8}}, headers=AUTH)
            response = await client.post(URL, json=BODY, headers=AUTH)
            assert parse_sse(response.text)[-1][1]["status"] == "succeeded", response.text

            async with factory() as session:
                from sqlalchemy import select

                from app.models import TaskRow

                task = (await session.execute(select(TaskRow))).scalar_one()
                assert task.f_input["agent_version"] == 2

    asyncio.run(run())


def test_a_stray_second_current_version_neither_breaks_reads_nor_survives_a_save(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            from sqlalchemy import select

            from app import dao
            from app.models import AgentVersionRow

            await client.put(ADMIN, json={"limits": {"max_turns": 6}}, headers=AUTH)
            async with factory() as session:
                # What two racing writers could once leave behind.
                first = await session.get(AgentVersionRow, ("studio_smart_qa", 1))
                first.f_current = True
                await session.commit()

            read = await client.get(ADMIN, headers=AUTH)
            assert read.status_code == 200, read.text
            assert read.json()["version"] == 2
            response = await client.post(URL, json=BODY, headers=AUTH)
            assert parse_sse(response.text)[-1][1]["status"] == "succeeded", response.text

            await client.put(ADMIN, json={"limits": {"max_turns": 7}}, headers=AUTH)
            async with factory() as session:
                current = (
                    await session.execute(
                        select(AgentVersionRow.f_version).where(
                            AgentVersionRow.f_agent_id == "studio_smart_qa",
                            AgentVersionRow.f_current.is_(True),
                        )
                    )
                ).scalars().all()
                assert current == [3]
                assert (await dao.current_version(session, "studio_smart_qa")).f_version == 3

    asyncio.run(run())


def test_activating_the_version_a_request_started_on_keeps_it_current(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            from app import dao

            await client.put(ADMIN, json={"limits": {"max_turns": 6}}, headers=AUTH)
            async with factory() as session:
                # Loaded while v2 is current, as _shared_editor does at the start.
                loaded = await dao.current_version(session, "studio_smart_qa")
                assert loaded.f_version == 2
                # Meanwhile another request switches to v1.
                async with factory() as other:
                    await dao.activate_version(other, "studio_smart_qa", 1, "admin-2")
                # This request now activates the version it started on.
                await dao.activate_version(session, "studio_smart_qa", 2, "admin-1")
            async with factory() as session:
                current = await dao.current_version(session, "studio_smart_qa")
                assert current is not None and current.f_version == 2
                versions = await dao.list_versions(session, "studio_smart_qa")
                assert [v.f_version for v in versions if v.f_current] == [2]

    asyncio.run(run())


def test_repeated_prompt_rebinding_returns_conflict_without_writes(monkeypatch):
    from app import dao

    async def run():
        async with environment(monkeypatch) as (client, _, _):
            before = (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()
            versions = (await client.get(f"{ADMIN}/versions", headers=AUTH)).json()
            calls = 0

            async def binding_changed(session, prompt_id):
                nonlocal calls
                calls += 1
                return []

            monkeypatch.setattr(dao, "lock_prompt_agents", binding_changed)
            response = await client.put(f"{ADMIN}/prompt",
                json={"content": "must not publish"}, headers=AUTH)
            assert response.status_code == 409, response.text
            assert response.json()["code"] == "BknAgent.ParamError.Conflict"
            assert calls == 3
            assert (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json() == before
            assert (await client.get(f"{ADMIN}/versions", headers=AUTH)).json() == versions

    asyncio.run(run())
