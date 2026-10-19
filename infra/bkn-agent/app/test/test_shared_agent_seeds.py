"""Execute shared SQL defaults in isolated SQLite; live MariaDB is separate validation."""

import hashlib
import json
import re
import sqlite3
from pathlib import Path

import pytest

from app.models import AgentSpec, PromptSpec

ROOT = Path(__file__).resolve().parents[4]
MIGRATIONS = ROOT / "migrations/bkn-agent/mariadb"
STUDIO = MIGRATIONS / "0.2.1/01-seed-studio-smart-qa.sql"
PROVENANCE = MIGRATIONS / "0.2.1/02-seed-business-provenance.sql"
PROVENANCE_UPGRADE = MIGRATIONS / "0.2.1/03-upgrade-business-provenance-defaults.sql"
SETTINGS = MIGRATIONS / "0.2.1/04-agent-settings.sql"
VERSIONS = MIGRATIONS / "0.2.1/05-agent-versions.sql"


def statements(path):
    pending = ""
    for line in path.read_text().splitlines(keepends=True):
        if line.lstrip().startswith("--"):
            continue
        pending += line
        if sqlite3.complete_statement(pending):
            yield pending.strip().replace("\\n", "\n").replace("\\\\", "\\")
            pending = ""
    assert not pending.strip()


def database():
    db = sqlite3.connect(":memory:", isolation_level=None)
    db.row_factory = sqlite3.Row
    db.create_function("concat", -1, lambda *parts: "".join(parts))
    db.create_function("now", 1, lambda _: 1)
    db.create_function("unix_timestamp", 1, lambda _: 1)
    db.create_function(
        "sha2", 2, lambda value, bits: hashlib.sha256(value.encode()).hexdigest()
    )
    # Only flat limits objects are compared by this migration.
    db.create_function(
        "json_contains",
        2,
        lambda document, candidate: (
            None
            if document is None or candidate is None
            else int(
                all(
                    key in json.loads(document) and json.loads(document)[key] == value
                    for key, value in json.loads(candidate).items()
                )
            )
        ),
    )
    db.executescript("""
        CREATE TABLE dual(dummy INTEGER); INSERT INTO dual VALUES(1);
        CREATE TABLE t_agent(f_agent_id TEXT PRIMARY KEY, f_name TEXT NOT NULL UNIQUE,
            f_mode TEXT, f_prompt_id TEXT, f_prompt_vars_schema TEXT, f_model TEXT,
            f_tools TEXT, f_skills TEXT, f_limits TEXT, f_status TEXT,
            f_create_user TEXT, f_update_user TEXT, f_create_time INTEGER, f_update_time INTEGER);
        CREATE TABLE t_agent_prompt(f_prompt_id TEXT PRIMARY KEY, f_name TEXT NOT NULL UNIQUE,
            f_current_version INTEGER, f_update_user TEXT, f_update_time INTEGER);
        CREATE TABLE t_agent_prompt_version(f_prompt_id TEXT, f_version INTEGER,
            f_content TEXT, f_vars_schema TEXT, f_create_user TEXT, f_create_time INTEGER,
            PRIMARY KEY(f_prompt_id, f_version));
        CREATE TABLE t_agent_version(f_agent_id TEXT, f_version INTEGER,
            f_model TEXT NOT NULL, f_tools TEXT NOT NULL, f_limits TEXT, f_prompt_version INTEGER, f_changes TEXT,
            f_initial INTEGER, f_current INTEGER, f_deleted INTEGER, f_create_user TEXT,
            f_create_time INTEGER, PRIMARY KEY(f_agent_id, f_version));
    """)
    return db


def apply(db, path, limit=None):
    inserts = [
        s
        for s in statements(path)
        if s.lower().startswith(("insert into t_agent", "update t_agent"))
    ]
    for statement in inserts if limit is None else inserts[:limit]:
        db.execute(statement)


