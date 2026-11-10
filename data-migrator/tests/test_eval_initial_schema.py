# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
"""Eval tables must be part of the backend fresh-install snapshot."""

import logging
import os
from pathlib import Path
import re

import pytest

from server.config.models import AppConfig, RDSConfig, ServiceConfig
from server.migrate.script_selector import ScriptSelector


ROOT = Path(__file__).resolve().parents[2]
SNAPSHOT = ROOT / "migrations/bkn-backend/mariadb/0.2.2/init.sql"
EVAL_TABLES = {
    "t_bkn_eval_suite",
    "t_bkn_eval_suite_version",
    "t_bkn_eval_case",
    "t_bkn_eval_case_version",
    "t_bkn_eval_run",
    "t_bkn_eval_run_credential",
    "t_bkn_eval_case_run",
    "t_bkn_eval_assertion_result",
    "t_bkn_eval_finding",
    "t_bkn_eval_job",
    "t_bkn_eval_case_draft",
    "t_bkn_eval_gold_access_audit",
    "t_bkn_eval_template_config",
    "t_bkn_eval_template_config_version",
    "t_bkn_eval_run_result_revision",
    "t_bkn_eval_target_scan",
    "t_bkn_eval_generation_plan",
    "t_bkn_eval_generation_job",
    "t_bkn_eval_case_candidate",
    "t_bkn_eval_template_grant",
    "t_bkn_eval_template_origin",
    "t_bkn_eval_case_state",
    "t_bkn_eval_run_plan",
    "t_bkn_eval_run_plan_authorization",
    "t_bkn_eval_overview_scope",
    "t_bkn_eval_root_cause_analysis",
}


def test_backend_fresh_install_selects_snapshot_with_all_eval_tables():
    config = AppConfig(
        rds=RDSConfig("", 3306, "", "", "mariadb", "internal"),
        services={"bkn-backend": ServiceConfig()},
        repo_path=str(ROOT / "migrations"),
    )
    path, version = ScriptSelector(config, logging.getLogger(__name__)).find_init_sql("bkn-backend")
    assert Path(path) == SNAPSHOT
    assert version == "0.2.2"
    tables = set(re.findall(r"CREATE TABLE(?: IF NOT EXISTS)? (t_bkn_eval_\w+)", SNAPSHOT.read_text()))
    assert tables == EVAL_TABLES


def test_eval_initialization_is_schema_only_without_private_initializer_marker():
    snapshot = SNAPSHOT.read_text()
    assert "USE openbkn;" in snapshot
    assert "t_bkn_eval_schema" not in snapshot
    assert not re.search(r"INSERT\s+INTO\s+t_bkn_eval_", snapshot, re.I)
    assert "t_bkn_eval_run_plan_authorization" in snapshot
    assert "description_snapshot VARCHAR(1000) NOT NULL" in snapshot


def test_eval_foreign_keys_reference_tables_created_earlier():
    snapshot = SNAPSHOT.read_text()
    created = set()
    for table, body in re.findall(
        r"CREATE TABLE(?: IF NOT EXISTS)? (t_bkn_eval_\w+)\s*\((.*?)\) ENGINE[^;]+;",
        snapshot, re.S,
    ):
        references = set(re.findall(r"REFERENCES (t_bkn_eval_\w+)", body))
        assert references <= created, f"{table} references tables not yet initialized"
        assert "PRIMARY KEY" in body
        created.add(table)
    assert created == EVAL_TABLES


def test_community_migrator_initializes_eval_in_fresh_mariadb():
    """Opt in only against a disposable, empty MariaDB service (as in CI)."""
    host = os.environ.get("EVAL_FRESH_INSTALL_DB_HOST")
    if not host:
        pytest.skip("EVAL_FRESH_INSTALL_DB_HOST is not configured")

    import rdsdriver
    from server.migrate.executor import MigrationExecutor

    rds = RDSConfig(
        host, int(os.environ.get("EVAL_FRESH_INSTALL_DB_PORT", "3306")),
        os.environ["EVAL_FRESH_INSTALL_DB_USER"], os.environ["EVAL_FRESH_INSTALL_DB_PASSWORD"],
        "mariadb", "internal",
    )
    connection = rdsdriver.connect(
        host=rds.host, port=rds.port, user=rds.user, password=rds.password,
        connect_timeout=5, autocommit=True, cursorclass=rdsdriver.DictCursor,
    )
    try:
        with connection.cursor() as cursor:
            cursor.execute("SELECT table_name FROM information_schema.tables WHERE table_schema = 'openbkn'")
            assert not cursor.fetchall(), "This test requires an empty disposable database"

        config = AppConfig(
            rds=rds, services={"bkn-backend": ServiceConfig()}, databases=["openbkn"],
            repo_path=str(ROOT / "migrations"),
        )
        MigrationExecutor(config, logging.getLogger(__name__)).run()

        with connection.cursor() as cursor:
            cursor.execute("SELECT table_name AS name FROM information_schema.tables "
                           "WHERE table_schema = 'openbkn' AND LEFT(table_name, 11) = 't_bkn_eval_'")
            assert {row["name"] for row in cursor.fetchall()} == EVAL_TABLES
            for table in sorted(EVAL_TABLES):
                cursor.execute(f"SELECT COUNT(*) AS rows_count FROM openbkn.`{table}`")
                assert cursor.fetchone()["rows_count"] == 0
            cursor.execute("SELECT f_installed_version AS version FROM deploy.t_schema_migration_task "
                           "WHERE f_service_name = 'bkn-backend'")
            assert cursor.fetchone()["version"] == "0.2.2"
    finally:
        connection.close()
