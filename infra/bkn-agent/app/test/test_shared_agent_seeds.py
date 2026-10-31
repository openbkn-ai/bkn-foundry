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
from app.public_auth import BUILTIN_AGENT_IDS

ROOT = Path(__file__).resolve().parents[4]
MIGRATIONS = ROOT / "migrations/bkn-agent/mariadb"
STUDIO = MIGRATIONS / "0.2.1/01-seed-studio-smart-qa.sql"
PROVENANCE = MIGRATIONS / "0.2.1/02-seed-business-provenance.sql"
PROVENANCE_UPGRADE = MIGRATIONS / "0.2.1/03-upgrade-business-provenance-defaults.sql"
SETTINGS = MIGRATIONS / "0.2.1/04-agent-settings.sql"
VERSIONS = MIGRATIONS / "0.2.1/05-agent-versions.sql"
CYPHER = MIGRATIONS / "0.2.1/06-seed-cypher-generation.sql"
METRIC_DESIGN = MIGRATIONS / "0.2.1/07-seed-object-metric-designer.sql"
METRIC_DESIGN_BUDGET = MIGRATIONS / "0.2.1/14-object-metric-designer-output-budget.sql"
SEMANTIC_LANGUAGE = MIGRATIONS / "0.2.1/08-semantic-output-language.sql"
GRAPH_EXPLORER = MIGRATIONS / "0.2.1/09-seed-graph-explorer.sql"
CLAIM_LOCALES = MIGRATIONS / "0.2.1/10-claim-prompt-locales.sql"
PROMPT_INDEX = MIGRATIONS / "0.2.1/11-agent-prompt-index.sql"
RESOURCE_ID_LIMITS = MIGRATIONS / "0.2.1/12-resource-id-limits.sql"
DRAFTER = MIGRATIONS / "0.2.1/13-seed-bkn-project-drafter.sql"
BUILTIN_LOCALES = MIGRATIONS / "0.2.1/16-builtin-prompt-locales.sql"
SOURCE_LANGUAGE = MIGRATIONS / "0.2.1/25-prompt-source-language.sql"
SEMANTIC_API_NAME = MIGRATIONS / "0.2.1/15-semantic-api-name.sql"
SEMANTIC_NAMING = MIGRATIONS / "0.2.1/17-semantic-naming.sql"
REQUIREMENTS = MIGRATIONS / "0.2.1/18-seed-bkn-project-requirements.sql"
ANSWER_JUDGE = MIGRATIONS / "0.2.1/20-seed-bkn-project-answer-judge.sql"
ANSWER_JUDGE_EXPECT = MIGRATIONS / "0.2.1/21-answer-judge-test-expect.sql"
ANSWER_JUDGE_REASON = MIGRATIONS / "0.2.1/26-answer-judge-business-reason.sql"
REQUIREMENTS_V2 = MIGRATIONS / "0.2.1/22-requirements-guidance-v2.sql"
REQUIREMENTS_V3 = MIGRATIONS / "0.2.1/23-requirements-guidance-v3.sql"
REQUIREMENTS_V4 = MIGRATIONS / "0.2.1/24-requirements-guidance-v4.sql"
REQUIREMENTS_V5 = MIGRATIONS / "0.2.1/27-requirements-guidance-v5.sql"
REQUIREMENTS_V6 = MIGRATIONS / "0.2.1/28-requirements-guidance-v6.sql"
ONTOLOGY_ASK = MIGRATIONS / "0.2.1/29-seed-bkn-project-ontology-ask.sql"
METRIC_DESIGN_LOCALE = MIGRATIONS / "0.2.1/30-object-metric-designer-locale.sql"
ONTOLOGY_ASK_REVISIONS = MIGRATIONS / "0.2.1/31-bkn-project-ontology-ask-revisions.sql"


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
        "sha2", 2, lambda value, bits: None if value is None else hashlib.sha256(value.encode()).hexdigest()
    )
    # SQLite's json_extract already returns unquoted text.
    db.create_function("json_unquote", 1, lambda value: value)

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
            field = "f_source_language" if "f_source_language" in statement else "f_localized_content"
            if field not in {row[1] for row in db.execute("PRAGMA table_info(t_agent_prompt_version)")}:
                db.execute(f"ALTER TABLE t_agent_prompt_version ADD COLUMN {field} TEXT")
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
        + DRAFTER.read_text()
        + SEMANTIC_API_NAME.read_text()
    )
    assert "alter table" not in deltas.lower()
    assert not (ROOT / "infra/bkn-agent/deploy").exists()


def test_fresh_install_and_upgrade_have_identical_defaults_and_preserve_upgrade_history():
    with database() as fresh, database() as upgraded:
        apply(fresh, Path(selected_migrations("0.0.0")[0][0]))
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
        apply(upgraded, DRAFTER)
        apply(upgraded, SEMANTIC_API_NAME)
        apply(upgraded, BUILTIN_LOCALES)
        apply(upgraded, SEMANTIC_NAMING)
        apply(upgraded, REQUIREMENTS)
        apply(upgraded, ANSWER_JUDGE)
        apply(upgraded, ANSWER_JUDGE_EXPECT)
        apply(upgraded, REQUIREMENTS_V2)
        apply(upgraded, REQUIREMENTS_V3)
        apply(upgraded, REQUIREMENTS_V4)
        apply(upgraded, SOURCE_LANGUAGE)
        apply(upgraded, ANSWER_JUDGE_REASON)
        apply(upgraded, REQUIREMENTS_V5)
        apply(upgraded, REQUIREMENTS_V6)
        apply(upgraded, ONTOLOGY_ASK)
        apply(upgraded, METRIC_DESIGN_LOCALE)
        apply(upgraded, ONTOLOGY_ASK_REVISIONS)
        # Fresh installs initialize current defaults directly; upgrades retain
        # historical Prompt/Agent versions while resolving the same defaults.
        assert snapshot(fresh)[:2] == snapshot(upgraded)[:2]
        for agent in fresh.execute("SELECT f_agent_id FROM t_agent"):
            assert item_from_db(fresh, agent[0]) == item_from_db(upgraded, agent[0])
        # Every seeded Agent is registered as builtin, so System Admin manages it
        # and it cannot be unpublished or deleted.
        assert {row[0] for row in snapshot(fresh)[0]} == set(BUILTIN_AGENT_IDS)
        updated = snapshot(upgraded)
        assert all(row in updated[0] for row in original[0])
        for row in original[1]:
            match = next(after for after in updated[1] if after[0] == row[0])
            if row[0] == "resource-semantic-understanding-prompt":
                assert row[:2] == match[:2]
                assert match[2] == 6
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
        assert versions["resource-semantic-understanding-prompt"] == 6
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


@pytest.mark.parametrize("path", [STUDIO, PROVENANCE, CYPHER, METRIC_DESIGN, GRAPH_EXPLORER, DRAFTER, REQUIREMENTS, ANSWER_JUDGE, ONTOLOGY_ASK])
def test_repeated_seed_preserves_custom_configuration_and_disabled_agents(path):
    with database() as db:
        # 18 also attaches translations, to the column 10 adds.
        apply(db, CLAIM_LOCALES)
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
                    str(CYPHER),
                    str(METRIC_DESIGN),
                    str(SEMANTIC_LANGUAGE),
                    str(GRAPH_EXPLORER),
                    str(CLAIM_LOCALES),
                    str(PROMPT_INDEX),
                    str(RESOURCE_ID_LIMITS),
                    str(DRAFTER),
                    str(METRIC_DESIGN_BUDGET),
                    str(SEMANTIC_API_NAME),
                    str(BUILTIN_LOCALES),
                    str(SEMANTIC_NAMING),
                    str(REQUIREMENTS),
                    str(ANSWER_JUDGE),
                    str(ANSWER_JUDGE_EXPECT),
                    str(REQUIREMENTS_V2),
                    str(REQUIREMENTS_V3),
                    str(REQUIREMENTS_V4),
                    str(SOURCE_LANGUAGE),
                    str(ANSWER_JUDGE_REASON),
                    str(REQUIREMENTS_V5),
                    str(REQUIREMENTS_V6),
                    str(ONTOLOGY_ASK),
                    str(METRIC_DESIGN_LOCALE),
                    str(ONTOLOGY_ASK_REVISIONS),
                ],
            ],
        ],
        "0.2.1",
        True,
    ]


def test_claim_prompt_translation_is_idempotent_and_preserves_custom_content():
    with database() as db:
        apply(db, PROVENANCE)
        apply(db, VERSIONS)
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


def naming_locales_text():
    """The v6 translation backfill at the end of 17, which the fresh snapshot ends with too."""
    text = SEMANTIC_NAMING.read_text()
    return text[text.index("-- Attach the zh-CN"):]


def requirements_locales_text():
    """The v6 translation backfill at the end of 28, which the fresh snapshot ends with too."""
    text = REQUIREMENTS_V6.read_text()
    return text[text.index("-- Attach the zh-CN"):]


def answer_judge_locales_text():
    """The translation backfill of the judge's v3 at the end of 26, which the fresh snapshot ends with too."""
    text = ANSWER_JUDGE_REASON.read_text()
    return text[text.index("-- Attach the zh-CN"):]


def ontology_ask_locales_text():
    """The translation backfill of v2 at the end of 31, which the fresh snapshot ends with too."""
    text = ONTOLOGY_ASK_REVISIONS.read_text()
    return text[text.index("-- Attach the zh-CN"):]


