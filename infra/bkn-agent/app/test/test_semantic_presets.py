"""Legacy SQL parity and enrollment tests; no deployed database is accessed."""

import asyncio
import json
import sqlite3
from pathlib import Path

import pytest
from sqlalchemy import select

from app import dao
from app.bootstrap.preset_models import PresetInstallationRow, ScenarioBindingRow
from app.bootstrap.presets import (
    PresetError,
    enroll,
    initialize,
    inspect_legacy,
    load_package,
    preview,
)
from app.bootstrap.studio import execute, parser
from app.models import AgentRow, PromptRow, PromptVersionRow
from app.test import test_studio_presets as preset_tests

sessions = preset_tests.sessions

ROOT = Path(__file__).resolve().parents[4]
PACKAGES = ROOT / "infra/bkn-agent/deploy/agents"
OWNER = "266c6a42-6131-4d62-8f39-853e7093701c"
ACTOR = "deployment-operator"
IDS = ("resource-semantic-understanding", "catalog-semantic-understanding")


def legacy_rows(path):
    """Evaluate the actual seed DML, including CONCAT and MySQL newlines."""
    db = sqlite3.connect(":memory:")
    db.row_factory = sqlite3.Row
    db.create_function("concat", -1, lambda *parts: "".join(parts))
    db.create_function("now", 1, lambda _: 1)
    db.create_function("unix_timestamp", 1, lambda _: 1)
    db.executescript("""
        CREATE TABLE dual(dummy INTEGER); INSERT INTO dual VALUES(1);
        CREATE TABLE t_agent(f_agent_id TEXT PRIMARY KEY, f_name TEXT, f_mode TEXT,
            f_prompt_id TEXT, f_prompt_vars_schema TEXT, f_model TEXT, f_tools TEXT,
            f_skills TEXT, f_limits TEXT, f_status TEXT, f_create_user TEXT,
            f_update_user TEXT, f_create_time INTEGER, f_update_time INTEGER);
        CREATE TABLE t_agent_prompt(f_prompt_id TEXT PRIMARY KEY, f_name TEXT,
            f_current_version INTEGER, f_update_user TEXT, f_update_time INTEGER);
        CREATE TABLE t_agent_prompt_version(f_prompt_id TEXT, f_version INTEGER,
            f_content TEXT, f_vars_schema TEXT, f_create_user TEXT, f_create_time INTEGER,
            PRIMARY KEY(f_prompt_id, f_version));
    """)
    statement = ""
    for line in path.read_text().splitlines(keepends=True):
        if line.lstrip().startswith("--"):
            continue
        statement += line
        if sqlite3.complete_statement(statement):
            if statement.strip().lower().startswith("insert into t_agent"):
                db.execute(statement.replace("\\n", "\n"))
            statement = ""
    rows = []
    for agent_id in IDS:
        agent = dict(
            db.execute(
                "SELECT * FROM t_agent WHERE f_agent_id=?", (agent_id,)
            ).fetchone()
        )
        prompt = dict(
            db.execute(
                "SELECT * FROM t_agent_prompt WHERE f_prompt_id=?",
                (agent["f_prompt_id"],),
            ).fetchone()
        )
        version = dict(
            db.execute(
                "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id=? AND f_version=?",
                (prompt["f_prompt_id"], prompt["f_current_version"]),
            ).fetchone()
        )
        spec = {
            key.removeprefix("f_"): agent[key]
            for key in (
                "f_agent_id",
                "f_name",
                "f_mode",
                "f_prompt_id",
                "f_model",
                "f_status",
            )
        }
        for key in ("tools", "skills", "limits"):
            spec[key] = json.loads(agent[f"f_{key}"])
        rows.append(
            (
                spec,
                {
                    "prompt_id": prompt["f_prompt_id"],
                    "name": prompt["f_name"],
                    "content": version["f_content"],
                },
                prompt["f_current_version"],
            )
        )
    db.close()
    return rows


def package(agent_id=IDS[0]):
    return load_package(PACKAGES / f"{agent_id}.preset.json")


async def seed(sessions, preset):
    async with sessions() as session, session.begin():
        await dao.upsert_prompt_with_id(
            session,
            preset.prompt.prompt_id,
            preset.prompt.name,
            preset.prompt.content,
            preset.prompt.vars_schema,
            OWNER,
            commit=False,
        )
        await dao.upsert_agent_with_id(
            session, preset.spec.agent_id, preset.spec, OWNER, commit=False
        )
        version_number = 3 if preset.spec.agent_id == IDS[0] else 2
        head = await session.get(PromptRow, preset.prompt.prompt_id)
        version = await session.get(PromptVersionRow, (preset.prompt.prompt_id, 1))
        version.f_version = version_number
        head.f_current_version = version_number


async def snapshot(sessions):
    async with sessions() as session:
        result = []
        for model in (AgentRow, PromptRow, PromptVersionRow, ScenarioBindingRow):
            result.append(
                [
                    {
                        column.name: getattr(row, column.name)
                        for column in model.__table__.columns
                    }
                    for row in (await session.execute(select(model))).scalars()
                ]
            )
        return result


