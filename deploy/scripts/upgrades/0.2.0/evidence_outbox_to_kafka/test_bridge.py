import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from bridge import load_checkpoint, publish_entries
from manifest import ManifestError, entries_digest

FIXTURE = Path(__file__).resolve().parents[6] / "bkn-docs" / "docs" / "foundry" / "bkn-trace" / "testing" / "fixtures" / "0.2.0" / "evidence-kafka-record-golden.json"


class SimulatedCrash(RuntimeError):
    pass


class BridgeTest(unittest.TestCase):
    def setUp(self):
        fixture = json.loads(FIXTURE.read_text(encoding="utf-8"))
        self.publish = dict(fixture["manifest_digest_golden"]["entries"][0], entry_id="z-last")
        self.publish_second = dict(self.publish, entry_id="a-first", event_id="evt-second", source_primary_key="1002")
        self.gap = dict(self.publish, entry_id="entry-gap", classification="coverage_gap", event_id=None, payload_hash=None, producer_id=None, producer_stream_id=None, producer_epoch=None, producer_sequence=None, source_primary_key="0999")
        self.manifest = {"manifest_id": self.publish["manifest_id"], "contract_sha": fixture["contract_sha"], "state": "active", "source_snapshot_at": "2026-09-22T08:00:00.000Z", "entry_count": "3", "entries_digest": entries_digest([self.publish, self.publish_second, self.gap])}

    @staticmethod
    def ack(entry):
        return {"topic": "openbkn.evidence.v1", "partition": 1, "offset": 9, "event_id": entry["event_id"]}

    def test_source_cursor_not_nonmonotonic_entry_id(self):
        with tempfile.TemporaryDirectory() as root:
            checkpoint = Path(root) / "bridge-checkpoint.json"
            self.assertEqual(publish_entries(self.manifest, [self.publish, self.publish_second, self.gap], checkpoint, self.ack), ["z-last", "a-first"])
            saved = load_checkpoint(checkpoint, self.manifest["manifest_id"], self.manifest["source_snapshot_at"])
            self.assertEqual((saved["source_table"], saved["source_primary_key"]), (self.publish_second["source_table"], self.publish_second["source_primary_key"]))
            self.assertIn("event_id", saved["event_identity"])
            self.assertEqual(saved["last_kafka_ack"]["offset"], 9)
            self.assertEqual(publish_entries(self.manifest, [self.publish, self.publish_second, self.gap], checkpoint, self.ack), [])

    def test_draft_manifest_cannot_publish(self):
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaises(ManifestError):
                publish_entries(dict(self.manifest, state="draft"), [self.publish, self.publish_second, self.gap], Path(root) / "checkpoint", self.ack)

    def test_crash_boundaries_never_skip_uncheckpointed_ack(self):
        for stage in ("after_temp_write", "after_file_fsync", "after_rename"):
            with self.subTest(stage=stage), tempfile.TemporaryDirectory() as root:
                checkpoint = Path(root) / "bridge-checkpoint.json"
                def crash(current):
                    if current == stage:
                        raise SimulatedCrash(stage)
                with self.assertRaises(SimulatedCrash):
                    publish_entries(self.manifest, [self.publish, self.publish_second, self.gap], checkpoint, self.ack, crash)
                checkpoint.unlink(missing_ok=True)  # model rename loss before directory fsync
                self.assertEqual(publish_entries(self.manifest, [self.publish, self.publish_second, self.gap], checkpoint, self.ack), ["z-last", "a-first"])
        with tempfile.TemporaryDirectory() as root:
            checkpoint = Path(root) / "bridge-checkpoint.json"
            def crash_after_durable(current):
                if current == "after_directory_fsync":
                    raise SimulatedCrash(current)
            with self.assertRaises(SimulatedCrash):
                publish_entries(self.manifest, [self.publish, self.publish_second, self.gap], checkpoint, self.ack, crash_after_durable)
            self.assertEqual(publish_entries(self.manifest, [self.publish, self.publish_second, self.gap], checkpoint, self.ack), ["a-first"])


if __name__ == "__main__":
    unittest.main()