def test_fresh_snapshot_ends_with_the_locale_and_index_upgrades():
    # The fresh-install snapshot applies the same locale backfill and binding index as an upgrade.
    assert (MIGRATIONS / "0.2.1/init.sql").read_text().endswith(
        "\n" + CLAIM_LOCALES.read_text() + "\n" + PROMPT_INDEX.read_text() + "\n" + BUILTIN_LOCALES.read_text()
        + "\n" + naming_locales_text() + "\n" + requirements_locales_text() + "\n" + answer_judge_locales_text()
        + "\n" + ontology_ask_locales_text()
        + "\n" + METRIC_DESIGN_LOCALE.read_text()
    )
    # The seed itself sits with the other seeds, before the locale column is added; a fresh
    # install starts at v6, so its prompt is the body 28 writes.
    init = (MIGRATIONS / "0.2.1/init.sql").read_text()
    assert init.index(SOURCE_LANGUAGE.read_text()) < init.index("insert into t_agent_prompt_version")
    seed = init.index("-- bkn-project-requirements:")
    v6 = [s for s in statements(REQUIREMENTS_V6) if s.startswith("insert into t_agent_prompt_version")][0]
    body = v6[v6.index("select v.f_prompt_id, 6,") + len("select v.f_prompt_id, 6,"):v6.index("v.f_vars_schema,")].strip()
    assert init.index("select 'bkn-project-requirements-prompt', 6, 'en-US',\n    " + body, seed) < init.index(CLAIM_LOCALES.read_text())
    # The judge is seeded at its latest version, after the other seeds and before the locale column.
    assert seed < init.index("-- bkn-project-answer-judge:") < init.index(CLAIM_LOCALES.read_text())
    # Ask AI is seeded after the judge and before the locale column, its source language marked; a
    # fresh install starts at v2, so its prompt is the body 31 writes.
    ask = init.index("-- bkn-project-ontology-ask:")
    assert init.index("-- bkn-project-answer-judge:") < ask < init.index(CLAIM_LOCALES.read_text())
    v2 = [s for s in statements(ONTOLOGY_ASK_REVISIONS) if s.startswith("insert into t_agent_prompt_version")][0]
    body = v2[v2.index("select v.f_prompt_id, 2,") + len("select v.f_prompt_id, 2,"):v2.index("v.f_vars_schema,")].strip()
    assert init.index("select 'bkn-project-ontology-ask-prompt', 2, 'en-US',\n    " + body, ask) < init.index(CLAIM_LOCALES.read_text())


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
        assert versions["resource-semantic-understanding-prompt"] == 6
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
        apply(db, Path(selected_migrations("0.0.0")[0][0]))
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
        assert initial[0] == 6
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
        assert rows["resource-semantic-understanding"]["f_prompt_version"] == 6

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


def test_project_drafter_seed_is_a_published_tool_free_task_on_the_default_model():
    with database() as db:
        apply(db, Path(selected_migrations("0.0.0")[0][0]))
        agent = db.execute(
            "SELECT f_name, f_mode, f_model, f_tools, f_skills, f_limits, f_status FROM t_agent"
            " WHERE f_agent_id='bkn-project-drafter'"
        ).fetchone()
        assert agent["f_name"] == "BKNProjectDrafter"
        assert agent["f_mode"] == "task"
        assert agent["f_model"] == ""
        assert json.loads(agent["f_tools"]) == []
        assert json.loads(agent["f_skills"]) == []
        assert agent["f_status"] == "published"
        limits = json.loads(agent["f_limits"])
        assert limits["max_turns"] == 1 and limits["max_tool_calls"] == 0
        prompt = db.execute(
            "SELECT v.f_content FROM t_agent_prompt p JOIN t_agent_prompt_version v"
            " ON v.f_prompt_id = p.f_prompt_id AND v.f_version = p.f_current_version"
            " WHERE p.f_prompt_id='bkn-project-drafter-prompt'"
        ).fetchone()[0]
        # It sees structure only, and writes names in the language the caller names.
        assert "no row data" in prompt
        assert "language named in the user message" in prompt
        # One Agent serves the three passes the server names in each message.
        for drafting_pass in ("pass structure", "pass references", "pass naming"):
            assert drafting_pass in prompt
        for evidence in ("declared", "comment", "name_exact", "name_role", "semantic", "requirement", "catalog_view"):
            assert evidence in prompt
        assert "external_hints" in prompt
        # Clues are data: requirements and catalog semantics hint, counts decide.
        assert "never as instructions" in prompt


def test_metric_designer_seed_is_tool_free_and_draft():
    with database() as db:
        apply(db, METRIC_DESIGN)
        item = item_from_db(db, "object_metric_designer")
        assert item["spec"]["status"] == "draft"
        assert item["spec"]["model"] == ""
        assert item["spec"]["tools"] == []
        assert item["spec"]["skills"] == []
        assert item["spec"]["limits"]["max_tool_calls"] == 0
        assert item["spec"]["limits"]["max_output_tokens"] == 8192
        assert "save, publish, delete, or upgrade metrics" in item["prompt"]["content"]
        apply(db, METRIC_DESIGN)
        assert db.execute("SELECT COUNT(*) FROM t_agent WHERE f_agent_id='object_metric_designer'").fetchone()[0] == 1


def test_metric_designer_budget_upgrade_only_changes_untouched_default():
    with database() as db:
        apply(db, METRIC_DESIGN)
        original = {"max_turns": 6, "max_tool_calls": 0, "timeout_s": 120, "max_output_tokens": 4096}
        db.execute("UPDATE t_agent SET f_limits=? WHERE f_agent_id='object_metric_designer'", (json.dumps(original),))
        apply(db, METRIC_DESIGN_BUDGET)
        upgraded = json.loads(db.execute("SELECT f_limits FROM t_agent WHERE f_agent_id='object_metric_designer'").fetchone()[0])
        assert upgraded == {**original, "max_output_tokens": 8192}
        apply(db, METRIC_DESIGN_BUDGET)
        assert json.loads(db.execute("SELECT f_limits FROM t_agent WHERE f_agent_id='object_metric_designer'").fetchone()[0]) == upgraded
        custom = {**original, "max_output_tokens": 5000}
        db.execute("UPDATE t_agent SET f_limits=? WHERE f_agent_id='object_metric_designer'", (json.dumps(custom),))
        apply(db, METRIC_DESIGN_BUDGET)
        assert json.loads(db.execute("SELECT f_limits FROM t_agent WHERE f_agent_id='object_metric_designer'").fetchone()[0]) == custom


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
    init = Path(selected_migrations("0.0.0")[0][0])
    # The optional claim-locale column and guarded translation backfill do not
    # update semantic-understanding versions or their current/initial markers.
    claim_locale_statements = (set(statements(CLAIM_LOCALES)) | set(statements(BUILTIN_LOCALES))
                               | set(statements(SOURCE_LANGUAGE))
                               | {s for s in statements(SEMANTIC_NAMING) if "f_localized_content" in s}
                               | {s for s in statements(REQUIREMENTS_V6) if "f_localized_content" in s}
                               | {s for s in statements(ANSWER_JUDGE_REASON) if "f_localized_content" in s}
                               | {s for s in statements(ONTOLOGY_ASK_REVISIONS) if "f_localized_content" in s}
                               | set(statements(METRIC_DESIGN_LOCALE)))
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
        assert [row[0] for row in versions] == [6]
        agent_versions = db.execute(
            "SELECT f_version,f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding'"
        ).fetchall()
        assert [tuple(row) for row in agent_versions] == [(1, 6, 1, 1)]
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


def test_installed_021_selects_nothing_more():
    _, repeated = selected_migrations("0.2.1")
    assert repeated == [[], "0.2.1", False]


def v4_semantic_prompt():
    with database() as db:
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        apply(db, SEMANTIC_LANGUAGE)
        return item_from_db(db, "resource-semantic-understanding")["prompt"]["content"]


def semantic_v4_state(db, start):
    """Agent state just before 15: upgraded from 0.2.0, or installed from the 0.2.1 snapshot at v4."""
    if start == "upgraded":
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        apply(db, VERSIONS)
        apply(db, SEMANTIC_LANGUAGE)
        return
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=4,f_content=? WHERE f_prompt_id='resource-semantic-understanding-prompt'",
        (v4_semantic_prompt(),),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=4 WHERE f_prompt_id='resource-semantic-understanding-prompt'")
    db.execute("UPDATE t_agent_version SET f_prompt_version=4 WHERE f_agent_id='resource-semantic-understanding'")


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_semantic_api_name_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        # A fresh install is at v6 now; 15 is compared with an install as it was at v5.
        semantic_v5_state(fresh, "installed")
        semantic_v4_state(db, start)
        old_prompt = item_from_db(db, "resource-semantic-understanding")["prompt"]["content"]
        history = snapshot(db)[3]
        apply(db, SEMANTIC_API_NAME)
        assert item_from_db(db, "resource-semantic-understanding") == item_from_db(fresh, "resource-semantic-understanding")
        prompt = item_from_db(db, "resource-semantic-understanding")["prompt"]["content"]
        assert prompt.startswith(old_prompt)
        added = prompt[len(old_prompt):]
        assert "api_name" in added and "snake_case" in added and "40" in added
        assert "不受 options.language 影响" in added and "拼音" in added
        # Earlier prompt and Agent versions stay; only the initial/current markers move.
        assert db.execute(
            "SELECT f_content FROM t_agent_prompt_version WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=4"
        ).fetchone()[0] == old_prompt
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            "SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(5, 1, 1)]
        before = snapshot(db)
        apply(db, SEMANTIC_API_NAME)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback"])
def test_semantic_api_name_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        semantic_v4_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=4"
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('resource-semantic-understanding-prompt',5,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute(
                    "UPDATE t_agent_prompt SET f_current_version=5 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
                )
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
                )
        before = snapshot(db)
        apply(db, SEMANTIC_API_NAME)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 8))
