# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import gzip
import hashlib
import io
import json
import os
import tempfile
import unittest
from contextlib import ExitStack, redirect_stderr
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock
from unittest.mock import patch

import bkn_data as migration

from bkn_data import (
    BackupResult,
    DBConfig,
    GrantIndex,
    MigrationPlan,
    ProxyMigrationPlan,
    ProxyNetworkPlan,
    ProxySource,
    ResourceRow,
    ResourceParent,
    apply_parent_plan,
    build_plan,
    derive_proxy_sources,
    is_inert_archived_proxy,
    load_proxy_authorization_references,
    load_proxy_plan,
    migration_report,
    stable_proxy_account_id,
    sync_proxy_sources,
)


def resource(
    resource_type,
    resource_id,
    kn_id="kn-1",
    branch="main",
):
    table_by_type = {
        "knowledge_network": "t_knowledge_network",
        "concept_group": "t_concept_group",
        "object_type": "t_object_type",
        "relation_type": "t_relation_type",
        "action_type": "t_action_type",
        "metric": "t_metric_definition",
        "risk_type": "t_risk_type",
    }
    return ResourceRow(
        resource_type=resource_type,
        table=table_by_type[resource_type],
        resource_id=resource_id,
        kn_id="" if resource_type == "knowledge_network" else kn_id,
        branch=branch,
    )


class BuildPlanTest(unittest.TestCase):
    def test_builds_six_parent_rows_without_rewriting_caller_policies(self):
        rows = [
            resource("knowledge_network", "kn-1"),
            resource("concept_group", "group-1"),
            resource("object_type", "object-1"),
            resource("relation_type", "relation-1"),
            resource("action_type", "action-1"),
            resource("metric", "metric-1"),
            resource("risk_type", "risk-1"),
        ]
        plan = build_plan(rows, branch_updates=0)

        self.assertEqual([], plan.failures)
        self.assertEqual(6, len(plan.parents))

    def test_reports_branch_collision_after_blank_normalization(self):
        rows = [
            resource("knowledge_network", "kn-1", branch=""),
            resource("knowledge_network", "kn-1", branch="main"),
        ]

        plan = build_plan(rows, branch_updates=1)

        self.assertEqual(["branch_conflict"], [item.code for item in plan.failures])

    def test_reports_invalid_child_id_and_missing_parent(self):
        rows = [
            resource("object_type", "bad/id"),
            resource("metric", "metric-1", kn_id="missing-kn"),
        ]

        plan = build_plan(rows, branch_updates=0)

        self.assertEqual(
            ["invalid_resource_id", "missing_parent"],
            [item.code for item in plan.failures],
        )

class ApplyParentPlanTest(unittest.TestCase):
    def test_rolls_back_when_parent_rebuild_fails_after_cleanup(self):
        connection = MagicMock()
        cursor = connection.cursor.return_value.__enter__.return_value
        cursor.execute.return_value = 6
        cursor.executemany.side_effect = RuntimeError("write failed")
        plan = MigrationPlan(
            resources={},
            branch_updates=0,
            parents=[
                ResourceParent(
                    "object_type",
                    "kn-1/object-1",
                    "knowledge_network",
                    "kn-1",
                )
            ],
        )

        with self.assertRaisesRegex(RuntimeError, "write failed"):
            apply_parent_plan(connection, plan)

        connection.rollback.assert_called_once_with()
        connection.commit.assert_not_called()


