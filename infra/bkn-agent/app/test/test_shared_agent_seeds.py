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
    """)
    return db


def apply(db, path, limit=None):
    inserts = [
        s for s in statements(path) if s.lower().startswith("insert into t_agent")
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
        for table in ("t_agent", "t_agent_prompt", "t_agent_prompt_version")
    ]


def test_new_snapshot_keeps_the_original_ten_tables_without_schema_changes():
    old = (MIGRATIONS / "0.2.0/init.sql").read_text()
    new = (MIGRATIONS / "0.2.1/init.sql").read_text()
    assert new.startswith(old.rstrip() + "\n")
    tables = re.findall(r"create table if not exists (\w+)", new, re.I)
    assert len(tables) == 10
    assert tables == re.findall(r"create table if not exists (\w+)", old, re.I)
    assert "alter table" not in (STUDIO.read_text() + PROVENANCE.read_text()).lower()
    assert not (ROOT / "infra/bkn-agent/deploy").exists()


def test_fresh_install_and_upgrade_have_identical_data_and_preserve_semantic_versions():
    with database() as fresh, database() as upgraded:
        apply(fresh, MIGRATIONS / "0.2.1/init.sql")
        apply(upgraded, MIGRATIONS / "0.2.0/init.sql")
        original = snapshot(upgraded)
        apply(upgraded, STUDIO)
        apply(upgraded, PROVENANCE)
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
        "studio_smart_qa": "fc8a4cf7307224d1f73e250641e881310ffb532d41f7b3b352a72f318b6018e5",
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


def test_shared_migrator_selects_new_snapshot_and_both_seed_deltas():
    import subprocess
    import sys

    script = """
import json, logging, sys
from server.config.models import AppConfig, RDSConfig
from server.migrate.script_selector import ScriptSelector
config = AppConfig(rds=RDSConfig('unused',3306,'unused','','mariadb','internal'),repo_path=sys.argv[1])
selector = ScriptSelector(config, logging.getLogger('test'))
print(json.dumps([selector.find_init_sql('bkn-agent'),selector.select_upgrade_scripts('bkn-agent','0.2.0')]))
"""
    result = subprocess.run(
        [sys.executable, "-c", script, str(ROOT / "migrations")],
        cwd=ROOT / "data-migrator",
        capture_output=True,
        text=True,
        check=True,
    )
    fresh, upgrade = json.loads(result.stdout)
    assert fresh == [str(MIGRATIONS / "0.2.1/init.sql"), "0.2.1"]
    assert upgrade == [[["0.2.1", [str(STUDIO), str(PROVENANCE)]]], "0.2.1", True]