def test_packages_match_existing_sql_seed_and_keep_old_snapshot():
    previous = ROOT / "migrations/bkn-agent/mariadb/0.2.0/init.sql"
    current = ROOT / "migrations/bkn-agent/mariadb/0.2.1/init.sql"
    assert legacy_rows(previous) == legacy_rows(current)
    for spec, prompt, version in legacy_rows(previous):
        preset = package(spec["agent_id"])
        assert preset.spec.model_dump(exclude_none=True) == spec
        assert preset.prompt.model_dump(exclude_none=True) == prompt
        assert version == (3 if spec["agent_id"] == IDS[0] else 2)
        assert preset.expected_owner == OWNER
        assert preset.bind_scenario is False
        assert preset.installation_policy == "enroll_existing"
        assert spec["status"] == "published" and spec["tools"] == []


@pytest.mark.parametrize("agent_id", IDS)
def test_enrollment_preserves_legacy_configuration_and_prompt_version(
    sessions, agent_id
):
    async def run():
        preset = package(agent_id)
        await seed(sessions, preset)
        before = await snapshot(sessions)
        comparison = await inspect_legacy(sessions, preset, owner=OWNER)
        result = await enroll(
            sessions,
            preset,
            owner=OWNER,
            actor=ACTOR,
            expected_digest=comparison["expected_digest"],
        )
        assert result["baseline"] == "matched"
        assert await snapshot(sessions) == before
        assert (
            await enroll(
                sessions,
                preset,
                owner=OWNER,
                actor=ACTOR,
                expected_digest=comparison["expected_digest"],
            )
        )["action"] == "preserved"
        status = await initialize(
            sessions,
            preset,
            owner=OWNER,
            actor=ACTOR,
            installed_features=set(),
            dependencies={},
        )
        assert status["missing_resources"] == []
        assert await snapshot(sessions) == before

    asyncio.run(run())


def test_modified_legacy_baseline_is_not_invented_or_overwritten(sessions):
    async def run():
        preset = package()
        await seed(sessions, preset)
        async with sessions() as session, session.begin():
            agent = await session.get(AgentRow, preset.spec.agent_id)
            agent.f_model = "customer-model"
            agent.f_status = "draft"
            await dao.publish_prompt_version(
                session,
                preset.prompt.prompt_id,
                "customer prompt",
                None,
                "administrator",
                commit=False,
            )
        before = await snapshot(sessions)
        comparison = await inspect_legacy(sessions, preset, owner=OWNER)
        assert comparison["baseline"] == "legacy_modified"
        result = await enroll(
            sessions,
            preset,
            owner=OWNER,
            actor=ACTOR,
            expected_digest=comparison["expected_digest"],
        )
        assert result["applied_version"] is None
        async with sessions() as session:
            installed = await session.get(PresetInstallationRow, preset.scenario)
            assert installed.f_applied_version == installed.f_applied_digest == ""
        assert (
            await preview(sessions, preset.package_id, preset.version, owner=OWNER)
        )["applied_version"] is None
        assert await snapshot(sessions) == before

    asyncio.run(run())


def test_enrollment_rejects_stale_preview_wrong_owner_and_deleted_resources(sessions):
    async def run():
        preset = package()
        await seed(sessions, preset)
        comparison = await inspect_legacy(sessions, preset, owner=OWNER)
        with pytest.raises(PresetError, match="owner"):
            await inspect_legacy(sessions, preset, owner="foreign")
        async with sessions() as session, session.begin():
            agent = await session.get(AgentRow, preset.spec.agent_id)
            agent.f_model = "edited-after-preview"
        with pytest.raises(PresetError, match="changed after preview"):
            await enroll(
                sessions,
                preset,
                owner=OWNER,
                actor=ACTOR,
                expected_digest=comparison["expected_digest"],
            )
        async with sessions() as session, session.begin():
            await session.delete(await session.get(AgentRow, preset.spec.agent_id))
        with pytest.raises(PresetError, match="missing"):
            await inspect_legacy(sessions, preset, owner=OWNER)
        with pytest.raises(PresetError, match="provenance"):
            await initialize(
                sessions, preset, owner=OWNER, actor=ACTOR, installed_features=set()
            )

    asyncio.run(run())


def test_legacy_package_does_not_recreate_missing_agent_on_fresh_database(sessions):
    async def run():
        with pytest.raises(PresetError, match="explicit enrollment"):
            await initialize(
                sessions, package(), owner=OWNER, actor=ACTOR, installed_features=set()
            )

    asyncio.run(run())


def test_legacy_commands_require_preview_and_use_same_registration_engine(sessions):
    async def run():
        preset = package()
        await seed(sessions, preset)
        common = [
            "--owner-id",
            OWNER,
            "--package",
            str(PACKAGES / f"{IDS[0]}.preset.json"),
        ]
        comparison = await execute(
            parser().parse_args(["inspect-legacy", *common]), sessions
        )
        args = parser().parse_args(["enroll", *common, "--actor-id", ACTOR])
        with pytest.raises(PresetError, match="expected-digest"):
            await execute(args, sessions)
        args.expected_digest = comparison["expected_digest"]
        assert (await execute(args, sessions))["action"] == "enrolled"

    asyncio.run(run())