class ProxyPlanTest(unittest.TestCase):
    def test_revoked_proxy_sources_do_not_keep_an_archived_proxy_active(self):
        cursor = MagicMock()
        cursor.fetchall.side_effect = [[], [], [], []]

        references = load_proxy_authorization_references(cursor, ["proxy-1"])

        self.assertEqual(set(), references)
        source_query = cursor.execute.call_args_list[1]
        self.assertIn("lifecycle_status = %s", source_query.args[0])
        self.assertEqual(("proxy-1", "active"), source_query.args[1])

    def test_only_inert_archived_proxy_is_a_valid_deleted_network_tombstone(self):
        mapping = {
            "proxy_account_id": "proxy-1",
            "lifecycle_status": "archived",
        }
        user = {
            "id": "proxy-1",
            "enabled": 0,
            "account_type": "app",
            "password_hash": "",
        }

        self.assertTrue(is_inert_archived_proxy(mapping, user, set()))
        self.assertFalse(is_inert_archived_proxy(mapping, user, {"proxy-1"}))
        self.assertFalse(
            is_inert_archived_proxy(mapping, {**user, "enabled": 1}, set())
        )
        self.assertFalse(
            is_inert_archived_proxy(
                {**mapping, "lifecycle_status": "active"}, user, set()
            )
        )

    def test_migration_report_lists_ignored_archived_tombstones(self):
        proxy_plan = ProxyMigrationPlan(
            archived_tombstones=[("kn-deleted", "proxy-archived")]
        )

        report = migration_report(
            "dry-run", MigrationPlan({}, 0), proxy_plan
        )

        self.assertEqual(
            [
                {
                    "knowledge_network_id": "kn-deleted",
                    "proxy_account_id": "proxy-archived",
                }
            ],
            report["managed_proxies"]["archived_tombstones"],
        )

    def test_new_proxy_identity_is_stable_between_dry_run_and_apply(self):
        self.assertEqual(
            stable_proxy_account_id("kn-1"),
            stable_proxy_account_id("kn-1"),
        )
        self.assertNotEqual(
            stable_proxy_account_id("kn-1"),
            stable_proxy_account_id("kn-2"),
        )

    @patch.object(migration, "table_exists", return_value=False)
    @patch.object(migration, "require_safe_proxy_schema")
    def test_proxy_plan_requires_the_installed_0_1_5_schema(
        self, require_safe_proxy_schema, table_exists
    ):
        with self.assertRaisesRegex(
            migration.MigrationError,
            "BKN 0.1.5 schema is unavailable",
        ):
            load_proxy_plan(MagicMock(), MagicMock(), "grantor-1")

        require_safe_proxy_schema.assert_called_once()
        table_exists.assert_called_once()

    def test_derives_resource_toolbox_and_mcp_sources(self):
        sources, model_version = derive_proxy_sources(
            "kn-1",
            [
                {
                    "f_id": "object-1",
                    "f_data_source": '{"type":"resource","id":"resource-1"}',
                    "f_logic_properties": (
                        '[{"name":"risk","data_source":{"type":"tool",'
                        '"box_id":"box-1","tool_id":"tool-1"}}]'
                    ),
                }
            ],
            [],
            [],
            [
                {
                    "f_id": "action-1",
                    "f_action_source": (
                        '{"type":"mcp","mcp_id":"mcp-1","tool_name":"run"}'
                    ),
                }
            ],
        )

        self.assertTrue(model_version.startswith("sha256:"))
        self.assertEqual(64, len(model_version.removeprefix("sha256:")))
        self.assertEqual(
            {
                ("resource", "resource-1", "view_detail"),
                ("resource", "resource-1", "query_data"),
                ("tool_box", "box-1", "execute"),
                ("mcp", "mcp-1", "execute"),
            },
            {
                (source.resource_type, source.resource_id, source.operation)
                for source in sources
            },
        )

    def test_grant_index_resolves_roles_wildcards_and_parent_operations(self):
        rules = [
            {"ptype": "g", "v0": "grantor-1", "v1": "role-1", "v2": ""},
            {
                "ptype": "p",
                "v0": "role-1",
                "v1": "catalog:catalog-1",
                "v2": "resource_manage",
            },
            {
                "ptype": "p",
                "v0": "role-1",
                "v1": "tool_box:*",
                "v2": "execute",
            },
        ]
        index = GrantIndex(
            "grantor-1",
            rules,
            {("resource", "resource-1"): ("catalog", "catalog-1")},
            {("resource", "query_data"): "resource_manage"},
            {
                ("resource", "query_data"),
                ("catalog", "resource_manage"),
                ("tool_box", "execute"),
            },
        )

        self.assertTrue(index.allows("resource", "resource-1", "query_data"))
        self.assertTrue(index.allows("tool_box", "box-1", "execute"))
        self.assertFalse(index.allows("mcp", "mcp-1", "execute"))

    def test_grant_index_rejects_an_unregistered_operation(self):
        index = GrantIndex(
            "grantor-1",
            [
                {
                    "ptype": "p",
                    "v0": "grantor-1",
                    "v1": "resource:resource-1",
                    "v2": "query_data",
                }
            ],
            {},
            {},
            set(),
        )

        self.assertFalse(index.allows("resource", "resource-1", "query_data"))

    def test_sync_materializes_a_new_source_and_owned_policy(self):
        cursor = MagicMock()
        cursor.fetchall.return_value = []
        cursor.fetchone.side_effect = [{"count": 1}, None, {"count": 0}, None]
        source = ProxySource(
            resource_type="resource",
            resource_id="resource-1",
            operation="query_data",
            source_id="source-1",
            kn_id="kn-1",
            binding_type="object_type",
            binding_id="object-1",
        )
        network = ProxyNetworkPlan(
            kn_id="kn-1",
            kn_name="Network 1",
            proxy_account_id="proxy-1",
            model_version="sha256:model",
            sources=[source],
            create_account=True,
        )

        result = sync_proxy_sources(
            cursor,
            network,
            "grantor-1",
            datetime(2026, 9, 7, 0, 0, 0),
        )

        self.assertEqual(1, result["sources_added"])
        self.assertEqual(1, result["policies_created"])
        statements = [call.args[0] for call in cursor.execute.call_args_list]
        self.assertTrue(
            any("INSERT INTO proxy_grant_source" in statement for statement in statements)
        )
        self.assertTrue(
            any("INSERT INTO proxy_grant_policy" in statement for statement in statements)
        )
        self.assertTrue(
            any("INSERT INTO casbin_rule" in statement for statement in statements)
        )
        self.assertTrue(
            any("INSERT INTO authorization_grant" in statement for statement in statements)
        )
        casbin_insert = next(
            call
            for call in cursor.execute.call_args_list
            if "INSERT INTO casbin_rule" in call.args[0]
        )
        self.assertEqual(
            ("allow", "system_derived", "system"),
            casbin_insert.args[1][-3:],
        )

    def test_sync_upgrades_a_pre_provenance_policy_owned_by_the_proxy_marker(self):
        cursor = MagicMock()
        cursor.fetchall.return_value = [
            {
                "id": "source-row-1",
                "resource_type": "resource",
                "resource_id": "resource-1",
                "operation": "query_data",
                "source_id": "source-1",
                "kn_id": "kn-1",
                "binding_type": "object_type",
                "binding_id": "object-1",
                "lifecycle_status": "active",
            }
        ]
        cursor.fetchone.side_effect = [
            {"count": 1},
            {"policy_owned": 1},
            {"count": 0},
            None,
        ]

        def execute(statement, parameters=()):
            del parameters
            if statement.startswith("UPDATE casbin_rule"):
                return 1
            return 0

        cursor.execute.side_effect = execute
        source = ProxySource(
            resource_type="resource",
            resource_id="resource-1",
            operation="query_data",
            source_id="source-1",
            kn_id="kn-1",
            binding_type="object_type",
            binding_id="object-1",
        )
        network = ProxyNetworkPlan(
            kn_id="kn-1",
            kn_name="Network 1",
            proxy_account_id="proxy-1",
            model_version="sha256:model",
            sources=[source],
            create_account=False,
        )

        result = sync_proxy_sources(
            cursor,
            network,
            "grantor-1",
            datetime(2026, 9, 7, 0, 0, 0),
        )

        self.assertEqual(1, result["policies_upgraded"])
        self.assertEqual(0, result["policies_created"])
        statements = [call.args[0] for call in cursor.execute.call_args_list]
        self.assertTrue(any(statement.startswith("UPDATE casbin_rule") for statement in statements))
        self.assertFalse(any("INSERT INTO casbin_rule" in statement for statement in statements))
        self.assertTrue(
            any("INSERT INTO authorization_grant" in statement for statement in statements)
        )

    def test_sync_revokes_only_the_owned_system_derived_projection(self):
        cursor = MagicMock()
        cursor.fetchall.return_value = [
            {
                "id": "source-row-1",
                "resource_type": "resource",
                "resource_id": "resource-1",
                "operation": "query_data",
                "source_id": "source-1",
                "kn_id": "kn-1",
                "binding_type": "object_type",
                "binding_id": "object-1",
                "lifecycle_status": "active",
            }
        ]
        cursor.fetchone.side_effect = [
            {"count": 0},
            {"policy_owned": 1},
            {"count": 1},
        ]

        def execute(statement, parameters=()):
            del parameters
            if statement.startswith("DELETE FROM casbin_rule"):
                return 1
            return 0

        cursor.execute.side_effect = execute
        network = ProxyNetworkPlan(
            kn_id="kn-1",
            kn_name="Network 1",
            proxy_account_id="proxy-1",
            model_version="sha256:model",
            sources=[],
            create_account=False,
        )

        result = sync_proxy_sources(
            cursor,
            network,
            "grantor-1",
            datetime(2026, 9, 7, 0, 0, 0),
        )

        self.assertEqual(1, result["sources_revoked"])
        self.assertEqual(1, result["policies_removed"])
        deletion = next(
            call
            for call in cursor.execute.call_args_list
            if call.args[0].startswith("DELETE FROM casbin_rule")
        )
        self.assertIn("v3 = %s AND v4 = %s AND v5 = %s", deletion.args[0])
        self.assertEqual(
            ("allow", "system_derived", "system"),
            deletion.args[1][-3:],
        )


