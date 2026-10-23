"""Execute shared SQL defaults in isolated SQLite; live MariaDB is separate validation."""

import asyncio
import hashlib
import json
import re
import sqlite3
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from app.models import AgentLimits, AgentSpec, PromptSpec

ROOT = Path(__file__).resolve().parents[4]
MIGRATIONS = ROOT / "migrations/bkn-agent/mariadb"
STUDIO = MIGRATIONS / "0.2.1/01-seed-studio-smart-qa.sql"
PROVENANCE = MIGRATIONS / "0.2.1/02-seed-business-provenance.sql"
PROVENANCE_UPGRADE = MIGRATIONS / "0.2.1/03-upgrade-business-provenance-defaults.sql"
SETTINGS = MIGRATIONS / "0.2.1/04-agent-settings.sql"
VERSIONS = MIGRATIONS / "0.2.1/05-agent-versions.sql"
CYPHER = MIGRATIONS / "0.2.1/06-seed-cypher-generation.sql"
METRIC_DESIGN = MIGRATIONS / "0.2.1/07-seed-object-metric-designer.sql"
SEMANTIC_LANGUAGE = MIGRATIONS / "0.2.1/08-semantic-output-language.sql"
GRAPH_EXPLORER = MIGRATIONS / "0.2.1/09-seed-graph-explorer.sql"
CLAIM_LOCALES = MIGRATIONS / "0.2.2/01-claim-prompt-locales.sql"


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

    # Migrations compare flat limits objects and lists of changed field names.
    def json_contains(document, candidate):
        if document is None or candidate is None:
            return None
        document, candidate = json.loads(document), json.loads(candidate)
        if isinstance(candidate, list):
            return int(
                isinstance(document, list)
                and all(value in document for value in candidate)
            )
        return int(
            isinstance(document, dict)
            and all(document.get(key) == value for key, value in candidate.items())
        )

    db.create_function("json_contains", 2, json_contains)
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
        if s.lower().startswith(("insert into t_agent", "update t_agent", "alter table t_agent_prompt_version"))
    ]
    for statement in inserts if limit is None else inserts[:limit]:
        if statement.lower().startswith("alter table t_agent_prompt_version"):
            if "f_localized_content" not in {row[1] for row in db.execute("PRAGMA table_info(t_agent_prompt_version)")}:
                db.execute("ALTER TABLE t_agent_prompt_version ADD COLUMN f_localized_content TEXT")
        else:
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
    old_tables = [
        statement
        for statement in statements(MIGRATIONS / "0.2.0/init.sql")
        if statement.lower().startswith("create table")
    ]
    new_tables = [
        statement
        for statement in statements(MIGRATIONS / "0.2.1/init.sql")
        if statement.lower().startswith("create table")
    ]
    assert new_tables[: len(old_tables)] == old_tables
    tables = re.findall(r"create table if not exists (\w+)", new, re.I)
    # The original ten tables are untouched; network and user settings were
    # added deliberately (#2151 design change, 2026-10-17).
    assert tables == re.findall(r"create table if not exists (\w+)", old, re.I) + [
        "t_agent_network_setting",
        "t_agent_user_setting",
        "t_agent_version",
    ]
    assert re.findall(
        r"create table if not exists (\w+)", VERSIONS.read_text(), re.I
    ) == [
        "t_agent_version",
    ]
    assert re.findall(
        r"create table if not exists (\w+)", SETTINGS.read_text(), re.I
    ) == [
        "t_agent_network_setting",
        "t_agent_user_setting",
    ]
    deltas = (
        STUDIO.read_text()
        + PROVENANCE.read_text()
        + SETTINGS.read_text()
        + VERSIONS.read_text()
        + CYPHER.read_text()
        + METRIC_DESIGN.read_text()
        + GRAPH_EXPLORER.read_text()
    )
    assert "alter table" not in deltas.lower()
    assert not (ROOT / "infra/bkn-agent/deploy").exists()