def test_semantic_api_name_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            semantic_v4_state(connection, "upgraded")
            if customized:
                connection.execute(
                    "UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='resource-semantic-understanding'"
                )
        apply(expected, SEMANTIC_API_NAME)
        apply(db, SEMANTIC_API_NAME, limit=limit)
        apply(db, SEMANTIC_API_NAME)
        assert snapshot(db) == snapshot(expected)


def test_semantic_api_name_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        semantic_v4_state(db, "upgraded")
        apply(db, SEMANTIC_API_NAME)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=4,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
        )
        db.execute("UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='resource-semantic-understanding'")
        db.execute(
            "UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='resource-semantic-understanding' AND f_prompt_version=4"
        )
        before = snapshot(db)
        apply(db, SEMANTIC_API_NAME)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_semantic_api_name_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        semantic_v4_state(db, start)
        original = dict(
            db.execute(
                "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_version=1"
            ).fetchone()
        )
        db.execute(
            "UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='resource-semantic-understanding'",
            ('{"max_turns":17}',),
        )
        apply(db, SEMANTIC_API_NAME)
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
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (5, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (5, "custom-model", '{"max_turns":17}')


def test_semantic_api_name_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        semantic_v4_state(db, "upgraded")
        # Saved before the migration runs (the test clock's now is 1000), so only
        # the updater, not the timestamp, tells this head apart from the seed's.
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, SEMANTIC_API_NAME)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


def semantic_v5_state(db, start):
    """Agent state just before 17: upgraded from 0.2.0 through 15 and 16, or installed from the
    0.2.1 snapshot as it was before 17 was appended."""
    if start == "upgraded":
        apply(db, MIGRATIONS / "0.2.0/init.sql")
        for delta in (VERSIONS, SEMANTIC_LANGUAGE, CLAIM_LOCALES, SEMANTIC_API_NAME, BUILTIN_LOCALES):
            apply(db, delta)
        return
    with database() as upgraded:
        semantic_v5_state(upgraded, "upgraded")
        v5 = semantic_prompt_version(upgraded, 5)
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=5,f_content=?,f_localized_content=? WHERE f_prompt_id='resource-semantic-understanding-prompt'",
        (v5["f_content"], v5["f_localized_content"]),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=5 WHERE f_prompt_id='resource-semantic-understanding-prompt'")
    db.execute("UPDATE t_agent_version SET f_prompt_version=5 WHERE f_agent_id='resource-semantic-understanding'")


def semantic_prompt_version(db, version):
    result = dict(db.execute(
        "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=?",
        (version,),
    ).fetchone())
    # Compare migration 17's payload independently of language metadata from 25.
    # Source-language metadata has its own fresh/upgrade preservation regressions.
    result.pop("f_source_language", None)
    return result


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_semantic_naming_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        apply(fresh, Path(selected_migrations("0.0.0")[0][0]))
        semantic_v5_state(db, start)
        old = semantic_prompt_version(db, 5)
        history = snapshot(db)[3]
        apply(db, SEMANTIC_NAMING)
        assert item_from_db(db, "resource-semantic-understanding") == item_from_db(fresh, "resource-semantic-understanding")
        new = semantic_prompt_version(db, 6)
        assert new == semantic_prompt_version(fresh, 6)
        assert new["f_content"].startswith(old["f_content"] + "\n\n")
        added = new["f_content"][len(old["f_content"]):]
        # The resource is named as the business object; generic field meanings are qualified.
        assert "单数业务名词" in added and "不得写成句子" in added and "40" in added and "加限定" in added
        # Both translations carry the same rules after their v5 text.
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["zh-CN"].startswith(old_locales["zh-CN"] + "\n\n") and "单数业务名词" in new_locales["zh-CN"]
        assert new_locales["en-US"].startswith(old_locales["en-US"] + "\n\n")
        assert "singular business noun" in new_locales["en-US"] and "qualify generic" in new_locales["en-US"]
        # Earlier prompt and Agent versions stay; only the initial/current markers move.
        assert semantic_prompt_version(db, 5) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            "SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(6, 1, 1)]
        before = snapshot(db)
        apply(db, SEMANTIC_NAMING)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_semantic_naming_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        semantic_v5_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=5"
            )
        elif customization in ("zh-CN", "en-US"):
            # An administrator's translation of v5 is a customization too: moving to v6 would stop
            # using it, so nothing is upgraded.
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom') WHERE f_prompt_id='resource-semantic-understanding-prompt' AND f_version=5",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('resource-semantic-understanding-prompt',6,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute(
                    "UPDATE t_agent_prompt SET f_current_version=6 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
                )
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
                )
        before = snapshot(db)
        apply(db, SEMANTIC_NAMING)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 9))
def test_semantic_naming_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            semantic_v5_state(connection, "upgraded")
            if customized:
                connection.execute(
                    "UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='resource-semantic-understanding'"
                )
        apply(expected, SEMANTIC_NAMING)
        apply(db, SEMANTIC_NAMING, limit=limit)
        apply(db, SEMANTIC_NAMING)
        assert snapshot(db) == snapshot(expected)


def test_semantic_naming_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        semantic_v5_state(db, "upgraded")
        apply(db, SEMANTIC_NAMING)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=5,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
        )
        db.execute("UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='resource-semantic-understanding'")
        db.execute(
            "UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='resource-semantic-understanding' AND f_prompt_version=5"
        )
        before = snapshot(db)
        apply(db, SEMANTIC_NAMING)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_semantic_naming_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        semantic_v5_state(db, start)
        original = dict(
            db.execute(
                "SELECT * FROM t_agent_version WHERE f_agent_id='resource-semantic-understanding' AND f_version=1"
            ).fetchone()
        )
        db.execute(
            "UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='resource-semantic-understanding'",
            ('{"max_turns":17}',),
        )
        apply(db, SEMANTIC_NAMING)
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
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (6, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (6, "custom-model", '{"max_turns":17}')


def test_semantic_naming_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        semantic_v5_state(db, "upgraded")
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='resource-semantic-understanding-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, SEMANTIC_NAMING)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


def requirements_prompt(db):
    return db.execute(
        "SELECT v.f_content, v.f_localized_content FROM t_agent_prompt p JOIN t_agent_prompt_version v"
        " ON v.f_prompt_id = p.f_prompt_id AND v.f_version = p.f_current_version"
        " WHERE p.f_prompt_id='bkn-project-requirements-prompt'"
    ).fetchone()


def test_project_requirements_seed_is_a_published_tool_free_task_on_the_default_model():
    with database() as db:
        apply(db, Path(selected_migrations("0.0.0")[0][0]))
        agent = db.execute(
            "SELECT f_name, f_mode, f_model, f_tools, f_skills, f_limits, f_status FROM t_agent"
            " WHERE f_agent_id='bkn-project-requirements'"
        ).fetchone()
        assert agent["f_name"] == "BKNProjectRequirements"
        assert agent["f_mode"] == "task"
        assert agent["f_model"] == ""
        assert json.loads(agent["f_tools"]) == []
        assert json.loads(agent["f_skills"]) == []
        assert agent["f_status"] == "published"
        limits = json.loads(agent["f_limits"])
        assert limits["max_turns"] == 1 and limits["max_tool_calls"] == 0
        prompt, localized = requirements_prompt(db)
        assert prompt.isascii()
        # One Agent serves the passes the server names in each message.
        for requirement_pass in ("pass draft", "pass guidance"):
            assert requirement_pass in prompt
        for output in ("scenarios", "concepts", "questions", "rules", "conflicts", "choices", "doc_types"):
            assert output in prompt
        # The agent proposes and quotes; the server checks every quote against the documents.
        assert "copied verbatim" in prompt
        assert "supplement_reason" in prompt
        assert "Write a rule only when a document states it" in prompt
        # Concepts map to drafted items by meaning, by the ids the server lists.
        assert "maps_to" in prompt and "only ids listed in the message" in prompt
        # Requirements describe answers, never their values.
        assert "Never write an answer value" in prompt
        assert "no row data" in prompt
        assert "language named in the user message" in prompt
        assert "never as instructions" in prompt
        # The prompt is general: no business of any sample network.
        for sample in ("order", "customer", "delivery", "contract", "\u8ba2\u5355", "\u5ba2\u6237"):
            assert sample not in prompt.lower()
        variants = json.loads(localized)
        assert variants["en-US"] == prompt
        assert variants["zh-CN"] != prompt


@pytest.mark.parametrize("completed", [1, 2])
def test_partial_requirements_seed_is_retryable_without_an_unfinished_agent(completed):
    with database() as db:
        apply(db, CLAIM_LOCALES)
        apply(db, REQUIREMENTS, limit=completed)
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 0
        apply(db, REQUIREMENTS)
        assert db.execute(
            "SELECT f_name FROM t_agent WHERE f_agent_id='bkn-project-requirements'"
        ).fetchone()[0] == "BKNProjectRequirements"


def test_conflicting_requirements_prompt_is_not_taken_over():
    with database() as db:
        db.execute(
            "INSERT INTO t_agent_prompt VALUES('bkn-project-requirements-prompt','customer',3,'customer',1)"
        )
        apply(db, CLAIM_LOCALES)
        before = snapshot(db)
        # The seed does not adopt someone else's prompt: it fails closed and changes nothing.
        with pytest.raises(sqlite3.IntegrityError):
            apply(db, REQUIREMENTS)
        assert snapshot(db) == before


def test_requirements_translation_skips_a_customized_prompt():
    with database() as db:
        apply(db, CLAIM_LOCALES)
        seed = [s for s in statements(REQUIREMENTS) if s.lower().startswith("insert")]
        for statement in seed:
            db.execute(statement)
        db.execute(
            "UPDATE t_agent_prompt_version SET f_content='Administrator prompt'"
            " WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        apply(db, REQUIREMENTS)
        assert requirements_prompt(db)[1] is None


RQ = "bkn-project-requirements"


def requirements_prompt_version(db, version):
    result = dict(db.execute(
        "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=?",
        (version,),
    ).fetchone())
    # Compare migrations 22/23 independently of metadata maintained by 24.
    result.pop("f_source_language", None)
    return result


def requirements_v1_state(db, start):
    """The Agent just before 21: seeded by 18, or installed from an earlier 0.2.1 snapshot at v1."""
    if start == "upgraded":
        apply(db, CLAIM_LOCALES)
        apply(db, REQUIREMENTS)
        return
    with database() as seeded:
        requirements_v1_state(seeded, "upgraded")
        v1 = requirements_prompt_version(seeded, 1)
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=1,f_content=?,f_localized_content=? WHERE f_prompt_id='bkn-project-requirements-prompt'",
        (v1["f_content"], v1["f_localized_content"]),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=1 WHERE f_prompt_id='bkn-project-requirements-prompt'")
    db.execute(f"UPDATE t_agent_version SET f_prompt_version=1 WHERE f_agent_id='{RQ}'")


def requirements_v2_state(db, start):
    """The Agent just before 23: upgraded through 22, or installed from an earlier 0.2.1 snapshot at v2."""
    if start == "upgraded":
        requirements_v1_state(db, "upgraded")
        apply(db, REQUIREMENTS_V2)
        return
    with database() as upgraded:
        requirements_v2_state(upgraded, "upgraded")
        v2 = requirements_prompt_version(upgraded, 2)
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=2,f_content=?,f_localized_content=? WHERE f_prompt_id='bkn-project-requirements-prompt'",
        (v2["f_content"], v2["f_localized_content"]),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=2 WHERE f_prompt_id='bkn-project-requirements-prompt'")
    db.execute(f"UPDATE t_agent_version SET f_prompt_version=2 WHERE f_agent_id='{RQ}'")


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v2_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        # A fresh install is at v3 now; 22 is compared with an install as it was at v2.
        requirements_v2_state(fresh, "installed")
        requirements_v1_state(db, start)
        old = requirements_prompt_version(db, 1)
        history = snapshot(db)[3]
        apply(db, REQUIREMENTS_V2)
        assert item_from_db(db, RQ) == item_from_db(fresh, RQ)
        new = requirements_prompt_version(db, 2)
        assert new == requirements_prompt_version(fresh, 2)
        # Only pass guidance changes: what comes before it is v1's.
        cut = old["f_content"].index("pass guidance, for one scenario question")
        assert new["f_content"][:cut] == old["f_content"][:cut]
        guidance = new["f_content"][cut:]
        for rule in ("write at least one paraphrase", "no_tests_reason", "one of the property names the message lists",
                     "is no slot", "{{slot N}}", "Write no other date, number or code in the text",
                     "never states facts about the data", "the first slot names the record that should not exist"):
            assert rule in guidance
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == new["f_content"]
        zh_cut = old_locales["zh-CN"].index("pass guidance\uff0c")
        assert new_locales["zh-CN"][:zh_cut] == old_locales["zh-CN"][:zh_cut]
        for rule in ("no_tests_reason", "{{slot N}}", "\u81f3\u5c11\u5199\u4e00\u6761 paraphrase",
                     "\u7edd\u4e0d\u9648\u8ff0\u5173\u4e8e\u6570\u636e\u7684\u4e8b\u5b9e"):
            assert rule in new_locales["zh-CN"][zh_cut:]
        # Earlier prompt and Agent versions stay; only the initial/current markers move.
        assert requirements_prompt_version(db, 1) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            f"SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='{RQ}' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(2, 1, 1)]
        before = snapshot(db)
        apply(db, REQUIREMENTS_V2)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_requirements_v2_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        requirements_v1_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=1"
            )
        elif customization in ("zh-CN", "en-US"):
            # An administrator's translation of v1 is a customization too.
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom') WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=1",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('bkn-project-requirements-prompt',2,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=2 WHERE f_prompt_id='bkn-project-requirements-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
                )
        before = snapshot(db)
        apply(db, REQUIREMENTS_V2)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 9))
