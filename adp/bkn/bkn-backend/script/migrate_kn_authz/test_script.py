# Copyright openbkn.ai
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

import unittest
from unittest.mock import MagicMock

from script import (
    KN_CREATOR_OPERATIONS,
    NETWORK_BUILDER_ROLE_ID,
    MigrationPlan,
    Policy,
    ResourceRow,
    ResourceParent,
    SafeAccount,
    apply_safe_plan,
    build_plan,
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


if __name__ == "__main__":
    unittest.main()
