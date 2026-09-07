# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import io
import unittest
from contextlib import redirect_stderr
from datetime import datetime
from unittest.mock import MagicMock
from unittest.mock import patch

import script as migration

from script import (
    GrantIndex,
    KN_CREATOR_OPERATIONS,
    NETWORK_BUILDER_ROLE_ID,
    MigrationPlan,
    Policy,
    ProxyMigrationPlan,
    ProxyNetworkPlan,
    ProxySource,
    ResourceRow,
    ResourceParent,
    SafeAccount,
    apply_safe_plan,
    build_plan,
    derive_proxy_sources,
    load_proxy_plan,
    sync_proxy_sources,
)


def resource(
    resource_type,
    resource_id,
    kn_id="kn-1",
    branch="main",
    creator_id="",
    creator_type="",
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
        creator_id=creator_id,
        creator_type=creator_type,
    )


class BuildPlanTest(unittest.TestCase):
    def test_builds_creator_policies_and_six_parent_rows(self):
        rows = [
            resource(
                "knowledge_network",
                "kn-1",
                creator_id="owner-1",
                creator_type="user",
            ),
            resource("concept_group", "group-1"),
            resource("object_type", "object-1"),
            resource("relation_type", "relation-1"),
            resource(
                "action_type",
                "action-1",
                creator_id="app-1",
                creator_type="app",
            ),
            resource("metric", "metric-1"),
            resource("risk_type", "risk-1"),
        ]
        accounts = {
            "owner-1": SafeAccount("owner-1", True, "other"),
            "app-1": SafeAccount("app-1", True, "app"),
        }

        plan = build_plan(rows, accounts, branch_updates=0)

        self.assertEqual([], plan.failures)
        self.assertEqual(6, len(plan.parents))
        self.assertEqual(len(KN_CREATOR_OPERATIONS) + 2, len(plan.policies))
        self.assertIn(
            ("action_type", "kn-1/action-1", "execute"),
            {
                (policy.resource_type, policy.resource_id, policy.operation)
                for policy in plan.policies
            },
        )
        self.assertIn(
            (NETWORK_BUILDER_ROLE_ID, "knowledge_network", "*", "create"),
            {
                (
                    policy.accessor_id,
                    policy.resource_type,
                    policy.resource_id,
                    policy.operation,
                )
                for policy in plan.policies
            },
        )

    def test_reports_branch_collision_after_blank_normalization(self):
        rows = [
            resource(
                "knowledge_network",
                "kn-1",
                branch="",
                creator_id="owner-1",
                creator_type="user",
            ),
            resource(
                "knowledge_network",
                "kn-1",
                branch="main",
                creator_id="owner-1",
                creator_type="user",
            ),
        ]

        plan = build_plan(
            rows,
            {"owner-1": SafeAccount("owner-1", True, "other")},
            branch_updates=1,
        )

        self.assertEqual(["branch_conflict"], [item.code for item in plan.failures])

    def test_reports_invalid_child_id_and_missing_parent(self):
        rows = [
            resource("object_type", "bad/id"),
            resource("metric", "metric-1", kn_id="missing-kn"),
        ]

        plan = build_plan(rows, {}, branch_updates=0)

        self.assertEqual(
            ["invalid_resource_id", "missing_parent"],
            [item.code for item in plan.failures],
        )

    def test_reports_creator_account_failures(self):
        rows = [
            resource(
                "knowledge_network",
                "kn-disabled",
                creator_id="disabled",
                creator_type="user",
            ),
            resource(
                "knowledge_network",
                "kn-missing",
                creator_id="missing",
                creator_type="user",
            ),
            resource(
                "knowledge_network",
                "kn-mismatch",
                creator_id="app-1",
                creator_type="user",
            ),
            resource(
                "knowledge_network",
                "kn-invalid",
                creator_id="owner-1",
                creator_type="realname",
            ),
            resource(
                "knowledge_network",
                "kn-spaced",
                creator_id=" owner-1",
                creator_type="user",
            ),
        ]
        accounts = {
            "disabled": SafeAccount("disabled", False, "other"),
            "app-1": SafeAccount("app-1", True, "app"),
            "owner-1": SafeAccount("owner-1", True, "other"),
        }

        plan = build_plan(rows, accounts, branch_updates=0)

        self.assertEqual(
            [
                "creator_disabled",
                "creator_not_found",
                "creator_type_mismatch",
                "invalid_creator",
                "invalid_creator",
            ],
            [item.code for item in plan.failures],
        )


class ApplySafePlanTest(unittest.TestCase):
    def test_rolls_back_when_rebuild_fails_after_cleanup(self):
        connection = MagicMock()
        cursor = connection.cursor.return_value.__enter__.return_value
        cursor.execute.side_effect = [7, 6]
        cursor.executemany.side_effect = RuntimeError("write failed")
        plan = MigrationPlan(
            resources={},
            branch_updates=0,
            policies=[Policy("owner-1", "knowledge_network", "kn-1", "modify")],
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
            apply_safe_plan(connection, plan)

        connection.rollback.assert_called_once_with()
        connection.commit.assert_not_called()


class ProxyPlanTest(unittest.TestCase):
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
        cursor.fetchone.side_effect = [{"count": 1}, None, {"count": 0}]
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


class OneShotMigrationTest(unittest.TestCase):
    @patch.object(migration, "verify_proxy_plan")
    @patch.object(migration, "apply_proxy_plan", return_value={"mappings_ready": 0})
    @patch.object(migration, "apply_safe_plan", return_value=(0, 0))
    @patch.object(migration, "normalize_branches", return_value=0)
    @patch.object(migration, "load_proxy_plan", return_value=ProxyMigrationPlan())
    @patch.object(migration, "build_plan", return_value=MigrationPlan({}, 0))
    @patch.object(migration, "load_existing_safe_counts", return_value=(0, 0))
    @patch.object(migration, "count_branch_updates", return_value=0)
    @patch.object(migration, "load_accounts", return_value={})
    @patch.object(migration, "load_resources", return_value=[])
    @patch.object(migration, "connect_database")
    def test_apply_runs_complete_offline_flow_once(
        self,
        connect_database,
        load_resources,
        load_accounts,
        count_branch_updates,
        load_existing_safe_counts,
        build_plan,
        load_proxy_plan,
        normalize_branches,
        apply_safe_plan_mock,
        apply_proxy_plan_mock,
        verify_proxy_plan,
    ):
        bkn_connection = MagicMock()
        safe_connection = MagicMock()
        connect_database.side_effect = [bkn_connection, safe_connection]

        self.assertEqual(0, migration.run())

        apply_safe_plan_mock.assert_called_once()
        apply_proxy_plan_mock.assert_called_once()
        self.assertEqual(
            migration.MIGRATION_GRANTOR_ID,
            apply_proxy_plan_mock.call_args.args[3],
        )
        verify_proxy_plan.assert_called_once()
        safe_connection.commit.assert_called_once_with()
        bkn_connection.commit.assert_called_once_with()

    @patch.object(migration, "run", side_effect=RuntimeError("database write failed"))
    def test_command_prints_the_complete_traceback_on_failure(self, run):
        error_output = io.StringIO()

        with redirect_stderr(error_output):
            self.assertEqual(1, migration.main())

        self.assertIn("Traceback (most recent call last):", error_output.getvalue())
        self.assertIn("RuntimeError: database write failed", error_output.getvalue())


if __name__ == "__main__":
    unittest.main()
