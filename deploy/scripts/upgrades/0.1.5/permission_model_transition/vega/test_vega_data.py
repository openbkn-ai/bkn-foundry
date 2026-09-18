# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest
from unittest.mock import MagicMock

from vega import vega_data as migration


class BuildPlanTest(unittest.TestCase):
    def test_backfills_catalog_creator_grants_and_resource_parents(self):
        catalogs = [
            migration.Catalog("catalog-1", "user-1", "user", False),
            migration.Catalog("builtin-1", "", "", True),
        ]
        parents = [
            migration.ResourceParent("resource-2", "catalog-1"),
            migration.ResourceParent("resource-1", "catalog-1"),
        ]

        plan = migration.build_plan(catalogs, parents, {"user-1"})

        self.assertEqual([], plan.failures)
        self.assertEqual(
            ["full_business_access", "authorize"],
            [grant.operation for grant in plan.grants],
        )
        self.assertEqual(sorted(parents), plan.parents)

    def test_accepts_app_creator_like_the_runtime_catalog_flow(self):
        catalog = migration.Catalog("catalog-1", "app-1", "app", False)

        plan = migration.build_plan([catalog], [], {"app-1"})

        self.assertEqual([], plan.failures)
        self.assertEqual(2, len(plan.grants))

    def test_rejects_missing_creator_and_orphan_resource(self):
        catalogs = [migration.Catalog("catalog-1", "missing-user", "user", False)]
        parents = [migration.ResourceParent("resource-1", "missing-catalog")]

        plan = migration.build_plan(catalogs, parents, set())

        self.assertEqual(
            ["missing_creator_account", "missing_parent"],
            [failure.code for failure in plan.failures],
        )

    def test_creator_grant_identity_matches_bkn_safe_contract(self):
        catalog = migration.Catalog("catalog-1", "user-1", "user", False)

        bundle, authorize = migration.creator_grants(catalog)

        self.assertEqual(
            "a670bff841b57fe9cd54b8b24ba1be8700845ee6b4a574f06d135729f5f0eaa6",
            bundle.grant_id,
        )
        self.assertEqual(
            "03eb9d6bd96e6ceb1fcb0878f6a720da39fe2619d6e8415c2e67e2712c342d9e",
            authorize.grant_id,
        )
        self.assertEqual(bundle.grant_id, bundle.projection_key)
        self.assertEqual("community_bundle", bundle.policy_source)
        self.assertEqual("system_derived", authorize.policy_source)
        self.assertEqual("system", authorize.created_by)

    def test_partial_or_denied_creator_policy_blocks_privilege_expansion(self):
        catalog = migration.Catalog("catalog-1", "user-1", "user", False)
        key = ("user-1", "catalog:catalog-1")

        partial = migration.build_plan(
            [catalog],
            [],
            {"user-1"},
            existing_creator_policies={key: [("view_detail", "allow", "legacy")]},
        )
        denied = migration.build_plan(
            [catalog],
            [],
            {"user-1"},
            existing_creator_policies={
                key: [("view_detail", "deny", "professional_rule")]
            },
        )

        self.assertEqual(["partial_creator_policy"], [item.code for item in partial.failures])
        self.assertEqual(["partial_creator_policy"], [item.code for item in denied.failures])

    def test_complete_legacy_creator_policy_can_be_upgraded(self):
        catalog = migration.Catalog("catalog-1", "user-1", "user", False)
        key = ("user-1", "catalog:catalog-1")
        policies = [
            (operation, "allow", "legacy")
            for operation in migration.LEGACY_CATALOG_CREATOR_OPERATIONS
        ]

        plan = migration.build_plan(
            [catalog],
            [],
            {"user-1"},
            existing_creator_policies={key: policies},
        )

        self.assertEqual([], plan.failures)
        self.assertEqual(2, len(plan.grants))

    def test_rejects_wildcard_and_whitespace_resource_ids(self):
        catalogs = [migration.Catalog("catalog-*", "user-1", "user", False)]
        parents = [migration.ResourceParent(" resource-1", "catalog-*")]

        plan = migration.build_plan(catalogs, parents, {"user-1"})

        self.assertEqual(
            ["invalid_catalog_id", "invalid_resource_id"],
            [failure.code for failure in plan.failures],
        )


class ApplyPlanTest(unittest.TestCase):
    def test_rolls_back_if_parent_reconciliation_fails(self):
        connection = MagicMock()
        cursor = connection.cursor.return_value.__enter__.return_value
        cursor.executemany.side_effect = RuntimeError("write failed")
        plan = migration.MigrationPlan(
            parents=[migration.ResourceParent("resource-1", "catalog-1")]
        )

        with self.assertRaisesRegex(RuntimeError, "write failed"):
            migration.apply_plan(connection, plan)

        connection.rollback.assert_called_once_with()
        connection.commit.assert_not_called()


if __name__ == "__main__":
    unittest.main()