def item_from_db(db, agent_id):
    row = dict(
        db.execute("SELECT * FROM t_agent WHERE f_agent_id=?", (agent_id,)).fetchone()
    )
    head = dict(
        db.execute(
            "SELECT * FROM t_agent_prompt WHERE f_prompt_id=?", (row["f_prompt_id"],)
        ).fetchone()
    )
    version = dict(
        db.execute(
            "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id=? AND f_version=?",
            (head["f_prompt_id"], head["f_current_version"]),
        ).fetchone()
    )
    spec = {
        key.removeprefix("f_"): row[key]
        for key in (
            "f_agent_id",
            "f_name",
            "f_mode",
            "f_prompt_id",
            "f_model",
            "f_status",
        )
    }
    for field in ("tools", "skills", "limits"):
        spec[field] = json.loads(row[f"f_{field}"])
    prompt = {
        "prompt_id": head["f_prompt_id"],
        "name": head["f_name"],
        "content": version["f_content"],
    }
    return {
        "agent_id": agent_id,
        "spec": AgentSpec.model_validate(spec).model_dump(exclude_none=True),
        "prompt": PromptSpec.model_validate(prompt).model_dump(exclude_none=True),
    }


def seed_package(agent_id="business_provenance_optimizer"):
    with database() as db:
        apply(db, PROVENANCE)
        return {
            "format": "bkn-agent/v1",
            "exported_at": 0,
            "items": [item_from_db(db, agent_id)],
        }


def snapshot(db):
    return [
        [tuple(row) for row in db.execute(f"SELECT * FROM {table} ORDER BY 1,2")]
        for table in (
            "t_agent",
            "t_agent_prompt",
            "t_agent_prompt_version",
            "t_agent_version",
        )
    ]


def test_new_snapshot_only_adds_the_two_setting_tables():
    old = (MIGRATIONS / "0.2.0/init.sql").read_text()
    new = (MIGRATIONS / "0.2.1/init.sql").read_text()
    assert new.startswith(old.rstrip() + "\n")
    tables = re.findall(r"create table if not exists (\w+)", new, re.I)
    # The original ten tables are untouched; network and user settings were
    # added deliberately (#2151 design change, 2026-10-17).
    assert tables == re.findall(r"create table if not exists (\w+)", old, re.I) + [
        "t_agent_network_setting",
        "t_agent_user_setting",
        "t_agent_version",
    ]
    assert re.findall(r"create table if not exists (\w+)", VERSIONS.read_text(), re.I) == [
        "t_agent_version",
    ]
    assert re.findall(r"create table if not exists (\w+)", SETTINGS.read_text(), re.I) == [
        "t_agent_network_setting",
        "t_agent_user_setting",
    ]
    deltas = (
        STUDIO.read_text()
        + PROVENANCE.read_text()
        + SETTINGS.read_text()
        + VERSIONS.read_text()
    )
    assert "alter table" not in deltas.lower()
    assert not (ROOT / "infra/bkn-agent/deploy").exists()


def test_fresh_install_and_upgrade_have_identical_data_and_preserve_semantic_versions():
    with database() as fresh, database() as upgraded:
        apply(fresh, MIGRATIONS / "0.2.1/init.sql")
        apply(upgraded, MIGRATIONS / "0.2.0/init.sql")
        original = snapshot(upgraded)
        apply(upgraded, STUDIO)
        apply(upgraded, PROVENANCE)
        apply(upgraded, PROVENANCE_UPGRADE)
        apply(upgraded, VERSIONS)
        assert snapshot(fresh) == snapshot(upgraded)
        assert len(snapshot(fresh)[0]) == 5
        for table_before, table_after in zip(original, snapshot(upgraded)):
            assert all(row in table_after for row in table_before)
        versions = dict(
            upgraded.execute(
                "SELECT f_prompt_id, f_current_version FROM t_agent_prompt"
            )
        )
        assert versions["resource-semantic-understanding-prompt"] == 3
        assert versions["catalog-semantic-understanding-prompt"] == 2


@pytest.mark.parametrize("path", [STUDIO, PROVENANCE])
def test_repeated_seed_preserves_custom_configuration_and_disabled_agents(path):
    with database() as db:
        apply(db, path)
        db.execute(
            "UPDATE t_agent SET f_model='custom', f_status='draft', f_tools='[]'"
        )
        db.execute("UPDATE t_agent_prompt_version SET f_content='shared custom prompt'")
        before = snapshot(db)
        apply(db, path)
        assert snapshot(db) == before


