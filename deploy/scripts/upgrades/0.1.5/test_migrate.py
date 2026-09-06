# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


SCRIPT = Path(__file__).with_name("migrate.py")
SPEC = importlib.util.spec_from_file_location("openbkn_upgrade_015", SCRIPT)
assert SPEC and SPEC.loader
migrate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(migrate)


def online_args(state: Path) -> SimpleNamespace:
    return SimpleNamespace(
        state=state,
        backend_url="http://bkn",
        safe_url="http://safe",
        user_id="operator-1",
        runtime_ready=True,
        proxy_mode="off",
        pep_ready=["vega", "execution-factory"],
    )


class MigrationStateTest(unittest.TestCase):
    def test_run_skips_completed_phase_without_reapplying(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            state_path = Path(directory) / "state.json"
            state = migrate.new_state()
            state["stages"]["schema"] = {"status": "completed"}
            migrate.save_state(state_path, state)
            args = SimpleNamespace(phase="schema", state=state_path, force=False)
            with mock.patch.object(migrate, "schema_phase") as schema_phase, mock.patch(
                "builtins.print"
            ):
                self.assertEqual(migrate.run(args), 0)
                schema_phase.assert_not_called()

    def test_proxy_schema_exists_for_fresh_install_and_upgrade(self) -> None:
        incremental = migrate.SCHEMA_SCRIPT.read_text(encoding="utf-8")
        fresh = (
            migrate.ROOT / "migrations/bkn-backend/mariadb/0.1.5/init.sql"
        ).read_text(encoding="utf-8")
        for column in migrate.EXPECTED_SCHEMA_COLUMNS:
            self.assertIn(column, incremental)
            self.assertIn(column, fresh)
        self.assertNotIn("DROP TABLE IF EXISTS t_kn_proxy_account", incremental)

    def test_state_is_atomic_and_rejects_another_migration(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "state.json"
            state = migrate.new_state()
            migrate.save_state(path, state)
            self.assertEqual(migrate.load_state(path)["migration"], migrate.MIGRATION_ID)
            path.write_text('{"migration":"other"}', encoding="utf-8")
            with self.assertRaisesRegex(migrate.MigrationError, "not for"):
                migrate.load_state(path)

    def test_offline_preconditions_require_backup_and_export(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            backup = Path(directory) / "backup.sql"
            export = Path(directory) / "caller.json"
            backup.write_text("backup", encoding="utf-8")
            export.write_text("export", encoding="utf-8")
            args = SimpleNamespace(
                workloads_stopped=True,
                database_backup=backup,
                caller_export=export,
            )
            migrate.require_offline(args, caller_phase=True)
            args.workloads_stopped = False
            with self.assertRaisesRegex(migrate.MigrationError, "workloads-stopped"):
                migrate.require_offline(args, caller_phase=True)

    def test_online_preconditions_keep_proxy_off(self) -> None:
        args = SimpleNamespace(runtime_ready=True, proxy_mode="allowlist")
        with self.assertRaisesRegex(migrate.MigrationError, "must remain off"):
            migrate.require_online(args)

    def test_schema_apply_commits_only_after_post_apply_check(self) -> None:
        connection = mock.MagicMock()
        before = {"ready": False}
        after = {"ready": True}
        args = SimpleNamespace(apply=True)
        state = migrate.new_state()
        with mock.patch.object(migrate, "connect_bkn_database", return_value=connection), mock.patch.object(
            migrate, "inspect_schema", side_effect=[before, after]
        ), mock.patch.object(migrate, "require_offline"):
            migrate.schema_phase(args, state)
        connection.commit.assert_called_once_with()
        connection.rollback.assert_not_called()
        self.assertEqual(state["stages"]["schema"]["status"], "completed")

    def test_caller_apply_records_policy_and_parent_report(self) -> None:
        args = SimpleNamespace(
            apply=True,
            database_backup=Path("backup.sql"),
            caller_export=Path("caller.json"),
        )
        state = migrate.new_state()
        state["stages"]["schema"] = {"status": "completed"}
        report = {
            "policies": {"delete": 2, "create": 3},
            "resource_parents": {"delete": 1, "create": 4},
        }
        with mock.patch.object(migrate, "require_offline"), mock.patch.object(
            migrate, "run_caller_migration", return_value=report
        ):
            migrate.caller_phase(args, state)
        self.assertEqual(state["caller_authorization"], report)
        self.assertEqual(state["stages"]["caller_authorization"]["status"], "completed")


class ProxyBackfillTest(unittest.TestCase):
    def test_plan_rejects_existing_proxy_without_ownership_checkpoint(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            state = migrate.new_state()
            with mock.patch.object(migrate, "run_caller_migration", return_value={}), mock.patch.object(
                migrate, "list_networks", return_value=["kn-1"]
            ), mock.patch.object(
                migrate,
                "proxy_plan",
                return_value={"proxy_account_id": "proxy-1", "model_version": "v1", "sources": []},
            ):
                with self.assertRaisesRegex(migrate.MigrationError, "rollback would be unsafe"):
                    migrate.plan_phase(args, state)
            self.assertEqual(state["conflicts"][0]["kn_id"], "kn-1")
            self.assertEqual(state["stages"]["plan"]["status"], "failed")

    def test_replanning_preserves_migration_ownership_for_rollback(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            state = migrate.new_state()
            state["networks"]["kn-1"] = {
                "created_by_migration": True,
                "proxy_account_id": "proxy-1",
                "backfill_status": "ready",
            }
            with mock.patch.object(migrate, "run_caller_migration", return_value={}), mock.patch.object(
                migrate, "list_networks", return_value=["kn-1"]
            ), mock.patch.object(
                migrate,
                "proxy_plan",
                return_value={"proxy_account_id": "proxy-1", "model_version": "v1", "sources": []},
            ):
                migrate.plan_phase(args, state)
            self.assertTrue(state["networks"]["kn-1"]["created_by_migration"])

    def test_checkpoint_skips_ready_network_and_failed_network_resumes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            state = migrate.new_state()
            state["stages"]["caller_authorization"] = {"status": "completed"}
            state["stages"]["plan"] = {"status": "completed"}
            state["networks"] = {
                "kn-ready": {"backfill_status": "ready"},
                "kn-retry": {
                    "backfill_status": "planned",
                    "created_by_migration": True,
                },
            }
            with mock.patch.object(
                migrate, "request_json", side_effect=migrate.MigrationError("temporary")
            ) as request:
                with self.assertRaisesRegex(migrate.MigrationError, "failed for 1"):
                    migrate.proxy_backfill_phase(args, state)
                self.assertEqual(request.call_count, 1)
                self.assertEqual(state["networks"]["kn-retry"]["backfill_status"], "failed")

            mapping = {
                "proxy_account_id": "proxy-1",
                "lifecycle_status": "active",
                "sync_status": "ready",
                "published_model_version": "model-2",
                "synced_model_version": "model-2",
            }
            with mock.patch.object(migrate, "request_json", return_value=mapping) as request:
                migrate.proxy_backfill_phase(args, state)
                self.assertEqual(request.call_count, 1)
            self.assertEqual(state["stages"]["proxy_backfill"]["status"], "completed")

    def test_empty_installation_completes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            state = migrate.new_state()
            state["stages"]["caller_authorization"] = {"status": "completed"}
            state["stages"]["plan"] = {"status": "completed"}
            migrate.proxy_backfill_phase(args, state)
            self.assertEqual(state["stages"]["proxy_backfill"]["status"], "completed")


class VerifyAndRollbackTest(unittest.TestCase):
    def test_verify_sets_enable_gate_only_after_all_checks_pass(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            source = {
                "resource_type": "resource",
                "resource_id": "resource-1",
                "operation": "query_data",
            }
            state = migrate.new_state()
            state["stages"]["proxy_backfill"] = {"status": "completed"}
            state["networks"] = {
                "kn-1": {
                    "proxy_account_id": "proxy-1",
                    "authorization_sources": [source],
                }
            }

            def response(_base: str, path: str, _user: str, method: str = "GET"):
                if path.endswith("proxy-accounts/reconcile"):
                    self.assertEqual(method, "POST")
                    return {
                        "missing_mappings": [],
                        "orphan_mappings": [],
                        "conflicting_proxy_accounts": {},
                        "authorization_drift": {},
                    }
                if path.endswith("proxy-account/plan"):
                    return {"model_version": "model-2", "sources": [source]}
                if "managed-proxy-accounts" in path:
                    return {
                        "enabled": True,
                        "login_enabled": False,
                        "credential_issuance_enabled": False,
                    }
                return {
                    "proxy_account_id": "proxy-1",
                    "lifecycle_status": "active",
                    "sync_status": "ready",
                    "published_model_version": "model-2",
                    "synced_model_version": "model-2",
                }

            with mock.patch.object(migrate, "request_json", side_effect=response):
                migrate.verify_phase(args, state)
            self.assertTrue(state["safe_to_enable_proxy"])
            self.assertEqual(state["stages"]["verify"]["status"], "completed")

    def test_verify_fails_on_reconcile_drift(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            state = migrate.new_state()
            state["stages"]["proxy_backfill"] = {"status": "completed"}
            with mock.patch.object(
                migrate,
                "request_json",
                return_value={"authorization_drift": {"kn-1": {"policies_restored": 1}}},
            ):
                with self.assertRaisesRegex(migrate.MigrationError, "verification failed"):
                    migrate.verify_phase(args, state)
            self.assertFalse(state["safe_to_enable_proxy"])

    def test_rollback_archives_only_migration_created_proxies_and_resumes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = online_args(Path(directory) / "state.json")
            state = migrate.new_state()
            state["safe_to_enable_proxy"] = True
            state["networks"] = {
                "kn-existing": {"created_by_migration": False},
                "kn-created": {"created_by_migration": True},
                "kn-done": {
                    "created_by_migration": True,
                    "rollback_status": "archived",
                },
            }
            def response(_base: str, path: str, _user: str, method: str = "GET"):
                if method == "POST":
                    return None
                if "managed-proxy-accounts" in path:
                    return {
                        "enabled": False,
                        "login_enabled": False,
                        "credential_issuance_enabled": False,
                    }
                return {"proxy_account_id": "proxy-1", "lifecycle_status": "archived"}

            with mock.patch.object(migrate, "request_json", side_effect=response) as request:
                migrate.rollback_phase(args, state)
                self.assertEqual(request.call_count, 3)
                self.assertIn("kn-created", request.call_args_list[0].args[1])
            self.assertFalse(state["safe_to_enable_proxy"])
            self.assertFalse(state["rollback"]["caller_authorization_restored"])


if __name__ == "__main__":
    unittest.main()
