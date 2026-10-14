"""Installation tests use an isolated SQLite file, never a deployed database."""

import asyncio
import json
from pathlib import Path

import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from app import dao
from app.bootstrap.preset_models import (
    PresetInstallationRow,
    PresetVersionRow,
    ScenarioBindingRow,
)
from app.bootstrap.presets import (
    PresetError,
    PresetPackage,
    adopt,
    initialize,
    load_package,
    preview,
)
from app.bootstrap.studio import DEFAULT_MANIFEST, bootstrap_manifest, execute, parser
from app.models import AgentRow, Base, PromptOverrideRow, PromptRow, PromptVersionRow

OWNER = "preset-system-owner"
ACTOR = "deployment-operator"
READY = {"model": "ready", "context_loader": "ready", "studio_interactions": "missing"}
PACKAGE = DEFAULT_MANIFEST.parent / "studio-smart-qa.json"


@pytest.fixture
def sessions(tmp_path):
    engine = create_async_engine(f"sqlite+aiosqlite:///{tmp_path / 'presets.db'}")
    factory = async_sessionmaker(engine, expire_on_commit=False)

    async def create():
        async with engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)

    asyncio.run(create())
    yield factory
    asyncio.run(engine.dispose())


def package(version="1", content=None):
    payload = json.loads(PACKAGE.read_text())
    payload["version"] = version
    if content is not None:
        payload["prompt"]["content"] = content
    return PresetPackage.model_validate(payload)


async def install(sessions, preset=None, **kwargs):
    return await initialize(
        sessions,
        preset or package(),
        owner=OWNER,
        actor=ACTOR,
        installed_features=set(),
        dependencies=READY,
        **kwargs,
    )


async def counts(sessions):
    async with sessions() as session:
        return [
            (await session.execute(select(func.count()).select_from(row))).scalar_one()
            for row in (
                AgentRow,
                PromptRow,
                PromptVersionRow,
                PresetInstallationRow,
                PresetVersionRow,
                ScenarioBindingRow,
            )
        ]


def test_first_install_repeated_and_upgrade_preserve_defaults(sessions):
    async def run():
        assert (await install(sessions))["action"] == "created"
        assert (await install(sessions))["action"] == "preserved"
        upgraded = await install(sessions, package("2", "new official prompt"))
        assert upgraded["action"] == "preserved"
        assert upgraded["update_available"]
        assert upgraded["applied_version"] == "1"
        assert await counts(sessions) == [1, 1, 1, 1, 2, 1]
        async with sessions() as session:
            current = await session.get(
                PromptVersionRow, (package().prompt.prompt_id, 1)
            )
            assert current.f_content == package().prompt.content

    asyncio.run(run())


def test_repeated_install_preserves_admin_edits_custom_binding_and_disable(sessions):
    async def run():
        await install(sessions)
        async with sessions() as session, session.begin():
            agent = await session.get(AgentRow, package().spec.agent_id)
            agent.f_model = "customer-model"
            agent.f_tools = []
            agent.f_limits = {"max_turns": 2}
            binding = await session.get(ScenarioBindingRow, "smart_qa")
            binding.f_agent_id = "custom-agent"
            binding.f_enabled = 0
            await dao.publish_prompt_version(
                session, agent.f_prompt_id, "admin edited", None, "admin", commit=False
            )
        await install(sessions, package("2", "official update"))
        async with sessions() as session:
            agent = await session.get(AgentRow, package().spec.agent_id)
            prompt = await session.get(PromptRow, agent.f_prompt_id)
            binding = await session.get(ScenarioBindingRow, "smart_qa")
            assert (agent.f_model, agent.f_tools, agent.f_limits, agent.f_status) == (
                "customer-model",
                [],
                {"max_turns": 2},
                "draft",
            )
            assert prompt.f_current_version == 2
            assert (binding.f_agent_id, binding.f_enabled) == ("custom-agent", 0)

    asyncio.run(run())


@pytest.mark.parametrize("removed", ["agent", "prompt", "binding"])
def test_removed_resources_are_not_resurrected(sessions, removed):
    async def run():
        await install(sessions)
        row_type, key = {
            "agent": (AgentRow, package().spec.agent_id),
            "prompt": (PromptRow, package().prompt.prompt_id),
            "binding": (ScenarioBindingRow, "smart_qa"),
        }[removed]
        async with sessions() as session, session.begin():
            await session.delete(await session.get(row_type, key))
        result = await install(sessions)
        assert {"agent": "spec", "prompt": "prompt", "binding": "binding"}[
            removed
        ] in result["missing_resources"]
        async with sessions() as session:
            assert await session.get(row_type, key) is None
            assert await session.get(PresetInstallationRow, "smart_qa") is not None

    asyncio.run(run())


