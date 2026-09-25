import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from manifest import ManifestError, closure_digest, entries_digest, verify_activation

FIXTURE = Path(__file__).resolve().parents[6] / "bkn-docs" / "docs" / "foundry" / "bkn-trace" / "testing" / "fixtures" / "0.2.0" / "evidence-kafka-record-golden.json"


class ManifestTest(unittest.TestCase):
    def setUp(self):
        self.fixture = json.loads(FIXTURE.read_text(encoding="utf-8"))

    def test_frozen_entry_and_closure_goldens(self):
        golden = self.fixture["manifest_digest_golden"]
        self.assertEqual(entries_digest(golden["entries"]), golden["canonical_sha256"])
        closure = self.fixture["manifest_closure_digest_golden"]
        self.assertEqual(closure_digest(closure["document"]["manifest_header"], closure["document"]["results"]), closure["canonical_sha256"])

    def test_activation_rejects_number_and_digest_mismatch(self):
        entry = dict(self.fixture["manifest_digest_golden"]["entries"][0])
        entry["source_primary_key"] = 1001
        with self.assertRaises(ManifestError):
            entries_digest([entry])
        manifest = {"manifest_id": entry["manifest_id"], "contract_sha": self.fixture["contract_sha"], "state": "draft", "source_snapshot_at": "2026-09-22T08:00:00.000Z", "entry_count": "1", "entries_digest": "0" * 64}
        with self.assertRaises(ManifestError):
            verify_activation(manifest, self.fixture["manifest_digest_golden"]["entries"])


if __name__ == "__main__":
    unittest.main()
