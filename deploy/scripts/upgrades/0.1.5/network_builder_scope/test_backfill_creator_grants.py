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
            "779fc40d0307dddf5348ee957e53d73b815ab739d646f9a92c8e48dbf779f47f",
        )

    def test_knowledge_network_backfill_reads_only_the_main_branch(self):
        class Cursor:
            def __init__(self):
                self.calls = []

            def execute(self, statement, parameters=None):
                self.calls.append((statement, parameters))

            def fetchall(self):
                return []

        cursor = Cursor()
        self.assertEqual([], migration.fetch_resources(cursor))
        statement, parameters = cursor.calls[-1]
        self.assertIn("COALESCE(NULLIF(f_branch, ''), %s) = %s", statement)
        self.assertEqual((migration.MAIN_BRANCH, migration.MAIN_BRANCH), parameters)

    def test_all_parameterized_sql_uses_pymysql_placeholders(self):
        class Cursor:
            def __init__(self):
                self.calls = []

            def execute(self, statement, parameters=None):
                self.calls.append((statement, parameters))

            def fetchone(self):
                return None

            def fetchall(self):
                return []

        cursor = Cursor()
        resource = migration.Resource("catalog", "catalog-1", "creator-1", "user")
        self.assertFalse(migration.creator_exists(cursor, resource.creator_id))
        self.assertEqual([], migration.creator_grants(cursor, resource))
        migration.insert_resource_grants(cursor, resource)
        parameterized = [statement for statement, parameters in cursor.calls if parameters]
        self.assertTrue(parameterized)
        self.assertTrue(all("?" not in statement for statement in parameterized))
        self.assertTrue(all("%s" in statement for statement in parameterized))


if __name__ == "__main__":
    unittest.main()
