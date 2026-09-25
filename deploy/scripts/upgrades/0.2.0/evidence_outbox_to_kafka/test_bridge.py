import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from bridge import load_checkpoint, publish_entries

FIXTURE = Path(__file__).resolve().parents[6] / "bkn-docs" / "docs" / "foundry" / "bkn-trace" / "testing" / "fixtures" / "0.2.0" / "evidence-kafka-record-golden.json"


class BridgeTest(unittest.TestCase):
    def test_publish_only_and_restart_checkpoint(self):
        fixture = json.loads(FIXTURE.read_text(encoding="utf-8"))
        publish = dict(fixture["manifest_digest_golden"]["entries"][0])
        publish["entry_id"] = "entry-1"
        gap = dict(publish, entry_id="entry-2", classification="coverage_gap", event_id=None, payload_hash=None, producer_id=None, producer_stream_id=None, producer_epoch=None, producer_sequence=None)
        manifest = {"manifest_id": publish["manifest_id"], "contract_sha": fixture["contract_sha"], "state": "active", "source_snapshot_at": "2026-09-22T08:00:00.000Z", "entry_count": "2", "entries_digest": ""}
        from manifest import entries_digest
        manifest["entries_digest"] = entries_digest([publish, gap])
        with tempfile.TemporaryDirectory() as root:
            sent = []
            checkpoint = Path(root) / "bridge-checkpoint.json"
            self.assertEqual(publish_entries(manifest, [publish, gap], checkpoint, lambda entry: sent.append(entry["entry_id"])), ["entry-1"])
            self.assertEqual(sent, ["entry-1"])
            self.assertEqual(load_checkpoint(checkpoint, manifest["manifest_id"], manifest["source_snapshot_at"])["entry_id"], "entry-1")
            self.assertEqual(publish_entries(manifest, [publish, gap], checkpoint, lambda entry: sent.append(entry["entry_id"])), [])


if __name__ == "__main__":
    unittest.main()
