import importlib.util
import sys
import unittest
from pathlib import Path


MODULE = Path(__file__).with_name("backfill_creator_grants.py")
SPEC = importlib.util.spec_from_file_location("backfill_creator_grants", MODULE)
migration = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = migration
SPEC.loader.exec_module(migration)


class CreatorGrantPlanTest(unittest.TestCase):
    def test_missing_direct_creator_grant_is_backfilled(self):
        resource = migration.Resource("catalog", "catalog-1", "creator-1", "user")
        item = migration.plan_resource(resource, True, [])
        self.assertEqual("insert", item.action)

    def test_full_existing_legacy_grant_is_not_rewritten(self):
        resource = migration.Resource("knowledge_network", "kn-1", "creator-1", "user")
        grants = [(operation, "legacy") for operation in migration.RESOURCE_OPERATIONS["knowledge_network"]]
        item = migration.plan_resource(resource, True, grants)
        self.assertEqual("keep", item.action)

    def test_existing_canonical_bundle_and_authorize_are_not_rewritten(self):
        resource = migration.Resource("catalog", "catalog-1", "creator-1", "user")
        item = migration.plan_resource(
            resource,
            True,
            [
                (migration.BUNDLE, migration.COMMUNITY_BUNDLE),
                ("authorize", migration.SYSTEM_DERIVED),
            ],
        )
        self.assertEqual("keep", item.action)

    def test_partial_creator_grant_blocks_instead_of_escalating(self):
        resource = migration.Resource("catalog", "catalog-1", "creator-1", "user")
        item = migration.plan_resource(resource, True, [("view_detail", "legacy")])
        self.assertEqual("block", item.action)
        self.assertIn("partial", item.reason)

    def test_internal_catalog_is_not_granted_to_an_individual_creator(self):
        resource = migration.Resource("catalog", "internal-1", "creator-1", "user", internal=True)
        item = migration.plan_resource(resource, True, [])
        self.assertEqual("skip", item.action)

    def test_grant_identity_matches_runtime_projection_contract(self):
        resource = migration.Resource("catalog", "catalog-1", "creator-1", "user")
        rows = migration.create_grant_rows(resource)
        self.assertEqual(2, len(rows))
        self.assertEqual(
            rows[0][0],
            migration.projection_key(
                "creator-1", "catalog:catalog-1", migration.BUNDLE,
                migration.ALLOW, migration.COMMUNITY_BUNDLE, migration.SYSTEM,
            ),
        )


if __name__ == "__main__":
    unittest.main()