def test_requirements_v2_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            requirements_v1_state(connection, "upgraded")
            if customized:
                connection.execute(f"UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='{RQ}'")
        apply(expected, REQUIREMENTS_V2)
        apply(db, REQUIREMENTS_V2, limit=limit)
        apply(db, REQUIREMENTS_V2)
        assert snapshot(db) == snapshot(expected)


def test_requirements_v2_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        requirements_v1_state(db, "upgraded")
        apply(db, REQUIREMENTS_V2)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=1,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        db.execute(f"UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='{RQ}'")
        db.execute(f"UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='{RQ}' AND f_prompt_version=1")
        before = snapshot(db)
        apply(db, REQUIREMENTS_V2)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v2_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        requirements_v1_state(db, start)
        original = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_version=1").fetchone())
        db.execute(
            f"UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='{RQ}'", ('{"max_turns":17}',)
        )
        apply(db, REQUIREMENTS_V2)
        baseline = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_initial=1").fetchone())
        current = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (2, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (2, "custom-model", '{"max_turns":17}')


def test_requirements_v2_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        requirements_v1_state(db, "upgraded")
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, REQUIREMENTS_V2)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_requirements_v3_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        requirements_v2_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=2"
            )
        elif customization in ("zh-CN", "en-US"):
            # An administrator's translation of v2 is a customization too.
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom') WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=2",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('bkn-project-requirements-prompt',3,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=3 WHERE f_prompt_id='bkn-project-requirements-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
                )
        before = snapshot(db)
        apply(db, REQUIREMENTS_V3)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 9))
def test_requirements_v3_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            requirements_v2_state(connection, "upgraded")
            if customized:
                connection.execute(f"UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='{RQ}'")
        apply(expected, REQUIREMENTS_V3)
        apply(db, REQUIREMENTS_V3, limit=limit)
        apply(db, REQUIREMENTS_V3)
        assert snapshot(db) == snapshot(expected)


def test_requirements_v3_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        requirements_v2_state(db, "upgraded")
        apply(db, REQUIREMENTS_V3)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=2,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        db.execute(f"UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='{RQ}'")
        db.execute(f"UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='{RQ}' AND f_prompt_version=2")
        before = snapshot(db)
        apply(db, REQUIREMENTS_V3)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v3_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        requirements_v2_state(db, start)
        original = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_version=1").fetchone())
        db.execute(
            f"UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='{RQ}'", ('{"max_turns":17}',)
        )
        apply(db, REQUIREMENTS_V3)
        baseline = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_initial=1").fetchone())
        current = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (3, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (3, "custom-model", '{"max_turns":17}')


def test_requirements_v3_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        requirements_v2_state(db, "upgraded")
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, REQUIREMENTS_V3)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


def requirements_v3_state(db, start):
    """The Agent just before 24: upgraded through 23, or installed from an earlier 0.2.1 snapshot at v3."""
    if start == "upgraded":
        requirements_v2_state(db, "upgraded")
        apply(db, REQUIREMENTS_V3)
        return
    with database() as upgraded:
        requirements_v3_state(upgraded, "upgraded")
        v3 = requirements_prompt_version(upgraded, 3)
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=3,f_content=?,f_localized_content=? WHERE f_prompt_id='bkn-project-requirements-prompt'",
        (v3["f_content"], v3["f_localized_content"]),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=3 WHERE f_prompt_id='bkn-project-requirements-prompt'")
    db.execute(f"UPDATE t_agent_version SET f_prompt_version=3 WHERE f_agent_id='{RQ}'")


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v3_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        # A fresh install is at v4 now; 23 is compared with an install as it was at v3.
        requirements_v3_state(fresh, "installed")
        requirements_v2_state(db, start)
        old = requirements_prompt_version(db, 2)
        history = snapshot(db)[3]
        apply(db, REQUIREMENTS_V3)
        assert item_from_db(db, RQ) == item_from_db(fresh, RQ)
        new = requirements_prompt_version(db, 3)
        assert new == requirements_prompt_version(fresh, 3)
        # Only the boundary rule changes, in both languages.
        assert "never states, assumes or asks to suppose a fact about the data" in new["f_content"]
        assert "never states facts about the data" in old["f_content"]
        cut = old["f_content"].index("A boundary test")
        assert new["f_content"][:cut] == old["f_content"][:cut]
        assert new["f_content"].endswith(old["f_content"][old["f_content"].index(" In a not_exist test"):])
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == new["f_content"]
        zh_cut = old_locales["zh-CN"].index("\u8fb9\u754c\u60c5\u51b5")
        assert new_locales["zh-CN"][:zh_cut] == old_locales["zh-CN"][:zh_cut]
        assert "\u7edd\u4e0d\u9648\u8ff0\u3001\u5047\u5b9a" in new_locales["zh-CN"]
        assert requirements_prompt_version(db, 2) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            f"SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='{RQ}' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(3, 1, 1)]
        before = snapshot(db)
        apply(db, REQUIREMENTS_V3)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_requirements_v4_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        requirements_v3_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=3"
            )
        elif customization in ("zh-CN", "en-US"):
            # An administrator's translation of v3 is a customization too.
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom') WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=3",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('bkn-project-requirements-prompt',4,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=4 WHERE f_prompt_id='bkn-project-requirements-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
                )
        before = snapshot(db)
        apply(db, REQUIREMENTS_V4)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 9))