def test_first_install_keeps_preexisting_custom_binding(sessions):
    async def run():
        async with sessions() as session, session.begin():
            session.add(
                ScenarioBindingRow(
                    f_scenario="smart_qa",
                    f_agent_id="custom",
                    f_enabled=0,
                    f_revision=7,
                    f_update_user="admin",
                    f_update_time=1,
                )
            )
        await install(sessions)
        async with sessions() as session:
            binding = await session.get(ScenarioBindingRow, "smart_qa")
            assert (binding.f_agent_id, binding.f_enabled, binding.f_revision) == (
                "custom",
                0,
                7,
            )

    asyncio.run(run())


def test_foreign_fixed_id_is_not_adopted_even_when_identical(sessions):
    async def run():
        async with sessions() as session:
            await dao.upsert_agent_with_id(
                session, package().spec.agent_id, package().spec, OWNER
            )
        with pytest.raises(PresetError, match="without preset provenance"):
            await install(sessions)
        assert await counts(sessions) == [1, 0, 0, 0, 0, 0]

    asyncio.run(run())


def test_name_collision_does_not_create_prompt_or_catalog(sessions):
    async def run():
        async with sessions() as session:
            await dao.upsert_agent_with_id(
                session, "foreign", package().spec, "foreign-owner"
            )
        with pytest.raises(PresetError):
            await install(sessions)
        assert await counts(sessions) == [1, 0, 0, 0, 0, 0]

    asyncio.run(run())


def test_failure_after_prompt_flush_rolls_back_all_resources(sessions, monkeypatch):
    async def fail(*args, **kwargs):
        raise PresetError("simulated agent failure")

    monkeypatch.setattr(dao, "upsert_agent_with_id", fail)

    async def run():
        with pytest.raises(PresetError, match="simulated"):
            await install(sessions)
        assert await counts(sessions) == [0, 0, 0, 0, 0, 0]

    asyncio.run(run())


def test_concurrent_initializers_create_one_installation(sessions):
    async def run():
        results = await asyncio.gather(*(install(sessions) for _ in range(4)))
        assert [r["action"] for r in results].count("created") == 1
        assert [r["action"] for r in results].count("preserved") == 3
        assert await counts(sessions) == [1, 1, 1, 1, 1, 1]

    asyncio.run(run())


def test_reusing_version_with_changed_content_is_rejected(sessions):
    async def run():
        await install(sessions)
        with pytest.raises(PresetError, match="cannot change content"):
            await install(sessions, package(content="tampered same version"))
        assert await counts(sessions) == [1, 1, 1, 1, 1, 1]

    asyncio.run(run())


def test_explicit_adoption_preserves_binding_enablement_and_personal_override(sessions):
    async def run():
        await install(sessions)
        async with sessions() as session, session.begin():
            agent = await session.get(AgentRow, package().spec.agent_id)
            agent.f_model = "admin-model"
            await dao.publish_prompt_version(
                session, agent.f_prompt_id, "admin prompt", None, "admin", commit=False
            )
            binding = await session.get(ScenarioBindingRow, "smart_qa")
            binding.f_agent_id = "custom"
            binding.f_enabled = 0
            session.add(
                PromptOverrideRow(
                    f_agent_id=agent.f_agent_id,
                    f_account_id="user-1",
                    f_content="personal",
                    f_update_time=1,
                )
            )
        await install(sessions, package("2", "official v2"))
        comparison = await preview(sessions, "studio-smart-qa", "2", owner=OWNER)
        assert comparison["current"]["spec"]["model"] == "admin-model"
        assert comparison["candidate"]["prompt"]["content"] == "official v2"
        await adopt(
            sessions,
            "studio-smart-qa",
            "2",
            owner=OWNER,
            actor="admin",
            expected_digest=comparison["expected_digest"],
            installed_features=set(),
        )
        async with sessions() as session:
            binding = await session.get(ScenarioBindingRow, "smart_qa")
            agent = await session.get(AgentRow, package().spec.agent_id)
            prompt = await session.get(PromptRow, agent.f_prompt_id)
            override = await session.get(
                PromptOverrideRow, (agent.f_agent_id, "user-1")
            )
            installation = await session.get(PresetInstallationRow, "smart_qa")
            assert (binding.f_agent_id, binding.f_enabled) == ("custom", 0)
            assert agent.f_model == ""
            assert agent.f_status == "draft"
            assert prompt.f_current_version == 3
            assert override.f_content == "personal"
            assert (
                installation.f_applied_version,
                installation.f_update_user,
                installation.f_revision,
            ) == ("2", "admin", 2)

    asyncio.run(run())


