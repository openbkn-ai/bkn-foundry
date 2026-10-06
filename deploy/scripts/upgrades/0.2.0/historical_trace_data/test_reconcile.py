import unittest

import reconcile


class ReconcileTests(unittest.TestCase):
    def item(self):
        return {"kind": "audit", "source_id": "vega", "disposition": "convert", "target_id": "one",
                "content_hash": "hash", "payload": {"event_id": "one", "occurred_at": "2026-09-02T00:00:00Z"}}

    def row(self, item):
        return {"content_hash": "hash", "dedup_hash": "hash", "payload": item["payload"], "target_table": "audit_event_202609", "source_id": "vega", "occurred_at": "2026-09-02T00:00:00.000000Z", "topic": "openbkn.audit.v1", "partition": 0, "offset": 1}

    def test_receipt_does_not_substitute_for_database_readback(self):
        result = reconcile.check([self.item()], lambda item: None)
        self.assertEqual(result["counts"], {"missing": 1})
        self.assertFalse(result["complete"])

    def test_payload_and_dedup_hash_both_must_match(self):
        item = self.item()
        row = self.row(item)
        row["dedup_hash"] = "wrong"
        self.assertEqual(reconcile.check([item], lambda _: row)["counts"], {"conflict": 1})
        row["dedup_hash"] = "hash"
        self.assertTrue(reconcile.check([item], lambda _: row)["complete"])
        row["payload"] = {"event_id": "one", "occurred_at": "changed"}
        self.assertEqual(reconcile.check([item], lambda _: row)["counts"], {"conflict": 1})

    def test_non_audit_records_are_not_reported_as_verified(self):
        item = self.item()
        item["kind"] = "evidence"
        result = reconcile.check([item], lambda _: self.fail("must not fetch Evidence"))
        self.assertEqual(result["counts"], {"not_checked": 1})
        self.assertFalse(result["complete"])

    def test_wrong_native_month_time_source_or_coordinates_are_conflicts(self):
        item = self.item()
        for key, value in (("target_table", "audit_event_190001"), ("occurred_at", "1900-01-01T00:00:00Z"), ("source_id", "wrong"), ("topic", "wrong"), ("offset", -1)):
            with self.subTest(key=key):
                row = self.row(item)
                row[key] = value
                self.assertEqual(reconcile.check([item], lambda _: row)["counts"], {"conflict": 1})


if __name__ == "__main__":
    unittest.main()