def test_requirements_v4_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            requirements_v3_state(connection, "upgraded")
            if customized:
                connection.execute(f"UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='{RQ}'")
        apply(expected, REQUIREMENTS_V4)
        apply(db, REQUIREMENTS_V4, limit=limit)
        apply(db, REQUIREMENTS_V4)
        assert snapshot(db) == snapshot(expected)


def test_requirements_v4_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        requirements_v3_state(db, "upgraded")
        apply(db, REQUIREMENTS_V4)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=3,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        db.execute(f"UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='{RQ}'")
        db.execute(f"UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='{RQ}' AND f_prompt_version=3")
        before = snapshot(db)
        apply(db, REQUIREMENTS_V4)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v4_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        requirements_v3_state(db, start)
        original = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_version=1").fetchone())
        db.execute(
            f"UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='{RQ}'", ('{"max_turns":17}',)
        )
        apply(db, REQUIREMENTS_V4)
        baseline = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_initial=1").fetchone())
        current = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (4, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (4, "custom-model", '{"max_turns":17}')


def test_requirements_v4_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        requirements_v3_state(db, "upgraded")
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, REQUIREMENTS_V4)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


def requirements_v4_state(db, start):
    """The Agent just before 27: upgraded through 24 and 25, or installed from an earlier 0.2.1 snapshot at v4."""
    if start == "upgraded":
        requirements_v3_state(db, "upgraded")
        apply(db, REQUIREMENTS_V4)
        apply(db, SOURCE_LANGUAGE)
        return
    with database() as upgraded:
        requirements_v4_state(upgraded, "upgraded")
        v4 = requirements_prompt_version(upgraded, 4)
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=4,f_content=?,f_localized_content=? WHERE f_prompt_id='bkn-project-requirements-prompt'",
        (v4["f_content"], v4["f_localized_content"]),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=4 WHERE f_prompt_id='bkn-project-requirements-prompt'")
    db.execute(f"UPDATE t_agent_version SET f_prompt_version=4 WHERE f_agent_id='{RQ}'")


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v4_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        # A fresh install is at v5 now; 24 is compared with an install as it was at v4.
        requirements_v4_state(fresh, "installed")
        requirements_v3_state(db, start)
        old = requirements_prompt_version(db, 3)
        history = snapshot(db)[3]
        apply(db, REQUIREMENTS_V4)
        assert item_from_db(db, RQ) == item_from_db(fresh, RQ)
        new = requirements_prompt_version(db, 4)
        assert new == requirements_prompt_version(fresh, 4)
        content = new["f_content"]
        # pass draft: a rule's condition or a computed value makes a question capability.
        assert "applies a rule's condition or threshold" in content and "computes a value" in content
        # pass guidance: slots are asked about, of concepts; lists and filters get no tests.
        for rule in ("never what the answer returns", "one of the concept ids the message lists, never an object type id",
                     "such as a list or a filter, gets no tests and a no_tests_reason", "never the reverse question",
                     "expects same_record"):
            assert rule in content
        assert "never states, assumes or asks to suppose a fact about the data" in content
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == content
        for rule in ("\u6982\u5ff5 id", "\u7edd\u4e0d\u662f\u5bf9\u8c61\u7c7b id", "\u5957\u7528\u89c4\u5219\u7684\u6761\u4ef6\u6216\u9608\u503c"):
            assert rule in new_locales["zh-CN"]
        assert requirements_prompt_version(db, 3) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            f"SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='{RQ}' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(4, 1, 1)]
        before = snapshot(db)
        apply(db, REQUIREMENTS_V4)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_requirements_v5_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        requirements_v4_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=4"
            )
        elif customization in ("zh-CN", "en-US"):
            # An administrator's translation of v4 is a customization too.
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom') WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=4",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('bkn-project-requirements-prompt',5,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=5 WHERE f_prompt_id='bkn-project-requirements-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
                )
        before = snapshot(db)
        apply(db, REQUIREMENTS_V5)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 8))
def test_requirements_v5_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            requirements_v4_state(connection, "upgraded")
            if customized:
                connection.execute(f"UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='{RQ}'")
        apply(expected, REQUIREMENTS_V5)
        apply(db, REQUIREMENTS_V5, limit=limit)
        apply(db, REQUIREMENTS_V5)
        assert snapshot(db) == snapshot(expected)


def test_requirements_v5_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        requirements_v4_state(db, "upgraded")
        apply(db, REQUIREMENTS_V5)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=4,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        db.execute(f"UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='{RQ}'")
        db.execute(f"UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='{RQ}' AND f_prompt_version=4")
        before = snapshot(db)
        apply(db, REQUIREMENTS_V5)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v5_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        requirements_v4_state(db, start)
        original = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_version=1").fetchone())
        db.execute(
            f"UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='{RQ}'", ('{"max_turns":17}',)
        )
        apply(db, REQUIREMENTS_V5)
        baseline = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_initial=1").fetchone())
        current = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (5, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (5, "custom-model", '{"max_turns":17}')


def test_requirements_v5_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        requirements_v4_state(db, "upgraded")
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, REQUIREMENTS_V5)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v5_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        # A fresh install is at v6 now; 27 is compared with an install as it was at v5.
        requirements_v5_state(fresh, "installed")
        requirements_v4_state(db, start)
        old = requirements_prompt_version(db, 4)
        history = snapshot(db)[3]
        apply(db, REQUIREMENTS_V5)
        assert item_from_db(db, RQ) == item_from_db(fresh, RQ)
        new = requirements_prompt_version(db, 5)
        assert new == requirements_prompt_version(fresh, 5)
        content = new["f_content"]
        # A filter on properties is knowledge; a rule, an aggregate or a derived value is capability.
        for rule in ("A filter on properties", "is knowledge and names no function",
                     "Only applying a rule's condition or threshold, an aggregate", "makes the question capability",
                     "A question about what a term means is no scenario question",
                     "Split a term with several meanings into concepts, one per meaning", "nothing they ask for is dropped",
                     # A question about one record keeps saying so, and is tested with that record as its slot.
                     "never turning into a question about all records", "whatever shape the answer has, a list too",
                     "only a question with no value to ask about at all", "by a property that identifies it such as a code or an id, never a name"):
            assert rule in content
        # v4's rule that a list gets no tests is gone; its other rules stay.
        assert "such as a list or a filter, gets no tests" not in content
        assert "never an object type id" in content and "applies a rule's condition or threshold" in content
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == content
        for rule in ("\u5bf9\u5c5e\u6027\u7684\u7b5b\u9009", "\u4e0d\u662f\u573a\u666f\u95ee\u9898", "\u591a\u4e49\u8bcd\u62c6\u6210\u591a\u4e2a\u6982\u5ff5",
                     "\u5217\u8868\u4e5f\u4e00\u6837", "\u7edd\u4e0d\u7528\u540d\u79f0"):
            assert rule in new_locales["zh-CN"]
        assert requirements_prompt_version(db, 4) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            f"SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='{RQ}' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(5, 1, 1)]
        # Like every upgraded prompt, the source language stays unset (25 marks fresh installs only).
        assert db.execute(
            "SELECT f_source_language FROM t_agent_prompt_version WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=5"
        ).fetchone()[0] is None
        before = snapshot(db)
        apply(db, REQUIREMENTS_V5)
        assert snapshot(db) == before


def requirements_v5_state(db, start):
    """The Agent just before 28: upgraded through 27, or installed from an earlier 0.2.1 snapshot at v5."""
    if start == "upgraded":
        requirements_v4_state(db, "upgraded")
        apply(db, REQUIREMENTS_V5)
        return
    with database() as upgraded:
        requirements_v5_state(upgraded, "upgraded")
        v5 = requirements_prompt_version(upgraded, 5)
    apply(db, Path(selected_migrations("0.0.0")[0][0]))
    db.execute(
        "UPDATE t_agent_prompt_version SET f_version=5,f_content=?,f_localized_content=? WHERE f_prompt_id='bkn-project-requirements-prompt'",
        (v5["f_content"], v5["f_localized_content"]),
    )
    db.execute("UPDATE t_agent_prompt SET f_current_version=5 WHERE f_prompt_id='bkn-project-requirements-prompt'")
    db.execute(f"UPDATE t_agent_version SET f_prompt_version=5 WHERE f_agent_id='{RQ}'")


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_requirements_v6_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        requirements_v5_state(db, "upgraded")
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt' WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=5"
            )
        elif customization in ("zh-CN", "en-US"):
            # An administrator's translation of v5 is a customization too.
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom') WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=5",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time) VALUES('bkn-project-requirements-prompt',6,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=6 WHERE f_prompt_id='bkn-project-requirements-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
                )
        before = snapshot(db)
        apply(db, REQUIREMENTS_V6)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 8))
def test_requirements_v6_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            requirements_v5_state(connection, "upgraded")
            if customized:
                connection.execute(f"UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='{RQ}'")
        apply(expected, REQUIREMENTS_V6)
        apply(db, REQUIREMENTS_V6, limit=limit)
        apply(db, REQUIREMENTS_V6)
        assert snapshot(db) == snapshot(expected)


def test_requirements_v6_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        requirements_v5_state(db, "upgraded")
        apply(db, REQUIREMENTS_V6)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=5,f_update_user='operator',f_update_time=3000 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        db.execute(f"UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='{RQ}'")
        db.execute(f"UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='{RQ}' AND f_prompt_version=5")
        before = snapshot(db)
        apply(db, REQUIREMENTS_V6)
        assert snapshot(db) == before