class DatabaseConfigurationTest(unittest.TestCase):
    def test_explicit_settings_take_precedence_over_server_environment(self):
        environment = {
            "BKN_DB_HOST": "explicit-host",
            "BKN_DB_PORT": "4406",
            "BKN_DB_USER": "explicit-user",
            "BKN_DB_PASSWORD": "explicit-secret",
            "BKN_DB_NAME": "explicit-bkn",
            "MARIADB_HOST": "server-host",
            "MARIADB_PORT_NUMBER": "3307",
            "MARIADB_USER": "server-user",
            "MARIADB_PASSWORD": "server-secret",
            "MARIADB_DATABASE": "server-bkn",
        }

        with patch.dict(os.environ, environment, clear=True):
            bkn_config, safe_config = migration.database_configs()

        self.assertEqual(
            DBConfig(
                "explicit-host",
                4406,
                "explicit-user",
                "explicit-secret",
                "explicit-bkn",
            ),
            bkn_config,
        )
        self.assertEqual(
            DBConfig(
                "explicit-host", 4406, "explicit-user", "explicit-secret", "safe"
            ),
            safe_config,
        )

    def test_reads_server_password_file_and_reuses_connection_for_safe(self):
        with tempfile.TemporaryDirectory() as directory:
            password_file = Path(directory) / "mariadb-password"
            password_file.write_text("file-secret\n", encoding="utf-8")
            environment = {
                "MARIADB_HOST": "127.0.0.9",
                "MARIADB_PORT_NUMBER": "3308",
                "MARIADB_USER": "server-user",
                "MARIADB_PASSWORD_FILE": str(password_file),
                "MARIADB_DATABASE": "server-bkn",
            }

            with patch.dict(os.environ, environment, clear=True):
                bkn_config, safe_config = migration.database_configs()

        self.assertEqual("file-secret", bkn_config.password)
        self.assertEqual("server-bkn", bkn_config.database)
        self.assertEqual("safe", safe_config.database)
        self.assertEqual(bkn_config.host, safe_config.host)
        self.assertEqual(bkn_config.port, safe_config.port)
        self.assertEqual(bkn_config.user, safe_config.user)
        self.assertEqual(bkn_config.password, safe_config.password)

    def test_prefers_server_root_credentials_when_both_accounts_are_available(self):
        environment = {
            "MARIADB_USER": "application-user",
            "MARIADB_PASSWORD": "application-secret",
            "MARIADB_ROOT_PASSWORD": "root-secret",
        }

        with patch.dict(os.environ, environment, clear=True):
            bkn_config, safe_config = migration.database_configs()

        self.assertEqual("root", bkn_config.user)
        self.assertEqual("root-secret", bkn_config.password)
        self.assertEqual("root", safe_config.user)
        self.assertEqual("root-secret", safe_config.password)

    def test_reports_an_unreadable_password_file_without_exposing_a_secret(self):
        environment = {"BKN_DB_PASSWORD_FILE": "/missing/password-file"}

        with patch.dict(os.environ, environment, clear=True):
            with self.assertRaisesRegex(
                migration.MigrationError, "BKN_DB_PASSWORD_FILE"
            ):
                migration.database_configs()