@pytest.mark.parametrize("completed", [1, 2])
def test_partial_seed_is_retryable_without_publishing_an_unfinished_agent(completed):
    with database() as db:
        apply(db, STUDIO, limit=completed)
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 0
        apply(db, STUDIO)
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 1
        assert item_from_db(db, "studio_smart_qa")["spec"]["tools"] == [
            {"type": "context_loader"}
        ]


def test_conflicting_prompt_is_not_taken_over():
    with database() as db:
        db.execute(
            "INSERT INTO t_agent_prompt VALUES('studio_smart_qa_prompt','customer',7,'customer',1)"
        )
        before = snapshot(db)
        with pytest.raises(sqlite3.IntegrityError):
            apply(db, STUDIO)
        assert snapshot(db) == before


def test_name_collision_does_not_change_existing_agent():
    with database() as db:
        db.execute(
            "INSERT INTO t_agent(f_agent_id,f_name) VALUES('customer','Studio智能问答')"
        )
        with pytest.raises(sqlite3.IntegrityError):
            apply(db, STUDIO)
        assert (
            db.execute("SELECT f_agent_id FROM t_agent").fetchall()[0][0] == "customer"
        )
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 1


def test_sql_defaults_match_the_previous_agent_and_prompt_definitions():
    expected = {
        # Frozen from the former JSON definitions after normal AgentSpec/PromptSpec validation.
        # studio_smart_qa: Prompt revised before the 0.2.1 release with query and
        # answer guidance learned from the first end-to-end run, then with
        # guidance for listing every row in one query instead of pages.
        "studio_smart_qa": "2cee40de08fa687841d5b4f5dc39d8c9493d14cba3fe03e03d39163b154a02c6",
        "business_provenance_optimizer": "10531cf1bf25a4ac4e52219766f507556e3544f0d9296fc2daff589fe70e7540",
        "business_provenance_claim_attribution": "9dc9e6ad9f118db0c7f890f595584048e9937413c2b60319d57ebf809ee3cb43",
    }
    with database() as db:
        apply(db, STUDIO)
        apply(db, PROVENANCE)
        for agent_id, expected_digest in expected.items():
            item = item_from_db(db, agent_id)
            actual = hashlib.sha256(
                json.dumps(
                    {"spec": item["spec"], "prompt": item["prompt"]},
                    sort_keys=True,
                    ensure_ascii=False,
                    separators=(",", ":"),
                ).encode()
            ).hexdigest()
            assert actual == expected_digest