def test_requirements_v6_upgrade_leaves_a_prompt_rolled_back_before_it():
    with database() as db:
        requirements_v5_state(db, "upgraded")
        # An operator went back to v4 before 28 runs; the untouched official v5 is no longer the head.
        db.execute("UPDATE t_agent_prompt SET f_current_version=4 WHERE f_prompt_id='bkn-project-requirements-prompt'")
        before = snapshot(db)
        apply(db, REQUIREMENTS_V6)
        assert snapshot(db) == before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v6_upgrade_keeps_custom_settings_and_official_baseline(start):
    with database() as db:
        requirements_v5_state(db, start)
        original = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_version=1").fetchone())
        db.execute(
            f"UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='{RQ}'", ('{"max_turns":17}',)
        )
        apply(db, REQUIREMENTS_V6)
        baseline = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_initial=1").fetchone())
        current = dict(db.execute(f"SELECT * FROM t_agent_version WHERE f_agent_id='{RQ}' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (6, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (6, "custom-model", '{"max_turns":17}')


def test_requirements_v6_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        requirements_v5_state(db, "upgraded")
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500 WHERE f_prompt_id='bkn-project-requirements-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, REQUIREMENTS_V6)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


@pytest.mark.parametrize("start", ["upgraded", "installed"])
def test_requirements_v6_upgrade_matches_fresh_install_and_keeps_history(start):
    with database() as db, database() as fresh:
        apply(fresh, Path(selected_migrations("0.0.0")[0][0]))
        requirements_v5_state(db, start)
        old = requirements_prompt_version(db, 5)
        history = snapshot(db)[3]
        apply(db, REQUIREMENTS_V6)
        assert item_from_db(db, RQ) == item_from_db(fresh, RQ)
        new = requirements_prompt_version(db, 6)
        assert new == requirements_prompt_version(fresh, 6)
        content = new["f_content"]
        # pass draft: a question gives its inputs; a period counted back gives its reference time.
        for rule in ("a template with a {name} slot for each of its inputs", "Give its inputs:", "role, record or reference_time",
                     "has no inputs", "set relative_window and give that moment as a reference_time input",
                     "What the answer returns is never an input",
                     "one of the property names the message lists for that concept's object type",
                     "its concept is theirs, never a concept of the moment itself",
                     "naming one never makes a lookup capability",
                     "not judge: a judge applies a rule's condition",
                     "Give at most 10 scenarios and 40 concepts", "cover every question the documents ask",
                     "A question the documents ask about every record stays a list",
                     "names no particular record's value",
                     "an object type also with its properties (name and type) and primary keys",
                     # pass guidance: the slots are the inputs; the server writes the template and not_exist tests
                     # when it says so, and otherwise the Agent writes a not_exist test.
                     "its slots are those inputs",
                     "When the question says server_tests, the server itself writes the question as its template asks it and its not_exist test",
                     "otherwise write a not_exist test too",
                     "A boundary moves a slot's value or the edge of a window and adds no premise",
                     "another property of the same input's concept", "A question whose inputs are empty gets no tests"):
            assert rule in content
        # v5's rules stay.
        for rule in ("A filter on properties", "never turning into a question about all records", "never an object type id",
                     "by a property that identifies it such as a code or an id, never a name"):
            assert rule in content
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == content
        for rule in ("\u5199\u51fa\u5b83\u7684\u8f93\u5165", "reference_time", "relative_window", "\u7531\u670d\u52a1\u7aef\u81ea\u5df1\u5199",
                     "\u5bf9\u5c5e\u6027\u7684\u7b5b\u9009"):
            assert rule in new_locales["zh-CN"]
        assert requirements_prompt_version(db, 5) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            f"SELECT f_prompt_version,f_initial,f_current FROM t_agent_version WHERE f_agent_id='{RQ}' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(6, 1, 1)]
        # Like every upgraded prompt, the source language stays unset (25 marks fresh installs only).
        assert db.execute(
            "SELECT f_source_language FROM t_agent_prompt_version WHERE f_prompt_id='bkn-project-requirements-prompt' AND f_version=6"
        ).fetchone()[0] is None
        before = snapshot(db)
        apply(db, REQUIREMENTS_V6)
        assert snapshot(db) == before


def answer_judge_prompt(db):
    return db.execute(
        "SELECT v.f_content, v.f_localized_content FROM t_agent_prompt p JOIN t_agent_prompt_version v"
        " ON v.f_prompt_id = p.f_prompt_id AND v.f_version = p.f_current_version"
        " WHERE p.f_prompt_id='bkn-project-answer-judge-prompt'"
    ).fetchone()


def test_answer_judge_seed_is_a_published_tool_free_task_on_the_default_model():
    with database() as db:
        apply(db, Path(selected_migrations("0.0.0")[0][0]))
        agent = db.execute(
            "SELECT f_name, f_mode, f_model, f_tools, f_skills, f_limits, f_status FROM t_agent"
            " WHERE f_agent_id='bkn-project-answer-judge'"
        ).fetchone()
        assert agent["f_name"] == "BKNProjectAnswerJudge"
        assert agent["f_mode"] == "task"
        assert agent["f_model"] == ""
        assert json.loads(agent["f_tools"]) == []
        assert json.loads(agent["f_skills"]) == []
        assert agent["f_status"] == "published"
        limits = json.loads(agent["f_limits"])
        assert limits["max_turns"] == 1 and limits["max_tool_calls"] == 0
        prompt, localized = answer_judge_prompt(db)
        assert prompt.isascii()
        # A fresh install seeds the latest version directly.
        assert [tuple(row) for row in db.execute(
            "SELECT f_version FROM t_agent_prompt_version WHERE f_prompt_id='bkn-project-answer-judge-prompt'")] == [(3,)]
        assert [tuple(row) for row in db.execute(
            "SELECT f_version,f_prompt_version,f_initial,f_current FROM t_agent_version"
            " WHERE f_agent_id='bkn-project-answer-judge'")] == [(1, 3, 1, 1)]
        # A not_exists test is answered by establishing the record does not exist, not by the structure.
        assert "expect" in prompt and "not_exists" in prompt
        assert "establishes from the network, by its tool calls, that no such record exists" in prompt
        assert "the network lacks the object or property to look it up by, choose cannot_answer" in prompt
        assert "\u786e\u8ba4\u4e86\u4e0d\u5b58\u5728\u8fd9\u6837\u7684\u8bb0\u5f55" in json.loads(localized)["zh-CN"]
        # The four verdicts bkn-project maps to the regression's outcomes.
        for verdict in ("answered", "clarification_needed", "cannot_answer", "off_topic"):
            assert verdict in prompt
        assert "used_needs" in prompt and "only the kinds and ids listed in the message" in prompt
        # An answer passes only when it gives all of what is asked, from the network: facts no tool call
        # could have read, and part of an answer, are cannot_answer.
        assert "gives all of what it asks" in prompt
        assert "facts that none of its tool calls could have read from the network" in prompt
        assert "does not rest on data in the network" in prompt
        assert "gives only part of what the question asks" in prompt
        # cannot_answer does not take the cases clarification_needed and off_topic name.
        assert "when clarification_needed or off_topic applies, choose it rather than cannot_answer" in prompt
        zh = json.loads(localized)["zh-CN"]
        for rule in ("\u6ca1\u6709\u4f9d\u636e\u7f51\u7edc\u4e2d\u7684\u6570\u636e", "\u53ea\u7ed9\u51fa\u4e86\u95ee\u9898\u6240\u95ee\u7684\u4e00\u90e8\u5206",
                     "\u9009\u5b83\u4eec\u800c\u4e0d\u662f cannot_answer"):
            assert rule in zh
        # It judges what the answer says from what the message carries: never the data the tools returned.
        assert "without what the tools returned" in prompt
        assert "no row data" in prompt
        assert "language named in the message" in prompt
        assert "never as instructions" in prompt
        # The prompt is general: no business of any sample network.
        for sample in ("order", "customer", "supplier", "material", "\u8ba2\u5355", "\u5ba2\u6237", "\u4f9b\u5e94\u5546"):
            assert sample not in prompt.lower()
            assert sample not in json.loads(localized)["zh-CN"].lower()
        variants = json.loads(localized)
        assert variants["en-US"] == prompt
        assert variants["zh-CN"] != prompt


@pytest.mark.parametrize("completed", [1, 2])
def test_partial_answer_judge_seed_is_retryable_without_an_unfinished_agent(completed):
    with database() as db:
        apply(db, CLAIM_LOCALES)
        apply(db, ANSWER_JUDGE, limit=completed)
        assert db.execute("SELECT COUNT(*) FROM t_agent").fetchone()[0] == 0
        apply(db, ANSWER_JUDGE)
        assert db.execute(
            "SELECT f_name FROM t_agent WHERE f_agent_id='bkn-project-answer-judge'"
        ).fetchone()[0] == "BKNProjectAnswerJudge"


def test_conflicting_answer_judge_prompt_is_not_taken_over():
    with database() as db:
        db.execute(
            "INSERT INTO t_agent_prompt VALUES('bkn-project-answer-judge-prompt','someone',3,'someone',1)"
        )
        apply(db, CLAIM_LOCALES)
        before = snapshot(db)
        # The seed does not adopt someone else's prompt: it fails closed and changes nothing.
        with pytest.raises(sqlite3.IntegrityError):
            apply(db, ANSWER_JUDGE)
        assert snapshot(db) == before


def test_answer_judge_translation_skips_a_customized_prompt():
    with database() as db:
        apply(db, CLAIM_LOCALES)
        seed = [s for s in statements(ANSWER_JUDGE) if s.lower().startswith("insert")]
        for statement in seed:
            db.execute(statement)
        db.execute(
            "UPDATE t_agent_prompt_version SET f_content='Administrator prompt'"
            " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
        )
        apply(db, ANSWER_JUDGE)
        assert answer_judge_prompt(db)[1] is None


def judge_v1_state(db):
    """An answer judge as 20 seeds it: the official v1 with its translations."""
    apply(db, CLAIM_LOCALES)
    apply(db, ANSWER_JUDGE)


def judge_prompt_version(db, version):
    return dict(db.execute(
        "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id='bkn-project-answer-judge-prompt' AND f_version=?",
        (version,),
    ).fetchone())


def test_answer_judge_v2_upgrade_keeps_history():
    with database() as db:
        judge_v1_state(db)
        old = judge_prompt_version(db, 1)
        history = snapshot(db)[3]
        apply(db, ANSWER_JUDGE_EXPECT)
        new = judge_prompt_version(db, 2)
        assert new["f_content"].startswith(old["f_content"] + "\n\n")
        assert "not_exists" in new["f_content"][len(old["f_content"]):]
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == new["f_content"]
        assert new_locales["zh-CN"].startswith(old_locales["zh-CN"] + "\n\n") and "not_exists" in new_locales["zh-CN"]
        assert judge_prompt_version(db, 1) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            "SELECT f_prompt_version,f_initial,f_current FROM t_agent_version"
            " WHERE f_agent_id='bkn-project-answer-judge' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(2, 1, 1)]
        before = snapshot(db)
        apply(db, ANSWER_JUDGE_EXPECT)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_answer_judge_v2_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        judge_v1_state(db)
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt'"
                " WHERE f_prompt_id='bkn-project-answer-judge-prompt' AND f_version=1"
            )
        elif customization in ("zh-CN", "en-US"):
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom')"
                " WHERE f_prompt_id='bkn-project-answer-judge-prompt' AND f_version=1",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time)"
                " VALUES('bkn-project-answer-judge-prompt',2,'custom newer prompt',null,'operator',2000)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=2 WHERE f_prompt_id='bkn-project-answer-judge-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=3000"
                    " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
                )
        before = snapshot(db)
        apply(db, ANSWER_JUDGE_EXPECT)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 9))