def test_fresh_install_and_upgrade_have_identical_defaults_and_preserve_upgrade_history():
    with database() as fresh, database() as upgraded:
        apply(fresh, MIGRATIONS / "0.2.1/init.sql")
        apply(upgraded, MIGRATIONS / "0.2.0/init.sql")
        original = snapshot(upgraded)
        apply(upgraded, STUDIO)
        apply(upgraded, PROVENANCE)
        apply(upgraded, PROVENANCE_UPGRADE)
        apply(upgraded, VERSIONS)
        apply(upgraded, CYPHER)
        apply(upgraded, METRIC_DESIGN)
        history = snapshot(upgraded)[3]
        assert history, "Historical Agent snapshots must exist before comparison"
        apply(upgraded, SEMANTIC_LANGUAGE)
        apply(upgraded, GRAPH_EXPLORER)
        apply(upgraded, CLAIM_LOCALES)
        # Fresh installs initialize current defaults directly; upgrades retain
        # historical Prompt/Agent versions while resolving the same defaults.
        assert snapshot(fresh)[:2] == snapshot(upgraded)[:2]
        for agent in fresh.execute("SELECT f_agent_id FROM t_agent"):
            assert item_from_db(fresh, agent[0]) == item_from_db(upgraded, agent[0])
        assert len(snapshot(fresh)[0]) == 8
        updated = snapshot(upgraded)
        assert all(row in updated[0] for row in original[0])
        for row in original[1]:
            match = next(after for after in updated[1] if after[0] == row[0])
            if row[0] == "resource-semantic-understanding-prompt":
                assert row[:2] == match[:2]
                assert match[2] == 4
            else:
                assert row == match
        # Adding the nullable locale column must retain every original value.
        assert all(row == after[:len(row)] for row in original[2] for after in updated[2] if row[:2] == after[:2])
        assert {row[:2] for row in original[2]} <= {row[:2] for row in updated[2]}
        # Only the upgraded Agent may change its historical initial/current markers.
        for row in history:
            match = next(after for after in updated[3] if after[:2] == row[:2])
            if row[0] == "resource-semantic-understanding":
                assert row[:7] + row[9:] == match[:7] + match[9:]
            else:
                assert row == match
        versions = dict(
            upgraded.execute(
                "SELECT f_prompt_id, f_current_version FROM t_agent_prompt"
            )
        )
        assert versions["resource-semantic-understanding-prompt"] == 4
        assert versions["catalog-semantic-understanding-prompt"] == 2


def test_object_metric_designer_default_is_english_and_respects_user_language():
    with database() as db:
        apply(db, METRIC_DESIGN)
        seeded = item_from_db(db, "object_metric_designer")
        assert seeded["spec"]["name"] == "ObjectMetricDesigner"
        assert seeded["prompt"]["name"] == "Object Metric Designer Default Prompt"
        content = seeded["prompt"]["content"]
        assert content.isascii()
        assert "Respond in the language of the current user message" in content
        assert "Do not query instance data" in content
        assert "changes empty" in content


def test_graph_explorer_mounts_the_provider_without_context_loader_tools():
    with database() as db:
        apply(db, GRAPH_EXPLORER)
        seeded = item_from_db(db, "graph_explorer")
        assert seeded["spec"]["tools"] == [
            {"type": "context_loader", "profile": "full", "allowed_tools": []},
            {"type": "builtin", "provider": "graph_explorer"},
        ]
        assert "expand_neighbours" in seeded["prompt"]["content"]


@pytest.mark.parametrize("path", [STUDIO, PROVENANCE, CYPHER, METRIC_DESIGN, GRAPH_EXPLORER])
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
        # guidance for listing every row in one query instead of pages, then
        # with the run_cypher subset as it stands after #2263 in place of a
        # list of refusals that had stopped being true, then with CASE,
        # coalesce and the comparison of two collected lists, then with
        # nodes passed on to a second MATCH, then with explain for checking a
        # complex query first.
        "studio_smart_qa": "a03cdeb687769096f3efaa3ce108af2c6fbfc41ba5d6aad5dbb5b5dd709fd568",
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
    assert fresh == [str(MIGRATIONS / "0.2.2/init.sql"), "0.2.2"]
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
                    str(CYPHER),
                    str(METRIC_DESIGN),
                    str(SEMANTIC_LANGUAGE),
                    str(GRAPH_EXPLORER),
                ],
            ],
            ["0.2.2", [str(CLAIM_LOCALES)]],
        ],
        "0.2.2",
        True,
    ]


