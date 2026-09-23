import json
import tempfile
import unittest
from pathlib import Path

from archive_operation_audit import export_snapshot, verify_archive


class ArchiveOperationAuditTest(unittest.TestCase):
    def test_export_and_verify_are_deterministic_and_non_destructive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "snapshot.jsonl"
            source.write_text(
                json.dumps({"event_id": "b", "event_time": "2026-09-02T00:00:00Z"}) + "\n"
                + json.dumps({"event_id": "a", "event_time": "2026-09-01T00:00:00Z"}) + "\n",
                encoding="utf-8",
            )
            archive = root / "archive.jsonl"
            manifest = root / "manifest.json"
            result = export_snapshot(source, archive, manifest)
            self.assertFalse(result["drop_executed"])
            verified = verify_archive(archive, manifest)
            self.assertTrue(verified["verified"])
            self.assertEqual(verified["row_count"], 2)
            self.assertNotIn(b"INSERT", archive.read_bytes())


if __name__ == "__main__":
    unittest.main()
