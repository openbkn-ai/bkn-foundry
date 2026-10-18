# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Network and user settings: merge rules, ownership and permissions, and use in runs."""

import asyncio

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


async def _make_creator(factory, account_id):
    from app.models import AgentRow

    async with factory() as session:
        row = await session.get(AgentRow, "studio_smart_qa")
        row.f_create_user = account_id
        await session.commit()


def test_only_the_creator_changes_shared_configuration_for_now(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            # The seeded Agent belongs to "admin"; the caller is "user-1".
            assert (
                await client.put(ADMIN, json={"model": "m"}, headers=AUTH)
            ).status_code == 403
            assert (
                await client.post(
                    f"{ADMIN}/prompt/rollback", json={"version": 1}, headers=AUTH
                )
            ).status_code == 403
            assert (await client.get(CONFIG, headers=AUTH)).json()[
                "can_edit_shared"
            ] is False

            # Reading stays open, flagged read-only.
            read = await client.get(ADMIN, headers=AUTH)
            assert read.status_code == 200, read.text
            assert read.json()["can_edit"] is False
            assert (await client.get(f"{ADMIN}/prompt", headers=AUTH)).status_code == 200
            await _make_creator(factory, "user-1")
            assert (await client.get(CONFIG, headers=AUTH)).json()[
                "can_edit_shared"
            ] is True
            shared = (await client.get(ADMIN, headers=AUTH)).json()
            assert shared["status"] == "published"
            assert shared["can_edit"] is True
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
            assert qa["can_edit"] is False and qa["prompt_version"] == 1
            assert catalog["layered"] is False
            assert catalog["uses_context_loader"] is False
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


def test_shared_prompt_publishes_versions_and_rolls_back(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            await _make_creator(factory, "user-1")
            current = (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()
            assert current["prompt"]["current_version"] == 1

            published = await client.put(
                f"{ADMIN}/prompt",
                json={"content": "Revised instructions"},
                headers=AUTH,
            )
            assert published.status_code == 200, published.text
            body = published.json()
            assert body["prompt"]["current_version"] == 2
            assert body["prompt"]["content"] == "Revised instructions"
            assert [v["version"] for v in body["versions"]] == [2, 1]

            rolled = await client.post(
                f"{ADMIN}/prompt/rollback", json={"version": 1}, headers=AUTH
            )
            assert rolled.json()["prompt"]["current_version"] == 1
            missing = await client.post(
                f"{ADMIN}/prompt/rollback", json={"version": 9}, headers=AUTH
            )
            assert missing.status_code == 404

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
            await _make_creator(factory, "user-1")
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