@pytest.mark.parametrize("edit", ["agent", "prompt", "binding"])
def test_stale_adoption_preview_cannot_overwrite_concurrent_edit(sessions, edit):
    async def run():
        await install(sessions)
        await install(sessions, package("2", "official v2"))
        comparison = await preview(sessions, "studio-smart-qa", "2", owner=OWNER)
        async with sessions() as session, session.begin():
            if edit == "agent":
                agent = await session.get(AgentRow, package().spec.agent_id)
                agent.f_model = "changed-after-preview"
            elif edit == "prompt":
                await dao.publish_prompt_version(
                    session,
                    package().prompt.prompt_id,
                    "changed-after-preview",
                    None,
                    "admin",
                    commit=False,
                )
            else:
                binding = await session.get(ScenarioBindingRow, "smart_qa")
                binding.f_enabled = 0
        with pytest.raises(PresetError, match="changed after preview"):
            await adopt(
                sessions,
                "studio-smart-qa",
                "2",
                owner=OWNER,
                actor="admin",
                expected_digest=comparison["expected_digest"],
                installed_features=set(),
            )
        async with sessions() as session:
            installation = await session.get(PresetInstallationRow, "smart_qa")
            assert installation.f_applied_version == "1"

    asyncio.run(run())


def test_adoption_failure_keeps_prompt_and_applied_version(sessions, monkeypatch):
    async def run():
        await install(sessions)
        await install(sessions, package("2", "official v2"))
        comparison = await preview(sessions, "studio-smart-qa", "2", owner=OWNER)

        async def fail(*args, **kwargs):
            raise PresetError("simulated adoption failure")

        monkeypatch.setattr(dao, "upsert_agent_with_id", fail)
        with pytest.raises(PresetError, match="simulated"):
            await adopt(
                sessions,
                "studio-smart-qa",
                "2",
                owner=OWNER,
                actor="admin",
                expected_digest=comparison["expected_digest"],
                installed_features=set(),
            )
        async with sessions() as session:
            prompt = await session.get(PromptRow, package().prompt.prompt_id)
            installation = await session.get(PresetInstallationRow, "smart_qa")
            assert prompt.f_current_version == 1
            assert installation.f_applied_version == "1"

    asyncio.run(run())


def test_initialization_reports_missing_dependencies_without_losing_definitions(
    sessions,
):
    async def run():
        result = await install(sessions)
        assert result["action"] == "created"
        assert result["readiness"]["state"] == "missing"
        assert not result["executable"]
        recovered = await initialize(
            sessions,
            package(),
            owner=OWNER,
            actor=ACTOR,
            installed_features=set(),
            dependencies={k: "ready" for k in READY},
        )
        assert recovered["action"] == "preserved"
        assert recovered["readiness"]["state"] == "ready"
        assert await counts(sessions) == [1, 1, 1, 1, 1, 1]

    asyncio.run(run())


def test_manifest_initializes_available_scenarios_and_skips_uninstalled_graph(sessions):
    async def run():
        result = await bootstrap_manifest(
            sessions,
            DEFAULT_MANIFEST,
            owner=OWNER,
            actor=ACTOR,
            installed_features=set(),
            dependencies={},
        )
        assert [r["action"] for r in result["results"]] == [
            "created",
            "created",
            "skipped",
        ]
        assert result["results"][0]["readiness"]["state"] == "unknown"
        assert await counts(sessions) == [2, 2, 2, 2, 2, 2]
        result = await bootstrap_manifest(
            sessions,
            DEFAULT_MANIFEST,
            owner=OWNER,
            actor=ACTOR,
            installed_features={"graph_explorer_adapter"},
            dependencies={},
        )
        assert result["results"][2]["action"] == "failed"
        assert await counts(sessions) == [2, 2, 2, 2, 2, 2]

    asyncio.run(run())


def test_failed_scenario_does_not_block_other_scenarios(sessions):
    async def run():
        async with sessions() as session:
            await dao.upsert_agent_with_id(
                session, package().spec.agent_id, package().spec, "foreign"
            )
        result = await bootstrap_manifest(
            sessions,
            DEFAULT_MANIFEST,
            owner=OWNER,
            actor=ACTOR,
            installed_features=set(),
            dependencies={},
        )
        assert [r["action"] for r in result["results"]] == [
            "failed",
            "created",
            "skipped",
        ]

    asyncio.run(run())