class PreMigrationBackupTest(unittest.TestCase):
    def setUp(self):
        self.configs = {
            "bkn": DBConfig("127.0.0.1", 3306, "root", "bkn-secret", "openbkn"),
            "safe": DBConfig("127.0.0.1", 3306, "root", "safe-secret", "safe"),
        }

    def test_streams_a_real_dump_process_into_a_valid_gzip_archive(self):
        content = b"-- dump from a real child process\n"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            executable = root / "fake-mariadb-dump"
            executable.write_text(
                "#!/bin/sh\nprintf '%s\\n' '-- dump from a real child process'\n",
                encoding="utf-8",
            )
            executable.chmod(0o700)

            backup = migration.create_pre_migration_backup(
                {"bkn": self.configs["bkn"]},
                backup_root=root / "backups",
                dump_executable=str(executable),
            )

            with gzip.open(backup.path / "bkn.sql.gz", "rb") as source:
                self.assertEqual(content, source.read())

    def test_creates_verified_secret_free_backups_without_overwriting(self):
        calls = []

        def dump(command, stdout, stderr, env):
            calls.append((command, env))
            self.assertIs(migration.subprocess.PIPE, stdout)
            return SimpleNamespace(
                stdout=io.BytesIO(b"-- complete logical dump\n"),
                wait=lambda: 0,
            )

        instant = datetime(2026, 9, 8, 12, 30, tzinfo=timezone.utc)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(migration.subprocess, "Popen", side_effect=dump):
                first = migration.create_pre_migration_backup(
                    self.configs,
                    backup_root=root,
                    now=instant,
                    dump_executable="/usr/bin/mariadb-dump",
                )
                second = migration.create_pre_migration_backup(
                    self.configs,
                    backup_root=root,
                    now=instant,
                    dump_executable="/usr/bin/mariadb-dump",
                )

            self.assertEqual("20260908_123000", first.path.name)
            self.assertEqual("20260908_123000-01", second.path.name)
            self.assertEqual(0o700, first.path.stat().st_mode & 0o777)
            manifest_path = first.path / "manifest.json"
            self.assertEqual(0o600, manifest_path.stat().st_mode & 0o777)
            manifest = json.loads(manifest_path.read_text())
            serialized_manifest = json.dumps(manifest)
            self.assertNotIn("bkn-secret", serialized_manifest)
            self.assertNotIn("safe-secret", serialized_manifest)
            self.assertEqual(2, len(manifest["databases"]))
            for entry in manifest["databases"]:
                archive = first.path / entry["archive"]
                self.assertEqual(0o600, archive.stat().st_mode & 0o777)
                self.assertEqual(entry["sha256"], migration.sha256_file(archive))
                with gzip.open(archive, "rb") as source:
                    self.assertEqual(b"-- complete logical dump\n", source.read())
            for command in first.restore_commands:
                self.assertIn("gzip -dc", command)
                self.assertIn("| mariadb", command)
                self.assertNotIn("bkn-secret", command)
                self.assertNotIn("safe-secret", command)

        self.assertEqual(4, len(calls))
        for command, environment in calls:
            command_text = " ".join(command)
            self.assertNotIn("bkn-secret", command_text)
            self.assertNotIn("safe-secret", command_text)
            self.assertIn(environment["MYSQL_PWD"], {"bkn-secret", "safe-secret"})

    def test_removes_an_incomplete_backup_and_redacts_dump_errors(self):
        def failed_dump(command, stdout, stderr, env):
            del command, stdout, env
            stderr.write(b"authentication rejected for bkn-secret")
            return SimpleNamespace(
                stdout=io.BytesIO(),
                wait=lambda: 1,
            )

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(migration.subprocess, "Popen", side_effect=failed_dump):
                with self.assertRaises(migration.MigrationError) as context:
                    migration.create_pre_migration_backup(
                        self.configs,
                        backup_root=root,
                        dump_executable="/usr/bin/mariadb-dump",
                    )

            self.assertNotIn("bkn-secret", str(context.exception))
            self.assertIn("<redacted>", str(context.exception))
            self.assertEqual([], list(root.iterdir()))

    def test_validates_archive_against_the_independent_source_checksum(self):
        content = b"-- complete logical dump\n"
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / "backup.sql.gz"
            with gzip.open(archive, "wb") as output:
                output.write(content)

            migration.validate_backup_archive(
                archive,
                "bkn",
                "openbkn",
                hashlib.sha256(content).hexdigest(),
            )
            with self.assertRaisesRegex(
                migration.MigrationError, "checksum verification failed"
            ):
                migration.validate_backup_archive(
                    archive,
                    "bkn",
                    "openbkn",
                    hashlib.sha256(b"different source").hexdigest(),
                )