def test_claim_prompt_translation_is_idempotent_and_preserves_custom_content():
    with database() as db:
        apply(db, PROVENANCE)
        prompt_id = "business_provenance_claim_attribution_prompt"
        original = db.execute("SELECT f_content FROM t_agent_prompt_version WHERE f_prompt_id=?", (prompt_id,)).fetchone()[0]
        apply(db, CLAIM_LOCALES)
        translated = db.execute("SELECT f_localized_content FROM t_agent_prompt_version WHERE f_prompt_id=?", (prompt_id,)).fetchone()[0]
        assert json.loads(translated)["en-US"].startswith("You are the OpenBKN business-provenance")
        # The same JSON keys and strict evidence contract appear in both languages.
        for term in ("judgment_text", "display_text", "primary", "supporting", "disclosures", "question_quote", "answer_quote", "occurrence", "result_texts", "subject_text", "predicate_text", "evidence_fact_ids", "support_status"):
            assert term in original and term in translated
        before = snapshot(db)
        apply(db, CLAIM_LOCALES)
        assert snapshot(db) == before
        assert db.execute("SELECT f_content FROM t_agent_prompt_version WHERE f_prompt_id=?", (prompt_id,)).fetchone()[0] == original

    for field, value in (("f_content", "Administrator prompt"), ("f_create_user", "administrator"), ("f_vars_schema", '{"type":"object"}'), ("f_localized_content", '{"en-US":"Existing custom translation"}')):
        with database() as db:
            apply(db, PROVENANCE)
            db.execute("ALTER TABLE t_agent_prompt_version ADD COLUMN f_localized_content TEXT")
            db.execute(f"UPDATE t_agent_prompt_version SET {field}=? WHERE f_prompt_id=?", (value, prompt_id))
            before = snapshot(db)
            apply(db, CLAIM_LOCALES)
            assert snapshot(db) == before


def test_claim_prompt_fresh_snapshot_includes_the_same_locale_upgrade():
    migration = CLAIM_LOCALES.read_text()
    assert migration in (MIGRATIONS / "0.2.2/init.sql").read_text()


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
        assert upgrade[1] == "0.2.2"
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
        assert versions["resource-semantic-understanding-prompt"] == 4
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
            row["f_agent_id"]: dict(row)
            for row in db.execute("SELECT * FROM t_agent_version WHERE f_current=1")
        }
        agents = [
            row["f_agent_id"] for row in db.execute("SELECT f_agent_id FROM t_agent")
        ]
        assert sorted(rows) == sorted(agents)
        assert db.execute(
            "SELECT count(*) FROM t_agent_version WHERE f_current=1"
        ).fetchone()[0] == len(agents)
        assert all(row["f_version"] == 1 for row in rows.values())
        assert all(row["f_current"] == 1 for row in rows.values())
        # A fresh installation restores to its initialized current defaults.
        initial = db.execute(
            "SELECT f_prompt_version FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_initial=1"
        ).fetchone()
        assert initial[0] == 4
        assert all(row["f_initial"] == 1 for row in rows.values())
        qa = db.execute(
            "SELECT f_model, f_tools, f_limits FROM t_agent WHERE f_agent_id='studio_smart_qa'"
        ).fetchone()
        assert (
            rows["studio_smart_qa"]["f_tools"],
            rows["studio_smart_qa"]["f_limits"],
        ) == (
            qa["f_tools"],
            qa["f_limits"],
        )
        assert rows["resource-semantic-understanding"]["f_prompt_version"] == 4

        # Running the delta again adds nothing.
        apply(db, VERSIONS)
        assert db.execute("SELECT count(*) FROM t_agent_version").fetchone()[0] == len(
            rows
        )


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