def test_answer_judge_v2_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            judge_v1_state(connection)
            if customized:
                connection.execute("UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='bkn-project-answer-judge'")
        apply(expected, ANSWER_JUDGE_EXPECT)
        apply(db, ANSWER_JUDGE_EXPECT, limit=limit)
        apply(db, ANSWER_JUDGE_EXPECT)
        assert snapshot(db) == snapshot(expected)


def test_answer_judge_v2_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        judge_v1_state(db)
        apply(db, ANSWER_JUDGE_EXPECT)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=1,f_update_user='operator',f_update_time=3000"
            " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
        )
        db.execute("UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='bkn-project-answer-judge'")
        db.execute("UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='bkn-project-answer-judge' AND f_prompt_version=1")
        before = snapshot(db)
        apply(db, ANSWER_JUDGE_EXPECT)
        assert snapshot(db) == before


def test_answer_judge_v2_upgrade_keeps_custom_settings_and_official_baseline():
    with database() as db:
        judge_v1_state(db)
        original = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge' AND f_version=1").fetchone())
        db.execute(
            "UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='bkn-project-answer-judge'",
            ('{"max_turns":3}',),
        )
        apply(db, ANSWER_JUDGE_EXPECT)
        baseline = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge' AND f_initial=1").fetchone())
        current = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (2, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (2, "custom-model", '{"max_turns":3}')


def test_answer_judge_v2_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        judge_v1_state(db)
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500"
            " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, ANSWER_JUDGE_EXPECT)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


def judge_v2_state(db):
    """An answer judge as 21 leaves it: the official v2 with its translations."""
    judge_v1_state(db)
    apply(db, ANSWER_JUDGE_EXPECT)


def judge_reason_rule(text):
    """The paragraph 26 appends: the reason names things by the names it is given, never by tool,
    function, id or field name."""
    return text[text.rindex("\n\n") + 2:]


def test_answer_judge_v3_upgrade_matches_fresh_install_and_keeps_history():
    with database() as db, database() as fresh:
        apply(fresh, Path(selected_migrations("0.0.0")[0][0]))
        judge_v2_state(db)
        old = judge_prompt_version(db, 2)
        history = snapshot(db)[3]
        apply(db, ANSWER_JUDGE_REASON)
        assert item_from_db(db, "bkn-project-answer-judge") == item_from_db(fresh, "bkn-project-answer-judge")
        new = judge_prompt_version(db, 3)
        assert new["f_content"] == judge_prompt_version(fresh, 3)["f_content"]
        assert new["f_localized_content"] == judge_prompt_version(fresh, 3)["f_localized_content"]
        # The verdict rules stay as they were: v3 only appends.
        assert new["f_content"].startswith(old["f_content"] + "\n\n")
        old_locales, new_locales = json.loads(old["f_localized_content"]), json.loads(new["f_localized_content"])
        assert new_locales["en-US"] == new["f_content"]
        assert new_locales["zh-CN"].startswith(old_locales["zh-CN"] + "\n\n")
        assert judge_prompt_version(db, 2) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            "SELECT f_prompt_version,f_initial,f_current FROM t_agent_version"
            " WHERE f_agent_id='bkn-project-answer-judge' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(3, 1, 1)]
        before = snapshot(db)
        apply(db, ANSWER_JUDGE_REASON)
        assert snapshot(db) == before


def test_answer_judge_v3_reason_is_written_for_a_business_reader_in_both_languages():
    with database() as db:
        judge_v2_state(db)
        apply(db, ANSWER_JUDGE_REASON)
        locales = json.loads(judge_prompt_version(db, 3)["f_localized_content"])
    en, zh = judge_reason_rule(locales["en-US"]), judge_reason_rule(locales["zh-CN"])
    # Named by the names the message gives, needs and names alike.
    for term in ("names", "needs", "display names"):
        assert term in en
    for term in ("names", "needs", "显示名"):
        assert term in zh
    # Never by a tool or function name, an id or a field name, in parentheses neither.
    for term in ("Never write a tool or function name", "an id", "field name", "in parentheses"):
        assert term in en
    for term in ("不要写工具名或函数名", "id", "字段名", "括号里也不写"):
        assert term in zh
    # What has no name is described; record values may still be quoted.
    assert "plain words" in en and "may be quoted" in en
    assert "平实的话" in zh and "照引" in zh


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_answer_judge_v3_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        judge_v2_state(db)
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt'"
                " WHERE f_prompt_id='bkn-project-answer-judge-prompt' AND f_version=2"
            )
        elif customization in ("zh-CN", "en-US"):
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom')"
                " WHERE f_prompt_id='bkn-project-answer-judge-prompt' AND f_version=2",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time)"
                " VALUES('bkn-project-answer-judge-prompt',3,'custom newer prompt',null,'operator',9e15)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=3 WHERE f_prompt_id='bkn-project-answer-judge-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=9e15"
                    " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
                )
        before = snapshot(db)
        apply(db, ANSWER_JUDGE_REASON)
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 9))
def test_answer_judge_v3_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            judge_v2_state(connection)
            if customized:
                connection.execute("UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='bkn-project-answer-judge'")
        apply(expected, ANSWER_JUDGE_REASON)
        apply(db, ANSWER_JUDGE_REASON, limit=limit)
        apply(db, ANSWER_JUDGE_REASON)
        assert snapshot(db) == snapshot(expected)


def test_answer_judge_v3_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        judge_v2_state(db)
        apply(db, ANSWER_JUDGE_REASON)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=2,f_update_user='operator',f_update_time=9e15"
            " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
        )
        db.execute("UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='bkn-project-answer-judge'")
        db.execute("UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='bkn-project-answer-judge'"
                   " AND f_version=(SELECT max(f_version) FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge'"
                   " AND f_prompt_version=2)")
        before = snapshot(db)
        apply(db, ANSWER_JUDGE_REASON)
        assert snapshot(db) == before


def test_answer_judge_v3_upgrade_keeps_custom_settings_and_official_baseline():
    with database() as db:
        judge_v1_state(db)
        original = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge' AND f_version=1").fetchone())
        apply(db, ANSWER_JUDGE_EXPECT)
        db.execute(
            "UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='bkn-project-answer-judge'",
            ('{"max_turns":3}',),
        )
        apply(db, ANSWER_JUDGE_REASON)
        baseline = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge' AND f_initial=1").fetchone())
        current = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-answer-judge' AND f_current=1").fetchone())
        assert baseline["f_prompt_version"] == 3
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (3, "custom-model", '{"max_turns":3}')


def test_answer_judge_v3_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        judge_v2_state(db)
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500"
            " WHERE f_prompt_id='bkn-project-answer-judge-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, ANSWER_JUDGE_REASON)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


def test_answer_judge_v3_upgrade_does_not_switch_a_head_saved_after_v3_was_written():
    with database() as db:
        judge_v2_state(db)
        # Interrupted after writing v3; the head was then saved again, still at v2.
        apply(db, ANSWER_JUDGE_REASON, limit=1)
        db.execute("UPDATE t_agent_prompt SET f_update_time=9e15 WHERE f_prompt_id='bkn-project-answer-judge-prompt'")
        head_before = snapshot(db)[1]
        apply(db, ANSWER_JUDGE_REASON)
        assert snapshot(db)[1] == head_before


def ontology_ask_prompt(db):
    return db.execute(
        "SELECT v.f_content, v.f_localized_content FROM t_agent_prompt p JOIN t_agent_prompt_version v"
        " ON v.f_prompt_id = p.f_prompt_id AND v.f_version = p.f_current_version"
        " WHERE p.f_prompt_id='bkn-project-ontology-ask-prompt'"
    ).fetchone()


