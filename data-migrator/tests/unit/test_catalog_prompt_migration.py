# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
"""Exercise prompt upgrade DML in memory; MariaDB syntax is checked separately."""
import logging
import sqlite3
from pathlib import Path

import pytest

from server.utils.sql import parse_sql_file


MIGRATIONS = Path(__file__).resolve().parents[3] / "migrations/bkn-agent/mariadb"
PROMPT_ID = "catalog-semantic-understanding-prompt"


@pytest.fixture
def logger():
    return logging.getLogger(__name__)


@pytest.fixture
def db():
    connection = sqlite3.connect(":memory:")
    connection.create_function("now", 1, lambda precision: 1)
    connection.create_function("unix_timestamp", 1, lambda value: value)
    connection.executescript("""
        CREATE TABLE dual (dummy INTEGER);
        INSERT INTO dual VALUES (1);
        CREATE TABLE t_agent_prompt (
            f_prompt_id TEXT PRIMARY KEY, f_name TEXT, f_current_version INTEGER,
            f_update_user TEXT, f_update_time INTEGER
        );
        CREATE TABLE t_agent_prompt_version (
            f_prompt_id TEXT, f_version INTEGER, f_content TEXT, f_vars_schema TEXT,
            f_create_user TEXT, f_create_time INTEGER,
            PRIMARY KEY (f_prompt_id, f_version)
        );
    """)
    yield connection
    connection.close()


def apply_catalog_seed(db, path, logger):
    for sql in parse_sql_file(str(path), logger):
        if sql.lower().startswith("insert into t_agent_prompt") and PROMPT_ID in sql:
            db.execute(sql)


def apply_upgrade(db, logger):
    for sql in parse_sql_file(str(MIGRATIONS / "0.2.0/01-logical-view-prompt.sql"), logger):
        if not sql.upper().startswith("USE "):
            db.execute(sql)


def current_version(db):
    return db.execute("SELECT f_current_version FROM t_agent_prompt WHERE f_prompt_id = ?", (PROMPT_ID,)).fetchone()[0]


def test_upgrade_preserves_v1_and_is_idempotent(db, logger):
    apply_catalog_seed(db, MIGRATIONS / "0.1.5/init.sql", logger)
    before = db.execute("SELECT * FROM t_agent_prompt_version").fetchall()
    apply_upgrade(db, logger)
    apply_upgrade(db, logger)
    assert current_version(db) == 2
    assert db.execute("SELECT * FROM t_agent_prompt_version WHERE f_version = 1").fetchall() == before
    assert db.execute("SELECT COUNT(*) FROM t_agent_prompt_version").fetchone()[0] == 2
    content = db.execute("SELECT f_content FROM t_agent_prompt_version WHERE f_version = 2").fetchone()[0]
    assert "obsolete_logical_views" in content
    assert "obsolete_logic_views" not in content


def test_upgrade_preserves_operator_selected_version(db, logger):
    apply_catalog_seed(db, MIGRATIONS / "0.1.5/init.sql", logger)
    db.execute("INSERT INTO t_agent_prompt_version VALUES (?, 7, 'custom', NULL, 'operator', 0)", (PROMPT_ID,))
    db.execute("UPDATE t_agent_prompt SET f_current_version = 7")
    apply_upgrade(db, logger)
    assert current_version(db) == 7


def test_upgrade_does_not_activate_colliding_custom_v2(db, logger):
    apply_catalog_seed(db, MIGRATIONS / "0.1.5/init.sql", logger)
    db.execute("INSERT INTO t_agent_prompt_version VALUES (?, 2, 'custom', NULL, 'operator', 0)", (PROMPT_ID,))
    apply_upgrade(db, logger)
    assert current_version(db) == 1
    assert db.execute("SELECT f_content FROM t_agent_prompt_version WHERE f_version = 2").fetchone()[0] == "custom"


def test_upgrade_does_not_activate_missing_prompt_version(db, logger):
    apply_catalog_seed(db, MIGRATIONS / "0.1.5/init.sql", logger)
    db.execute("DELETE FROM t_agent_prompt_version")
    apply_upgrade(db, logger)
    assert current_version(db) == 1
    assert db.execute("SELECT COUNT(*) FROM t_agent_prompt_version").fetchone()[0] == 0


def test_fresh_install_matches_upgraded_active_prompt(db, logger):
    apply_catalog_seed(db, MIGRATIONS / "0.1.5/init.sql", logger)
    apply_upgrade(db, logger)
    upgraded = db.execute("SELECT f_content, f_vars_schema FROM t_agent_prompt_version WHERE f_version = 2").fetchone()
    db.executescript("DELETE FROM t_agent_prompt; DELETE FROM t_agent_prompt_version;")
    apply_catalog_seed(db, MIGRATIONS / "0.2.0/init.sql", logger)
    assert current_version(db) == 2
    assert db.execute("SELECT f_content, f_vars_schema FROM t_agent_prompt_version WHERE f_version = 2").fetchone() == upgraded