class OneShotMigrationTest(unittest.TestCase):
    def test_dry_run_validates_and_reports_without_backup_or_writes(self):
        bkn_connection = MagicMock()
        safe_connection = MagicMock()
        plan = MigrationPlan({}, 2, existing_parents=3)
        proxy_plan = ProxyMigrationPlan()
        with ExitStack() as stack, tempfile.TemporaryDirectory() as directory:
            stack.enter_context(
                patch.object(
                    migration,
                    "database_configs",
                    return_value=(MagicMock(), MagicMock()),
                )
            )
            stack.enter_context(
                patch.object(
                    migration,
                    "connect_database",
                    side_effect=[bkn_connection, safe_connection],
                )
            )
            stack.enter_context(patch.object(migration, "load_resources", return_value=[]))
            stack.enter_context(patch.object(migration, "count_branch_updates", return_value=2))
            stack.enter_context(
                patch.object(migration, "load_existing_parent_count", return_value=3)
            )
            stack.enter_context(patch.object(migration, "build_plan", return_value=plan))
            stack.enter_context(
                patch.object(migration, "load_proxy_plan", return_value=proxy_plan)
            )
            backup = stack.enter_context(
                patch.object(migration, "create_pre_migration_backup")
            )
            normalize = stack.enter_context(patch.object(migration, "normalize_branches"))
            apply_parents = stack.enter_context(
                patch.object(migration, "apply_parent_plan")
            )
            apply_proxies = stack.enter_context(
                patch.object(migration, "apply_proxy_plan")
            )
            report = Path(directory) / "bkn.json"

            self.assertEqual(0, migration.run("dry-run", str(report)))
            self.assertEqual("dry-run", json.loads(report.read_text())["mode"])

        backup.assert_not_called()
        normalize.assert_not_called()
        apply_parents.assert_not_called()
        apply_proxies.assert_not_called()

    @patch.object(migration, "create_pre_migration_backup")
    @patch.object(migration, "verify_proxy_plan")
    @patch.object(migration, "apply_proxy_plan", return_value={"mappings_ready": 0})
    @patch.object(migration, "apply_parent_plan", return_value=0)
    @patch.object(migration, "normalize_branches", return_value=0)
    @patch.object(migration, "load_proxy_plan", return_value=ProxyMigrationPlan())
    @patch.object(migration, "build_plan", return_value=MigrationPlan({}, 0))
    @patch.object(migration, "load_existing_parent_count", return_value=0)
    @patch.object(migration, "count_branch_updates", return_value=0)
    @patch.object(migration, "load_resources", return_value=[])
    @patch.object(migration, "connect_database")
    def test_apply_runs_complete_offline_flow_once(
        self,
        connect_database,
        load_resources,
        count_branch_updates,
        load_existing_parent_count,
        build_plan,
        load_proxy_plan,
        normalize_branches,
        apply_parent_plan_mock,
        apply_proxy_plan_mock,
        verify_proxy_plan,
        create_pre_migration_backup,
    ):
        bkn_connection = MagicMock()
        safe_connection = MagicMock()
        connect_database.side_effect = [bkn_connection, safe_connection]
        events = []
        create_pre_migration_backup.side_effect = lambda configs: (
            events.append("backup")
            or BackupResult(Path("/backup/20260908_120000"), ())
        )
        normalize_branches.side_effect = lambda *args, **kwargs: (
            events.append("first-write") or 0
        )

        self.assertEqual(0, migration.run("apply"))

        self.assertEqual(["backup", "first-write"], events)
        create_pre_migration_backup.assert_called_once()
        apply_parent_plan_mock.assert_called_once()
        apply_proxy_plan_mock.assert_called_once()
        self.assertEqual(
            migration.MIGRATION_GRANTOR_ID,
            apply_proxy_plan_mock.call_args.args[3],
        )
        verify_proxy_plan.assert_called_once()
        safe_connection.commit.assert_called_once_with()
        bkn_connection.commit.assert_called_once_with()

    def test_backup_failure_prevents_all_migration_writes(self):
        bkn_connection = MagicMock()
        safe_connection = MagicMock()
        configs = (
            DBConfig("127.0.0.1", 3306, "root", "", "openbkn"),
            DBConfig("127.0.0.1", 3306, "root", "", "safe"),
        )
        with ExitStack() as stack:
            stack.enter_context(
                patch.object(migration, "database_configs", return_value=configs)
            )
            stack.enter_context(
                patch.object(
                    migration,
                    "connect_database",
                    side_effect=[bkn_connection, safe_connection],
                )
            )
            stack.enter_context(
                patch.object(migration, "load_resources", return_value=[])
            )
            stack.enter_context(
                patch.object(migration, "count_branch_updates", return_value=0)
            )
            stack.enter_context(
                patch.object(
                    migration, "load_existing_parent_count", return_value=0
                )
            )
            stack.enter_context(
                patch.object(
                    migration, "build_plan", return_value=MigrationPlan({}, 0)
                )
            )
            stack.enter_context(
                patch.object(
                    migration,
                    "load_proxy_plan",
                    return_value=ProxyMigrationPlan(),
                )
            )
            stack.enter_context(
                patch.object(
                    migration,
                    "create_pre_migration_backup",
                    side_effect=migration.MigrationError("backup failed"),
                )
            )
            normalize_branches = stack.enter_context(
                patch.object(migration, "normalize_branches")
            )
            apply_parent_plan_mock = stack.enter_context(
                patch.object(migration, "apply_parent_plan")
            )
            apply_proxy_plan_mock = stack.enter_context(
                patch.object(migration, "apply_proxy_plan")
            )
            with self.assertRaisesRegex(migration.MigrationError, "backup failed"):
                migration.run("apply")

        normalize_branches.assert_not_called()
        apply_parent_plan_mock.assert_not_called()
        apply_proxy_plan_mock.assert_not_called()
        bkn_connection.close.assert_called_once_with()
        safe_connection.close.assert_called_once_with()

    @patch.object(migration, "run", side_effect=RuntimeError("database write failed"))
    def test_command_prints_the_complete_traceback_on_failure(self, run):
        error_output = io.StringIO()

        with redirect_stderr(error_output):
            self.assertEqual(1, migration.main(["--mode", "apply"]))

        self.assertIn("Traceback (most recent call last):", error_output.getvalue())
        self.assertIn("RuntimeError: database write failed", error_output.getvalue())


if __name__ == "__main__":
    unittest.main()