def test_existing_v1_catalog_digest_survives_new_optional_metadata(sessions):
    from app.bootstrap.presets import digest

    old = load_package(ROOT / "infra/bkn-agent/fixtures/studio/smart-qa-v1.json")
    assert (
        digest(old.model_dump(exclude_none=True))
        == "6de24f00e5634ac5d8239fe0b2b069305fc8124bbec945b587464a0cd33e1970"
    )

    async def run():
        await initialize(
            sessions, old, owner=OWNER, actor=ACTOR, installed_features=set()
        )
        current = load_package(PACKAGES / "studio-smart-qa.json")
        assert current.version == "2"
        result = await initialize(
            sessions, current, owner=OWNER, actor=ACTOR, installed_features=set()
        )
        assert result["action"] == "preserved" and result["update_available"]
        assert result["applied_version"] == "1"
        async with sessions() as session:
            agent = await session.get(AgentRow, old.spec.agent_id)
            assert agent.f_tools == old.spec.tools

    asyncio.run(run())


def test_concurrent_enrollment_is_atomic_and_idempotent(sessions):
    async def run():
        preset = package()
        await seed(sessions, preset)
        before = await snapshot(sessions)
        comparison = await inspect_legacy(sessions, preset, owner=OWNER)
        results = await asyncio.gather(
            *(
                enroll(
                    sessions,
                    preset,
                    owner=OWNER,
                    actor=ACTOR,
                    expected_digest=comparison["expected_digest"],
                )
                for _ in range(4)
            )
        )
        assert [r["action"] for r in results].count("enrolled") == 1
        assert [r["action"] for r in results].count("preserved") == 3
        assert await snapshot(sessions) == before

    asyncio.run(run())


def test_enrollment_failure_rolls_back_catalog_and_provenance(sessions, monkeypatch):
    from app.bootstrap import presets
    from app.bootstrap.preset_models import PresetVersionRow

    original = presets._catalog

    async def fail_after_catalog(*args):
        await original(*args)
        raise PresetError("injected enrollment failure")

    async def run():
        preset = package()
        await seed(sessions, preset)
        before = await snapshot(sessions)
        comparison = await inspect_legacy(sessions, preset, owner=OWNER)
        monkeypatch.setattr(presets, "_catalog", fail_after_catalog)
        with pytest.raises(PresetError, match="injected"):
            await enroll(
                sessions,
                preset,
                owner=OWNER,
                actor=ACTOR,
                expected_digest=comparison["expected_digest"],
            )
        async with sessions() as session:
            assert await session.get(PresetInstallationRow, preset.scenario) is None
            assert (
                await session.get(PresetVersionRow, (preset.package_id, preset.version))
                is None
            )
        assert await snapshot(sessions) == before

    asyncio.run(run())


def test_explicit_adoption_after_enrollment_keeps_prompt_history_and_status(sessions):
    from app.bootstrap.presets import adopt
    from app.models import PromptOverrideRow

    async def run():
        preset = package()
        await seed(sessions, preset)
        async with sessions() as session, session.begin():
            agent = await session.get(AgentRow, preset.spec.agent_id)
            agent.f_status = "draft"
            await dao.publish_prompt_version(
                session,
                preset.prompt.prompt_id,
                "custom v4",
                None,
                "administrator",
                commit=False,
            )
            session.add(
                PromptOverrideRow(
                    f_agent_id=agent.f_agent_id,
                    f_account_id="user-1",
                    f_content="personal",
                    f_update_time=1,
                )
            )
        inspection = await inspect_legacy(sessions, preset, owner=OWNER)
        await enroll(
            sessions,
            preset,
            owner=OWNER,
            actor=ACTOR,
            expected_digest=inspection["expected_digest"],
        )
        comparison = await preview(
            sessions, preset.package_id, preset.version, owner=OWNER
        )
        await adopt(
            sessions,
            preset.package_id,
            preset.version,
            owner=OWNER,
            actor=ACTOR,
            expected_digest=comparison["expected_digest"],
            installed_features=set(),
        )
        async with sessions() as session:
            head = await session.get(PromptRow, preset.prompt.prompt_id)
            assert head.f_current_version == 5
            assert (
                await session.get(PromptVersionRow, (preset.prompt.prompt_id, 3))
            ).f_content == preset.prompt.content
            assert (
                await session.get(PromptVersionRow, (preset.prompt.prompt_id, 4))
            ).f_content == "custom v4"
            assert (
                await session.get(PromptVersionRow, (preset.prompt.prompt_id, 5))
            ).f_content == preset.prompt.content
            assert (
                await session.get(AgentRow, preset.spec.agent_id)
            ).f_status == "draft"
            assert (
                await session.get(PromptOverrideRow, (preset.spec.agent_id, "user-1"))
            ).f_content == "personal"
            assert await session.get(ScenarioBindingRow, preset.scenario) is None

    asyncio.run(run())