def test_wrong_management_owner_cannot_preview_or_adopt(sessions):
    async def run():
        await install(sessions)
        with pytest.raises(PresetError, match="owner"):
            await preview(sessions, "studio-smart-qa", "1", owner="foreign")

    asyncio.run(run())


def test_builtin_tools_exist_and_packages_have_no_embedded_environment_urls():
    schemas = (
        Path(__file__).resolve().parents[4]
        / "adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas"
    )
    for filename in ("studio-smart-qa.json", "studio-base-data-qa.json"):
        preset = load_package(DEFAULT_MANIFEST.parent / filename)
        assert preset.spec.model == ""
        assert preset.spec.status == "draft"
        assert "http" not in (DEFAULT_MANIFEST.parent / filename).read_text()
        for tool_ref in preset.spec.tools:
            assert tool_ref["type"] == "context_loader"
            assert tool_ref["allowed_tools"]
            assert all(
                (schemas / f"{name}.json").exists()
                for name in tool_ref["allowed_tools"]
            )


def test_cli_rejects_invalid_dependency_before_writing(sessions):
    args = parser().parse_args(
        [
            "initialize",
            "--owner-id",
            OWNER,
            "--actor-id",
            ACTOR,
            "--dependency",
            "model=unvalidated",
        ]
    )

    async def run():
        with pytest.raises(PresetError, match="--dependency"):
            await execute(args, sessions)
        assert await counts(sessions) == [0, 0, 0, 0, 0, 0]

    asyncio.run(run())


def test_deleted_resources_cannot_be_restored_by_adoption(sessions):
    async def run():
        await install(sessions)
        await install(sessions, package("2", "official v2"))
        async with sessions() as session, session.begin():
            await session.delete(await session.get(AgentRow, package().spec.agent_id))
        comparison = await preview(sessions, "studio-smart-qa", "2", owner=OWNER)
        with pytest.raises(PresetError, match="explicit recovery"):
            await adopt(
                sessions,
                "studio-smart-qa",
                "2",
                owner=OWNER,
                actor="admin",
                expected_digest=comparison["expected_digest"],
                installed_features=set(),
            )
        async with sessions() as session:
            assert await session.get(AgentRow, package().spec.agent_id) is None

    asyncio.run(run())


def test_missing_optional_feature_makes_no_database_writes(sessions):
    async def run():
        payload = package().model_dump()
        payload["required_features"] = ["optional-adapter"]
        result = await install(sessions, PresetPackage.model_validate(payload))
        assert result["action"] == "skipped"
        assert await counts(sessions) == [0, 0, 0, 0, 0, 0]

    asyncio.run(run())


def test_rollback_to_registered_default_version_preserves_prompt_history(sessions):
    async def run():
        await install(sessions)
        await install(sessions, package("2", "official v2"))
        for version in ("2", "1"):
            comparison = await preview(
                sessions, "studio-smart-qa", version, owner=OWNER
            )
            await adopt(
                sessions,
                "studio-smart-qa",
                version,
                owner=OWNER,
                actor="admin",
                expected_digest=comparison["expected_digest"],
                installed_features=set(),
            )
        async with sessions() as session:
            prompt = await session.get(PromptRow, package().prompt.prompt_id)
            current = await session.get(
                PromptVersionRow, (prompt.f_prompt_id, prompt.f_current_version)
            )
            assert prompt.f_current_version == 3
            assert current.f_content == package().prompt.content

    asyncio.run(run())


@pytest.mark.parametrize("problem", ["duplicate", "traversal", "invalid_package"])
def test_manifest_validation_finishes_before_any_scenario_is_created(
    sessions, tmp_path, problem
):
    definition = json.loads(DEFAULT_MANIFEST.read_text())
    definition["scenarios"][0]["package"] = "valid.json"
    (tmp_path / "valid.json").write_text(PACKAGE.read_text())
    if problem == "duplicate":
        definition["scenarios"][1] = definition["scenarios"][0]
    elif problem == "traversal":
        definition["scenarios"][1]["package"] = "../external.json"
    else:
        definition["scenarios"][1]["package"] = "invalid.json"
        (tmp_path / "invalid.json").write_text("{}")
    manifest = tmp_path / "manifest.json"
    manifest.write_text(json.dumps(definition))

    async def run():
        with pytest.raises((PresetError, ValueError)):
            await bootstrap_manifest(
                sessions,
                manifest,
                owner=OWNER,
                actor=ACTOR,
                installed_features=set(),
                dependencies={},
            )
        assert await counts(sessions) == [0, 0, 0, 0, 0, 0]

    asyncio.run(run())
