# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
"""Regression checks for the resource identifier length migration."""

from pathlib import Path


BACKEND_MIGRATION = (
    Path(__file__).resolve().parents[3]
    / "migrations/bkn-backend/mariadb/0.2.1/05-resource-id-name-limits.sql"
)
PROJECT_MIGRATION = (
    Path(__file__).resolve().parents[3]
    / "migrations/bkn-project/mariadb/0.2.1/03-resource-id-limits.sql"
)
PROJECT_SNAPSHOT = (
    Path(__file__).resolve().parents[3]
    / "migrations/bkn-project/mariadb/0.2.1/init.sql"
)
AGENT_MIGRATION = (
    Path(__file__).resolve().parents[3]
    / "migrations/bkn-agent/mariadb/0.2.1/12-resource-id-limits.sql"
)


def test_foreign_key_rebuild_is_retry_safe():
    migration = BACKEND_MIGRATION.read_text(encoding="utf-8")

    assert "DROP FOREIGN KEY IF EXISTS fk_object_metric_reference_target" in migration


def test_project_network_references_are_widened():
    migration = PROJECT_MIGRATION.read_text(encoding="utf-8")

    assert "MODIFY COLUMN f_kn_id VARCHAR(128)" in migration
    assert "MODIFY COLUMN f_baseline_network_id VARCHAR(128)" in migration


def test_project_snapshot_matches_the_widened_references():
    snapshot = PROJECT_SNAPSHOT.read_text(encoding="utf-8")

    for column in (
        "f_lineage_id",
        "f_network_id",
        "f_from_network_id",
        "f_to_network_id",
        "f_kn_id",
        "f_baseline_network_id",
    ):
        assert f"{column} VARCHAR(40)" not in snapshot
        assert f"{column} VARCHAR(128)" in snapshot


def test_agent_network_reference_is_widened():
    migration = AGENT_MIGRATION.read_text(encoding="utf-8")

    assert "MODIFY COLUMN f_kn_id VARCHAR(128)" in migration