def test_cypher_generation_seed_reads_schema_and_runs_cypher_only():
    with database() as db:
        apply(db, CYPHER)
        item = item_from_db(db, "cypher_generation")
        assert item["spec"]["status"] == "draft"
        assert item["spec"]["model"] == ""
        assert item["spec"]["tools"] == [
            {
                "type": "context_loader",
                "profile": "full",
                "allowed_tools": [
                    "get_kn_detail",
                    "search_schema",
                    "get_object_types",
                    "get_relation_types",
                    "search_instance",
                    "run_cypher",
                ],
            }
        ]
        # Callers read the query out of a cypher fence and run it unchanged, so
        # the Prompt asks for complete queries only and names the virtual edge.
        content = item["prompt"]["content"]
        assert "```cypher" in content
        assert "relationType" in content
        assert "始终写完整查询" in content
        # Complete queries draw only what RETURN lists: a node-only answer to
        # a relationship question must not pass the Agent's own check.
        assert "RETURN p, r, m" in content
        assert "只有节点不算通过" in content
        # Any graph of connected nodes needs its relationships, not only relationship questions.
        assert "返回了两个以上相连的节点时" in content
        # Explaining an existing query is the same Agent: it must not rewrite the query
        # or squeeze the explanation into the one-line output rule.
        assert "解释查询" in content
        assert "只解释，不执行" in content
        assert "不写新查询，也不改写原查询" in content
        # The graph dedups nodes itself; the syntax limit lives in run_cypher's description.
        assert "不要为了图上干净而去重" in content
        # An ambiguous question gets the common reading, not minutes of deliberation.
        assert "按最常见的理解直接写，不反复权衡" in content
        # Anonymous relationships return no relation values, so the graph draws no lines.
        assert "MATCH 里的每条关系都起变量名" in content
        # A question the network cannot answer ends early with the reason, not more attempts.
        assert "答不了就尽早停" in content
        assert "片段" not in content
        assert "图探索" not in content and "画布" not in content
        rows = db.execute(
            "SELECT f_version, f_initial, f_current, f_prompt_version FROM t_agent_version"
            " WHERE f_agent_id='cypher_generation'"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(1, 1, 1, 1)]
        # A generation that cannot finish in two minutes is better given up than waited on.
        limits = db.execute(
            "SELECT f_limits FROM t_agent WHERE f_agent_id='cypher_generation'"
        ).fetchone()[0]
        assert json.loads(limits)["timeout_s"] == 120


def test_metric_designer_seed_is_tool_free_and_draft():
    with database() as db:
        apply(db, METRIC_DESIGN)
        item = item_from_db(db, "object_metric_designer")
        assert item["spec"]["status"] == "draft"
        assert item["spec"]["model"] == ""
        assert item["spec"]["tools"] == []
        assert item["spec"]["skills"] == []
        assert item["spec"]["limits"]["max_tool_calls"] == 0
        assert "save, publish, delete, or upgrade metrics" in item["prompt"]["content"]
        apply(db, METRIC_DESIGN)
        assert db.execute("SELECT COUNT(*) FROM t_agent WHERE f_agent_id='object_metric_designer'").fetchone()[0] == 1


@pytest.mark.parametrize("completed", [1, 2])
def test_partial_cypher_seed_is_retryable_without_an_unfinished_agent(completed):
    with database() as db:
        apply(db, CYPHER, limit=completed)
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 0
        apply(db, CYPHER)
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 1
        assert db.execute("SELECT COUNT(*) FROM t_agent_version").fetchone()[0] == 1


def test_conflicting_cypher_prompt_is_not_taken_over():
    with database() as db:
        db.execute(
            "INSERT INTO t_agent_prompt VALUES('cypher_generation_prompt','customer',7,'customer',1)"
        )
        before = snapshot(db)
        with pytest.raises(sqlite3.IntegrityError):
            apply(db, CYPHER)
        assert snapshot(db) == before


