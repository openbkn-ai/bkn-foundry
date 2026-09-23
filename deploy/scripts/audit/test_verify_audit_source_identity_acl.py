import unittest

from verify_audit_source_identity_acl import TOPIC, verify


class AuditAclVerifierTest(unittest.TestCase):
    def test_non_integrated_sources_are_not_falsely_green(self):
        registry = {"sources": [{"source_id": "legacy", "collection_method": "source_adapter"}]}
        ok, matrix = verify(registry, {"topic": TOPIC, "consumer_group": "writer", "principals": []})
        self.assertTrue(ok)
        self.assertEqual(matrix[0]["status"], "unverified")

    def test_kafka_source_requires_exact_single_acl_mapping(self):
        registry = {
            "sources": [{
                "source_id": "bkn-backend",
                "collection_method": "kafka_audit",
                "allowed_service_names": ["bkn-backend"],
                "allowed_environments": ["production"],
            }]
        }
        acl = {
            "topic": TOPIC,
            "consumer_group": "audit-ledger-writer",
            "principals": [{
                "principal": "User:bkn-backend",
                "source_ids": ["bkn-backend"],
                "service_name": "bkn-backend",
                "environments": ["production"],
                "write_topics": [TOPIC],
                "read_groups": [],
            }],
        }
        ok, matrix = verify(registry, acl)
        self.assertTrue(ok)
        self.assertEqual(matrix[0]["status"], "verified")

        acl["principals"][0]["read_groups"] = ["audit-ledger-writer"]
        ok, matrix = verify(registry, acl)
        self.assertFalse(ok)
        self.assertIn("service_principal_has_consumer_read", matrix[0]["reasons"])


if __name__ == "__main__":
    unittest.main()