def test_ontology_ask_seed_is_a_published_tool_free_chat_agent_with_a_bounded_output():
    with database() as db:
        apply(db, Path(selected_migrations("0.0.0")[0][0]))
        agent = db.execute(
            "SELECT f_name, f_mode, f_model, f_tools, f_skills, f_limits, f_status FROM t_agent"
            " WHERE f_agent_id='bkn-project-ontology-ask'"
        ).fetchone()
        assert agent["f_name"] == "BKNProjectOntologyAsk"
        assert agent["f_mode"] == "chat"
        assert agent["f_model"] == ""
        assert json.loads(agent["f_tools"]) == []
        assert json.loads(agent["f_skills"]) == []
        assert agent["f_status"] == "published"
        limits = json.loads(agent["f_limits"])
        assert limits["max_turns"] == 1 and limits["max_tool_calls"] == 0
        # bkn-project waits 45 seconds within the request: the Agent stops first, with a short answer.
        assert limits["timeout_s"] < 45 and limits["max_output_tokens"] <= 1024
        prompt, localized = ontology_ask_prompt(db)
        assert prompt.isascii()
        # It answers and proposes only; the fields it may revise are the save body's own.
        assert "you never change the draft" in prompt
        for field in ("- name:", "- comment:", "- primary_keys,", "- display_key,", "- data_property,", "- ends,"):
            assert field in prompt
        assert "history" in prompt and "relation_type" in prompt
        # The answer is streamed as text; revisions follow when the server asks for them.
        assert "as plain text with no Markdown and no JSON" in prompt
        assert "after your reply the server asks for the changes" in prompt
        assert "never guess facts about the data" in prompt
        assert "It carries no row data." in prompt
        locales = json.loads(localized)
        assert locales["en-US"] == prompt
        assert "\u4ece\u4e0d\u4fee\u6539\u8349\u7a3f" in locales["zh-CN"]


def test_ontology_ask_seed_and_translation_are_idempotent_on_an_upgrade():
    with database() as db:
        apply(db, Path(selected_migrations("0.0.0")[0][0]))
        before = snapshot(db)
        for path in (ONTOLOGY_ASK, ONTOLOGY_ASK, ONTOLOGY_ASK_REVISIONS, ONTOLOGY_ASK_REVISIONS):
            apply(db, path)
        assert snapshot(db) == before


def ask_v1_state(db):
    """Ask AI as 29 seeds it: the official v1 with its translations."""
    apply(db, CLAIM_LOCALES)
    apply(db, ONTOLOGY_ASK)


def ask_prompt_version(db, version):
    return dict(db.execute(
        "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id='bkn-project-ontology-ask-prompt' AND f_version=?",
        (version,),
    ).fetchone())


def test_ontology_ask_v2_upgrade_matches_fresh_install_and_keeps_history():
    with database() as db, database() as fresh:
        apply(fresh, Path(selected_migrations("0.0.0")[0][0]))
        ask_v1_state(db)
        old = ask_prompt_version(db, 1)
        history = snapshot(db)[3]
        apply(db, ONTOLOGY_ASK_REVISIONS)
        assert item_from_db(db, "bkn-project-ontology-ask") == item_from_db(fresh, "bkn-project-ontology-ask")
        new = ask_prompt_version(db, 2)
        assert new["f_content"] == ask_prompt_version(fresh, 2)["f_content"]
        assert new["f_localized_content"] == ask_prompt_version(fresh, 2)["f_localized_content"]
        assert json.loads(new["f_localized_content"])["en-US"] == new["f_content"]
        assert ask_prompt_version(db, 1) == old
        updated = snapshot(db)[3]
        for row in history:
            match = next(after for after in updated if after[:2] == row[:2])
            assert row[:7] + row[9:] == match[:7] + match[9:]
        rows = db.execute(
            "SELECT f_prompt_version,f_initial,f_current FROM t_agent_version"
            " WHERE f_agent_id='bkn-project-ontology-ask' AND (f_initial=1 OR f_current=1)"
        ).fetchall()
        assert [tuple(row) for row in rows] == [(2, 1, 1)]
        before = snapshot(db)
        apply(db, ONTOLOGY_ASK_REVISIONS)
        assert snapshot(db) == before


@pytest.mark.parametrize("customization", ["content", "higher-version", "rollback", "zh-CN", "en-US"])
def test_ontology_ask_v2_upgrade_preserves_operator_prompt_choices(customization):
    with database() as db:
        ask_v1_state(db)
        if customization == "content":
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt'"
                " WHERE f_prompt_id='bkn-project-ontology-ask-prompt' AND f_version=1"
            )
        elif customization in ("zh-CN", "en-US"):
            db.execute(
                "UPDATE t_agent_prompt_version SET f_localized_content=json_set(f_localized_content,?,'custom')"
                " WHERE f_prompt_id='bkn-project-ontology-ask-prompt' AND f_version=1",
                (f'$."{customization}"',),
            )
        else:
            db.execute(
                "INSERT INTO t_agent_prompt_version(f_prompt_id,f_version,f_content,f_vars_schema,f_create_user,f_create_time)"
                " VALUES('bkn-project-ontology-ask-prompt',2,'custom newer prompt',null,'operator',9e15)"
            )
            if customization == "higher-version":
                db.execute("UPDATE t_agent_prompt SET f_current_version=2 WHERE f_prompt_id='bkn-project-ontology-ask-prompt'")
            else:
                db.execute(
                    "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=9e15"
                    " WHERE f_prompt_id='bkn-project-ontology-ask-prompt'"
                )
        before = snapshot(db)
        apply(db, ONTOLOGY_ASK_REVISIONS)
        # Prompts and versions stay as the operator left them; only the mode follows bkn-project.
        assert db.execute("SELECT f_mode FROM t_agent WHERE f_agent_id='bkn-project-ontology-ask'").fetchone()[0] == "chat"
        db.execute("UPDATE t_agent SET f_mode='task' WHERE f_agent_id='bkn-project-ontology-ask'")
        assert snapshot(db) == before


@pytest.mark.parametrize("customized", [False, True])
@pytest.mark.parametrize("limit", range(1, 10))
def test_ontology_ask_v2_upgrade_resumes_between_statements(limit, customized):
    with database() as db, database() as expected:
        for connection in (db, expected):
            ask_v1_state(connection)
            if customized:
                connection.execute("UPDATE t_agent SET f_model='custom-model' WHERE f_agent_id='bkn-project-ontology-ask'")
        apply(expected, ONTOLOGY_ASK_REVISIONS)
        apply(db, ONTOLOGY_ASK_REVISIONS, limit=limit)
        apply(db, ONTOLOGY_ASK_REVISIONS)
        assert snapshot(db) == snapshot(expected)


def test_ontology_ask_v2_upgrade_does_not_reverse_an_operator_rollback():
    with database() as db:
        ask_v1_state(db)
        apply(db, ONTOLOGY_ASK_REVISIONS)
        db.execute(
            "UPDATE t_agent_prompt SET f_current_version=1,f_update_user='operator',f_update_time=9e15"
            " WHERE f_prompt_id='bkn-project-ontology-ask-prompt'"
        )
        db.execute("UPDATE t_agent_version SET f_current=0 WHERE f_agent_id='bkn-project-ontology-ask'")
        db.execute("UPDATE t_agent_version SET f_current=1 WHERE f_agent_id='bkn-project-ontology-ask' AND f_prompt_version=1")
        before = snapshot(db)
        apply(db, ONTOLOGY_ASK_REVISIONS)
        assert snapshot(db) == before


def test_ontology_ask_v2_upgrade_keeps_custom_settings_and_official_baseline():
    with database() as db:
        ask_v1_state(db)
        original = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-ontology-ask' AND f_version=1").fetchone())
        db.execute(
            "UPDATE t_agent SET f_model='custom-model',f_limits=? WHERE f_agent_id='bkn-project-ontology-ask'",
            ('{"max_turns":3}',),
        )
        apply(db, ONTOLOGY_ASK_REVISIONS)
        baseline = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-ontology-ask' AND f_initial=1").fetchone())
        current = dict(db.execute(
            "SELECT * FROM t_agent_version WHERE f_agent_id='bkn-project-ontology-ask' AND f_current=1").fetchone())
        assert (baseline["f_prompt_version"], baseline["f_current"]) == (2, 0)
        for field in ("f_model", "f_tools", "f_limits"):
            assert baseline[field] == original[field]
        assert (current["f_prompt_version"], current["f_model"], current["f_limits"]) == (2, "custom-model", '{"max_turns":3}')


def test_ontology_ask_v2_upgrade_does_not_switch_a_prompt_head_an_operator_saved():
    with database() as db:
        ask_v1_state(db)
        db.execute(
            "UPDATE t_agent_prompt SET f_update_user='operator',f_update_time=500"
            " WHERE f_prompt_id='bkn-project-ontology-ask-prompt'"
        )
        head_before, agent_versions_before = snapshot(db)[1], snapshot(db)[3]
        apply(db, ONTOLOGY_ASK_REVISIONS)
        assert snapshot(db)[1] == head_before
        assert snapshot(db)[3] == agent_versions_before


@pytest.mark.parametrize("customized", [False, True])
def test_ontology_ask_v2_upgrade_makes_it_a_chat_agent_whatever_its_prompt(customized):
    with database() as db:
        ask_v1_state(db)
        if customized:
            db.execute(
                "UPDATE t_agent_prompt_version SET f_content='custom prompt'"
                " WHERE f_prompt_id='bkn-project-ontology-ask-prompt' AND f_version=1"
            )
        apply(db, ONTOLOGY_ASK_REVISIONS)
        mode = db.execute("SELECT f_mode FROM t_agent WHERE f_agent_id='bkn-project-ontology-ask'").fetchone()[0]
        assert mode == "chat"