def test_semantic_output_language_prompt_upgrade_preserves_history_and_configuration():
    with database() as db:
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        old_prompt = item_from_db(db, "resource-semantic-understanding")["prompt"][
            "content"
        ]
        db.execute(
            "UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='resource-semantic-understanding'"
        )
        apply(db, SEMANTIC_LANGUAGE)
        prompt = item_from_db(db, "resource-semantic-understanding")["prompt"][
            "content"
        ]
        assert prompt.startswith(old_prompt)
        assert "options.language" in prompt and "en-US" in prompt and "zh-CN" in prompt
        assert "display_name、description、自然语言 warnings" in prompt
        assert "技术字段 name、ID、类型" in prompt
        assert "不要仅为翻译而重写已有用户元数据" in prompt
        original = db.execute(
            "SELECT f_content FROM t_agent_prompt_version WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=3"
        ).fetchone()
        assert original[0] == old_prompt
        current = db.execute(
            "SELECT f_model,f_prompt_version FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_current=1"
        ).fetchall()
        assert [tuple(row) for row in current] == [("custom-model", 4)]
        before = snapshot(db)
        apply(db, SEMANTIC_LANGUAGE)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback"])
def test_semantic_output_language_upgrade_preserves_operator_prompt_choices(
    customization,
):
    with database() as db:
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=3"
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version VALUES('resource-semantic-understanding-prompt',4,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute(
                    "UPDATE t_agent_prompt SET f_current_version=4 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
                )
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
                )
        before = snapshot(db)
        apply(db, SEMANTIC_LANGUAGE)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 8))
def test_semantic_output_language_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            apply(connection, MIGRATIONS / "0.2.0/init.sql")
            apply(connection, VERSIONS)
            if customized:
                connection.execute(
                    "UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='resource-semantic-understanding'"
                )
        apply(expected, SEMANTIC_LANGUAGE)
        apply(db, SEMANTIC_LANGUAGE, limit=limit)
        apply(db, SEMANTIC_LANGUAGE)
        assert snapshot(db) == snapshot(expected)


def test_semantic_language_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        apply(db, SEMANTIC_LANGUAGE)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=3,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
        )
        db.execute(
            "UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='resource-semantic-understanding'"
        )
        db.execute(
            "UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='resource-semantic-understanding' AND f_version=1"
        )
        before = snapshot(db)
        apply(db, SEMANTIC_LANGUAGE)
        assert snapshot(db) == before


def test_current_init_only_initializes_the_latest_semantic_defaults():
    init = MIGRATIONS / "0.2.1/init.sql"
    # The optional claim-locale column and guarded translation backfill do not
    # update semantic-understanding versions or their current/initial markers.
    claim_locale_statements = set(statements(CLAIM_LOCALES))
    assert not any(
        re.match(r"(?i)(update|delete|alter)\b", statement)
        for statement in statements(init)
        if statement not in claim_locale_statements
    )
    with database() as db:
        apply(db, init)
        versions = db.execute(
            "SELECT f_version FROM t_agent_prompt_version WHERE f_prompt_id='resource-semantic-understanding-prompt'"
        ).fetchall()
        assert [row[0] for row in versions] == [4]
        agent_versions = db.execute(
            "SELECT f_version,f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding'"
        ).fetchall()
        assert [tuple(row) for row in agent_versions] == [(1, 4, 1, 1)]
        before = snapshot(db)
        apply(db, init)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
