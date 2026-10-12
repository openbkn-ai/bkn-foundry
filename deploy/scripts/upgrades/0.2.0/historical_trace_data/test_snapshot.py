import json
import os
import tempfile
import unittest
from pathlib import Path

import snapshot


class SnapshotTests(unittest.TestCase):
    def test_strict_json_rejects_ambiguous_keys_and_nonfinite_values(self):
        for text in ('{"x":1,"x":2}', '{"nested":{"x":1,"x":2}}', '{"x":NaN}', '{"x":1e400}'):
            with self.subTest(text=text), self.assertRaises(ValueError):
                snapshot.strict_loads(text)

    def test_queries_are_readonly_and_dates_are_explicit_utc(self):
        query = snapshot.export_query("openbkn", "t_operation_audit", [
            ("event_id", "varchar"), ("event_time", "datetime"), ("envelope", "longtext")], "vega", "audit")
        self.assertIn("START TRANSACTION WITH CONSISTENT SNAPSHOT", snapshot.readonly(query))
        self.assertIn("TRANSACTION READ ONLY", snapshot.readonly(query))
        self.assertIn("DATE_FORMAT", query)
        self.assertIn("%fZ", query)
        self.assertNotIn("INSERT", query)

    def test_identifiers_reject_sql_injection(self):
        with self.assertRaises(ValueError):
            snapshot.export_query("openbkn", "x;DROP TABLE x", [], "vega", "audit")

    def test_snapshot_private_and_integrity_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "backup"
            records = [{"source_id": "vega", "kind": "audit", "row": {"event_id": "one"}}]
            manifest = snapshot.save_snapshot(root, records, "instance-a", {"source": "015"})
            self.assertEqual(manifest["record_count"], 1)
            self.assertEqual(os.stat(root).st_mode & 0o777, 0o700)
            self.assertEqual(os.stat(root / "records.jsonl").st_mode & 0o777, 0o600)
            self.assertEqual(list(snapshot.read_snapshot(root)), records)
            (root / "records.jsonl").write_text("{}\n")
            with self.assertRaisesRegex(ValueError, "hash"):
                list(snapshot.read_snapshot(root))

    def test_existing_snapshot_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(FileExistsError):
                snapshot.save_snapshot(Path(tmp), [], "instance", {})

    def test_export_captures_all_columns_not_a_log_dto(self):
        query = snapshot.export_query("openbkn", "t_operation_audit", [("unknown_field", "text")], "bkn-backend", "audit")
        self.assertIn("'unknown_field',`unknown_field`", query)

    def test_metadata_digest_and_record_count_are_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "snapshot"
            snapshot.save_snapshot(root, [], "instance", {})
            manifest = json.loads((root / "snapshot.json").read_text())
            manifest["record_count"] = 1
            (root / "snapshot.json").write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, "count"):
                list(snapshot.read_snapshot(root))


if __name__ == "__main__":
    unittest.main()