def selected_migrations(installed_version):
    import subprocess
    import sys

    script = """
import json, logging, sys
from server.config.models import AppConfig, RDSConfig
from server.migrate.script_selector import ScriptSelector
config = AppConfig(rds=RDSConfig('unused',3306,'unused','','mariadb','internal'),repo_path=sys.argv[1])
selector = ScriptSelector(config, logging.getLogger('test'))
print(json.dumps([selector.find_init_sql('bkn-agent'),selector.select_upgrade_scripts('bkn-agent',sys.argv[2])]))
"""
    result = subprocess.run(
        [sys.executable, "-c", script, str(ROOT / "migrations"), installed_version],
        cwd=ROOT / "data-migrator",
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(result.stdout)


def test_shared_migrator_selects_new_snapshot_and_all_upgrade_deltas():
    fresh, upgrade = selected_migrations("0.2.0")
    assert fresh == [str(MIGRATIONS / "0.2.1/init.sql"), "0.2.1"]
    assert upgrade == [
        [
            [
                "0.2.1",
                [
                    str(STUDIO),
                    str(PROVENANCE),
                    str(PROVENANCE_UPGRADE),
                    str(SETTINGS),
                    str(VERSIONS),
                ],
            ]
        ],
        "0.2.1",
        True,
    ]


@pytest.mark.parametrize("release", ["v0.1.4", "v0.1.5"])
def test_older_ee_defaults_receive_timeout_and_prompt_fixes(release):
    with database() as db, database() as defaults:
        apply(defaults, PROVENANCE)
        expected = item_from_db(defaults, "business_provenance_optimizer")
        apply(db, ROOT / f"infra/bkn-agent/fixtures/upgrades/provenance-{release}.sql")
        old_agent = dict(db.execute("SELECT * FROM t_agent").fetchone())
        old_content = db.execute(
            "SELECT f_content FROM t_agent_prompt_version"
        ).fetchone()[0]
        for delta in sorted((MIGRATIONS / "0.2.1").glob("[0-9][0-9]-*.sql")):
            apply(db, delta)
        actual = item_from_db(db, "business_provenance_optimizer")
        assert actual["spec"]["limits"]["timeout_s"] == 600
        assert actual["prompt"]["content"] == expected["prompt"]["content"]
        assert (
            db.execute(
                "SELECT f_current_version FROM t_agent_prompt WHERE f_prompt_id=?",
                (actual["prompt"]["prompt_id"],),
            ).fetchone()[0]
            == 2
        )
        assert (
            db.execute(
                "SELECT f_content FROM t_agent_prompt_version WHERE f_prompt_id=? AND f_version=1",
                (actual["prompt"]["prompt_id"],),
            ).fetchone()[0]
            == old_content
        )
        row = dict(
            db.execute(
                "SELECT * FROM t_agent WHERE f_agent_id='business_provenance_optimizer'"
            ).fetchone()
        )
        for key in old_agent:
            if key not in {"f_limits", "f_update_user", "f_update_time"}:
                assert row[key] == old_agent[key]
        before = snapshot(db)
        apply(db, MIGRATIONS / "0.2.1/03-upgrade-business-provenance-defaults.sql")
        assert snapshot(db) == before


def old_provenance(db):
    apply(db, ROOT / "infra/bkn-agent/fixtures/upgrades/provenance-v0.1.5.sql")


def test_legacy_upgrade_keeps_customer_configuration_and_administrator_rollback():
    with database() as db:
        old_provenance(db)
        db.execute(
            "UPDATE t_agent SET f_model='customer',f_status='draft',f_update_user='administrator'"
        )
        # Even the old official content is preserved after an administrator's rollback.
        db.execute("UPDATE t_agent_prompt SET f_update_user='administrator'")
        before = snapshot(db)
        apply(db, PROVENANCE_UPGRADE)
        assert snapshot(db) == before


def test_custom_prompt_and_limits_are_protected_independently():
    with database() as db:
        old_provenance(db)
        db.execute(
            "UPDATE t_agent_prompt_version SET f_content='customer shared Prompt'"
        )
        apply(db, PROVENANCE_UPGRADE)
        item = item_from_db(db, "business_provenance_optimizer")
        assert item["prompt"]["content"] == "customer shared Prompt"
        assert item["spec"]["limits"]["timeout_s"] == 600
        assert (
            db.execute("SELECT COUNT(*) FROM t_agent_prompt_version").fetchone()[0] == 1
        )
    with database() as db:
        old_provenance(db)
        limits = {
            "max_turns": 3,
            "max_tool_calls": 12,
            "timeout_s": 180,
            "max_output_tokens": 8192,
        }
        db.execute("UPDATE t_agent SET f_limits=?", (json.dumps(limits),))
        apply(db, PROVENANCE_UPGRADE)
        item = item_from_db(db, "business_provenance_optimizer")
        assert item["spec"]["limits"] == limits
        assert (
            db.execute("SELECT COUNT(*) FROM t_agent_prompt_version").fetchone()[0] == 2
        )


@pytest.mark.parametrize(
    "change", ["foreign_owner", "custom_reference", "prompt_variables"]
)
def test_legacy_upgrade_does_not_touch_unrelated_or_custom_prompt_configuration(change):
    with database() as db:
        old_provenance(db)
        if change == "foreign_owner":
            db.execute("UPDATE t_agent SET f_create_user='foreign'")
        elif change == "custom_reference":
            db.execute("UPDATE t_agent SET f_prompt_id='customer_prompt'")
        else:
            db.execute("UPDATE t_agent SET f_update_user='administrator'")
            db.execute("UPDATE t_agent_prompt_version SET f_vars_schema='{}'")
        before = snapshot(db)
        apply(db, PROVENANCE_UPGRADE)
        assert snapshot(db) == before


def test_legacy_upgrade_never_overwrites_or_activates_a_colliding_customer_version():
    with database() as db:
        old_provenance(db)
        db.execute(
            "INSERT INTO t_agent_prompt_version VALUES('business_provenance_optimizer_prompt',2,'customer draft',null,'administrator',2000)"
        )
        before = snapshot(db)
        apply(db, PROVENANCE_UPGRADE)
        after = snapshot(db)
        assert after[1:] == before[1:]
        assert (
            db.execute("SELECT f_current_version FROM t_agent_prompt").fetchone()[0]
            == 1
        )


def test_interrupted_legacy_prompt_upgrade_can_resume_without_duplicate_versions():
    with database() as db:
        old_provenance(db)
        apply(db, PROVENANCE_UPGRADE, limit=2)
        assert (
            db.execute("SELECT f_current_version FROM t_agent_prompt").fetchone()[0]
            == 1
        )
        assert (
            db.execute("SELECT COUNT(*) FROM t_agent_prompt_version").fetchone()[0] == 2
        )
        apply(db, PROVENANCE_UPGRADE)
        assert (
            db.execute("SELECT f_current_version FROM t_agent_prompt").fetchone()[0]
            == 2
        )
        before = snapshot(db)
        apply(db, PROVENANCE_UPGRADE)
        assert snapshot(db) == before


def test_imported_legacy_prompt_with_a_higher_current_version_keeps_its_history():
    with database() as db:
        old_provenance(db)
        db.execute("UPDATE t_agent_prompt_version SET f_version=7")
        db.execute("UPDATE t_agent_prompt SET f_current_version=7")
        apply(db, PROVENANCE_UPGRADE)
        assert (
            db.execute("SELECT f_current_version FROM t_agent_prompt").fetchone()[0]
            == 8
        )
        assert [
            row[0]
            for row in db.execute(
                "SELECT f_version FROM t_agent_prompt_version ORDER BY f_version"
            )
        ] == [7, 8]


def test_legacy_fixture_uses_the_same_json_null_encoding_as_the_original_orm():
    from sqlalchemy.dialects.sqlite import dialect

    from app.models import PromptVersionRow

    encode = PromptVersionRow.__table__.c.f_vars_schema.type.bind_processor(dialect())
    with database() as db:
        old_provenance(db)
        assert (
            db.execute("SELECT f_vars_schema FROM t_agent_prompt_version").fetchone()[0]
            == encode(None)
            == "null"
        )


def test_sql_null_legacy_schema_also_upgrades_and_custom_agent_variables_are_preserved():
    with database() as db:
        old_provenance(db)
        db.execute("UPDATE t_agent_prompt_version SET f_vars_schema=NULL")
        db.execute("UPDATE t_agent SET f_prompt_vars_schema=NULL")
        apply(db, PROVENANCE_UPGRADE)
        assert (
            db.execute("SELECT f_current_version FROM t_agent_prompt").fetchone()[0]
            == 2
        )
    with database() as db:
        old_provenance(db)
        db.execute(
            "UPDATE t_agent SET f_prompt_vars_schema='{}',f_update_user='administrator'"
        )
        before = snapshot(db)
        apply(db, PROVENANCE_UPGRADE)
        assert snapshot(db) == before


def test_already_updated_prompt_does_not_get_a_redundant_history_version():
    with database() as db:
        apply(db, PROVENANCE)
        db.execute(
            "UPDATE t_agent SET f_limits=json_set(f_limits,'$.timeout_s',180) WHERE f_agent_id='business_provenance_optimizer'"
        )
        prompt_before = snapshot(db)[1:]
        apply(db, PROVENANCE_UPGRADE)
        assert (
            item_from_db(db, "business_provenance_optimizer")["spec"]["limits"][
                "timeout_s"
            ]
            == 600
        )
        assert snapshot(db)[1:] == prompt_before


@pytest.mark.parametrize("release", ["0.1.4", "0.1.5"])
def test_real_migrator_selected_cross_version_chain_preserves_semantic_agents(release):
    with database() as db, database() as defaults:
        # Product v0.1.4 shipped the Agent component schema at 0.1.3.
        schema_version = "0.1.3" if release == "0.1.4" else release
        apply(db, MIGRATIONS / schema_version / "init.sql")
        apply(db, ROOT / f"infra/bkn-agent/fixtures/upgrades/provenance-v{release}.sql")
        semantic_before = [
            dict(row)
            for row in db.execute(
                "SELECT * FROM t_agent WHERE f_agent_id IN ('resource-semantic-understanding','catalog-semantic-understanding')"
            )
        ]
        _, upgrade = selected_migrations(schema_version)
        assert upgrade[1] == "0.2.1"
        for _, paths in upgrade[0]:
            for path in paths:
                apply(db, Path(path))
        apply(defaults, PROVENANCE)
        current = item_from_db(db, "business_provenance_optimizer")
        assert current["spec"]["limits"]["timeout_s"] == 600
        assert (
            current["prompt"]["content"]
            == item_from_db(defaults, "business_provenance_optimizer")["prompt"][
                "content"
            ]
        )
        assert [
            dict(row)
            for row in db.execute(
                "SELECT * FROM t_agent WHERE f_agent_id IN ('resource-semantic-understanding','catalog-semantic-understanding')"
            )
        ] == semantic_before
        versions = dict(
            db.execute("SELECT f_prompt_id,f_current_version FROM t_agent_prompt")
        )
        assert versions["resource-semantic-understanding-prompt"] == 3
        assert versions["catalog-semantic-understanding-prompt"] == 2


def test_owner_credential_rollback_is_not_mistaken_for_an_untouched_default():
    with database() as db, database() as defaults:
        old_provenance(db)
        apply(defaults, PROVENANCE)
        new_content = item_from_db(defaults, "business_provenance_optimizer")["prompt"][
            "content"
        ]
        db.execute(
            "INSERT INTO t_agent_prompt_version VALUES('business_provenance_optimizer_prompt',2,?,NULL,'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb',2000)",
            (new_content,),
        )
        # Existing rollback_prompt keeps the creator and changes the head timestamp.
        db.execute("UPDATE t_agent_prompt SET f_current_version=1,f_update_time=3000")
        prompt_before = snapshot(db)[1:]
        apply(db, PROVENANCE_UPGRADE)
        assert snapshot(db)[1:] == prompt_before


def test_every_agent_starts_with_one_current_version():
    with database() as db:
        apply(db, MIGRATIONS / "0.2.1/init.sql")
        rows = {
            row["f_agent_id"]: dict(row) for row in db.execute("SELECT * FROM t_agent_version")
        }
        agents = [row["f_agent_id"] for row in db.execute("SELECT f_agent_id FROM t_agent")]
        assert sorted(rows) == sorted(agents)
        assert all(row["f_version"] == 1 and row["f_current"] == 1 for row in rows.values())
        # Built-in Agents mark v1 as the initial version restore returns to.
        assert all(row["f_initial"] == 1 for row in rows.values())
        qa = db.execute(
            "SELECT f_model, f_tools, f_limits FROM t_agent WHERE f_agent_id='studio_smart_qa'"
        ).fetchone()
        assert (rows["studio_smart_qa"]["f_tools"], rows["studio_smart_qa"]["f_limits"]) == (
            qa["f_tools"],
            qa["f_limits"],
        )
        assert rows["resource-semantic-understanding"]["f_prompt_version"] == 3

        # Running the delta again adds nothing.
        apply(db, VERSIONS)
        assert db.execute("SELECT count(*) FROM t_agent_version").fetchone()[0] == len(rows)


def test_version_snapshot_tolerates_null_tools_and_model_in_older_rows():
    with database() as db:
        db.execute(
            "INSERT INTO t_agent(f_agent_id, f_name, f_mode, f_model, f_tools, f_update_user)"
            " VALUES ('legacy', 'Legacy', 'chat', NULL, NULL, 'u')"
        )
        apply(db, VERSIONS)
        row = db.execute(
            "SELECT f_model, f_tools, f_initial FROM t_agent_version WHERE f_agent_id='legacy'"
        ).fetchone()
        assert (row["f_model"], row["f_tools"], row["f_initial"]) == ("", "[]", 0)