def test_semantic_upgrade_advances_restore_baseline_without_replacing_custom_settings(
    customized,
):
    with database() as db:
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        initial = dict(
            db.execute(
                "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_initial=1"
            ).fetchone()
        )
        if customized:
            db.execute(
                "UPDATE t_agent SET f_model='custom-model',f_tools='[]',f_limits=? WHERE f_agent_id='resource-semantic-understanding'",
                ('{"max_turns":17}',),
            )
        apply(db, SEMANTIC_LANGUAGE)
        baseline = dict(
            db.execute(
                "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_initial=1"
            ).fetchone()
        )
        current = dict(
            db.execute(
                "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_current=1"
            ).fetchone()
        )
        assert baseline["f_prompt_version"] == 4
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == initial[field]
        assert bool(baseline["f_current"]) is not customized
        assert current["f_prompt_version"] == 4
        if customized:
            assert current["f_model"] == "custom-model"
            assert current["f_tools"] == "[]"
            assert json.loads(current["f_limits"]) == {"max_turns": 17}
        historical = dict(
            db.execute(
                "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_version=1"
            ).fetchone()
        )
        for field in (
            "f_model",
            "f_tools",
            "f_limits",
            "f_prompt_version",
            "f_changes",
        ):
            assert historical[field] == initial[field]
        before = snapshot(db)
        apply(db, SEMANTIC_LANGUAGE)
        assert snapshot(db) == before

        async def restore():
            from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

            from app import dao
            from app.models import (
                AgentRow,
                AgentVersionRow,
                Base,
                PromptRow,
                PromptVersionRow,
            )
            from app.routers.agent_settings import _shared_out

            engine = create_async_engine("sqlite+aiosqlite:///:memory:")
            try:
                async with engine.begin() as connection:
                    await connection.run_sync(Base.metadata.create_all)
                async with async_sessionmaker(
                    engine, expire_on_commit=False
                )() as session:
                    for table, model in (
                        ("t_agent", AgentRow),
                        ("t_agent_prompt", PromptRow),
                        ("t_agent_prompt_version", PromptVersionRow),
                        ("t_agent_version", AgentVersionRow),
                    ):
                        for row in db.execute(f"SELECT * FROM {table}"):
                            values = dict(row)
                            for field in (
                                "f_tools",
                                "f_skills",
                                "f_limits",
                                "f_changes",
                                "f_vars_schema",
                                "f_prompt_vars_schema",
                            ):
                                if field in values and values[field] is not None:
                                    values[field] = json.loads(values[field])
                            session.add(model(**values))
                    await session.commit()
                    target = await dao.initial_version(
                        session, "resource-semantic-understanding"
                    )
                    assert target.f_prompt_version == 4
                    restored = await dao.activate_version(
                        session, target.f_agent_id, target.f_version, "operator"
                    )
                    assert restored is not None
                    agent = await dao.get_agent(session, target.f_agent_id)
                    shared = await _shared_out(session, agent, True)
                    assert shared.prompt_version == 4
                    assert shared.modified is False
                    assert shared.version == shared.initial_version
                    assert agent.model == initial["f_model"]
                    assert agent.tools == json.loads(initial["f_tools"])
                    assert agent.limits == AgentLimits.model_validate(
                        json.loads(initial["f_limits"])
                    )
            finally:
                await engine.dispose()

        asyncio.run(restore())


def test_semantic_upgrade_versions_serialize_through_settings_api(monkeypatch):
    from app.routers import agent_settings

    with database() as db:
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        apply(db, SEMANTIC_LANGUAGE)
        versions = []
        for row in db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding'"
        ):
            values = dict(row)
            for field in ("f_tools", "f_limits", "f_changes"):
                values[field] = json.loads(values[field]) if values[field] else None
            versions.append(SimpleNamespace(**values))
        monkeypatch.setattr(
            agent_settings.dao, "list_versions", AsyncMock(return_value=versions)
        )
        monkeypatch.setattr(
            agent_settings.dao, "list_prompt_versions", AsyncMock(return_value=[])
        )
        output = asyncio.run(
            agent_settings._versions_out(
                None,
                SimpleNamespace(
                    agent_id="resource-semantic-understanding", prompt_id=None
                ),
            )
        )
        upgraded = [
            version
            for version in output
            if version.prompt_version == 4 and version.current
        ]
        assert upgraded
        assert all(version.changes == ["prompt_version"] for version in upgraded)


def test_installed_021_selects_locale_schema_upgrade():
    _, upgrade = selected_migrations("0.2.1")
    assert upgrade == [[["0.2.2", [str(CLAIM_LOCALES)]]], "0.2.2", True]
    _, repeated = selected_migrations("0.2.2")
    assert repeated == [[], "0.2.2", False]
