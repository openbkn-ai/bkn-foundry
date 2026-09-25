import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from bridge import load_active_artifact, load_checkpoint, publish_encoded_entries, publish_entries, publish_frozen_snapshot
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
        self.tail = dict(self.gap, entry_id="tail-gap", source_primary_key="1003")
        self.entries = [self.publish, self.publish_second, self.gap, self.tail]
        self.manifest = {"manifest_id": self.publish["manifest_id"], "contract_sha": fixture["contract_sha"], "state": "active", "source_snapshot_at": "2026-09-22T08:00:00.000Z", "entry_count": "4", "entries_digest": entries_digest(self.entries)}

    def test_active_artifact_requires_matching_go_activation_receipt(self):
        artifact = {key: self.manifest[key] for key in ("manifest_id", "contract_sha", "source_snapshot_at", "entry_count", "entries_digest")}
        artifact["entries"] = [{key: value for key, value in entry.items() if key != "entry_id"} for entry in self.entries]
        receipt = {key: artifact[key] for key in ("manifest_id", "contract_sha", "source_snapshot_at", "entry_count", "entries_digest")}
        receipt["state"] = "active"
        with tempfile.TemporaryDirectory() as root:
            artifact_path, receipt_path = Path(root) / "manifest.json", Path(root) / "active.json"
            artifact_path.write_text(json.dumps(artifact), encoding="utf-8")
            receipt_path.write_text(json.dumps(receipt), encoding="utf-8")
            manifest, entries = load_active_artifact(artifact_path, receipt_path)
            self.assertEqual(manifest["state"], "active")
            self.assertEqual(len(entries), 4)
            receipt_path.write_text(json.dumps(dict(receipt, entries_digest="0" * 64)), encoding="utf-8")
            with self.assertRaisesRegex(ManifestError, "receipt"):
                load_active_artifact(artifact_path, receipt_path)

    @staticmethod
    def ack(entry):
        return {"topic": "openbkn.evidence.v1", "partition": 1, "offset": 9, "event_id": entry["event_id"]}

    def test_source_cursor_not_nonmonotonic_entry_id(self):
        with tempfile.TemporaryDirectory() as root:
            checkpoint = Path(root) / "bridge-checkpoint.json"
            self.assertEqual(publish_entries(self.manifest, self.entries, checkpoint, self.ack), ["z-last", "a-first"])
            saved = load_checkpoint(checkpoint, self.manifest["manifest_id"], self.manifest["source_snapshot_at"])
            self.assertEqual((saved["source_table"], saved["source_primary_key"]), (self.tail["source_table"], self.tail["source_primary_key"]))
            self.assertIn("event_id", saved["event_identity"])
            self.assertEqual(saved["last_kafka_ack"]["offset"], 9)
            self.assertTrue(saved["completed"])
            self.assertEqual(saved["classification_counts"], {"coverage_gap": 2, "publish": 2})
            self.assertEqual(publish_entries(self.manifest, self.entries, checkpoint, self.ack), [])

    def test_draft_manifest_cannot_publish(self):
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaises(ManifestError):
                publish_entries(dict(self.manifest, state="draft"), self.entries, Path(root) / "checkpoint", self.ack)

    def test_frozen_source_entries_need_no_central_entry_id(self):
        entry = {key: value for key, value in self.publish.items() if key != "entry_id"}
        manifest = dict(self.manifest, entry_count="1", entries_digest=entries_digest([entry]))
        with tempfile.TemporaryDirectory() as root:
            emitted = publish_entries(manifest, [entry], Path(root) / "checkpoint", self.ack)
        self.assertEqual(emitted, [(entry["source_table"], entry["source_primary_key"])])

    def test_encoded_bridge_uses_only_snapshot_event_and_kafka_ack(self):
        class Metadata:
            topic, partition, offset = "openbkn.evidence.v1", 0, 12

        class Producer:
            def send(self, topic, **kwargs):
                self.topic, self.kwargs = topic, kwargs
                return type("Future", (), {"get": lambda _self, timeout: Metadata()})()

        event = {
            "event_id": self.publish["event_id"], "payload_hash": self.publish["payload_hash"],
            "producer_id": self.publish["producer_id"], "producer_stream_id": self.publish["producer_stream_id"],
            "producer_epoch": self.publish["producer_epoch"], "producer_sequence": self.publish["producer_sequence"],
        }
        # This test uses just the publish entries; non-publish entries are
        # deliberately absent from the source Event map and never sent.
        entries = [self.publish]
        manifest = dict(self.manifest, entry_count="1", entries_digest=entries_digest(entries))
        producer = Producer()
        with tempfile.TemporaryDirectory() as root:
            emitted = publish_encoded_entries(
                manifest, entries, {(self.publish["source_table"], self.publish["source_primary_key"]): event},
                Path(root) / "checkpoint", producer, "openbkn.evidence.v1", 5, "bridge#boot-1",
            )
        self.assertEqual(emitted, ["z-last"])
        self.assertEqual(producer.topic, "openbkn.evidence.v1")
        self.assertEqual(producer.kwargs["key"], self.publish["producer_stream_id"].encode("utf-8"))
        self.assertEqual(dict(producer.kwargs["headers"])["bkn-evidence-record-class"], b"migration")
        self.assertEqual(dict(producer.kwargs["headers"])["bkn-evidence-migration-id"], self.manifest["manifest_id"].encode("utf-8"))

    def test_source_drift_sends_nothing_and_never_creates_checkpoint(self):
        event = {
            "event_id": self.publish["event_id"], "payload_hash": self.publish["payload_hash"],
            "producer_id": self.publish["producer_id"], "producer_stream_id": self.publish["producer_stream_id"],
            "producer_epoch": int(self.publish["producer_epoch"]), "producer_sequence": int(self.publish["producer_sequence"]),
        }
        entry = {key: value for key, value in self.publish.items() if key != "entry_id"}
        manifest = dict(self.manifest, entry_count="1", entries_digest=entries_digest([entry]))

        class Cursor:
            description = [(key,) for key in ("outbox_id", "event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence", "envelope", "status", "locked_until")]

            def __init__(self, index):
                self.index = index

            def execute(self, _query, _args):
                pass

            def fetchall(self):
                if self.index:
                    return []
                return [(int(entry["source_primary_key"]), event["event_id"], event["payload_hash"], event["producer_id"], event["producer_stream_id"], event["producer_epoch"], event["producer_sequence"], json.dumps({"event": event}), "retry", None)]

            def close(self):
                pass

        class Connection:
            def __init__(self):
                self.index = 0

            def cursor(self):
                cursor = Cursor(self.index)
                self.index += 1
                return cursor

        class Producer:
            def __init__(self):
                self.sent = 0

            def send(self, *_args, **_kwargs):
                self.sent += 1
                raise AssertionError("source drift must not reach Kafka")

        producer = Producer()
        with tempfile.TemporaryDirectory() as root:
            checkpoint = Path(root) / "bridge-checkpoint.json"
            with self.assertRaises(ManifestError):
                publish_frozen_snapshot(Connection(), manifest, [entry], checkpoint, producer, "openbkn.evidence.v1", 5, "bridge#boot-1")
            self.assertEqual(producer.sent, 0)
            self.assertFalse(checkpoint.exists())

    def test_crash_boundaries_never_skip_uncheckpointed_ack(self):
        for stage in ("after_temp_write", "after_file_fsync", "after_rename"):
            with self.subTest(stage=stage), tempfile.TemporaryDirectory() as root:
                checkpoint = Path(root) / "bridge-checkpoint.json"
                def crash(current):
                    if current == stage:
                        raise SimulatedCrash(stage)
                with self.assertRaises(SimulatedCrash):
                    publish_entries(self.manifest, self.entries, checkpoint, self.ack, crash)
                checkpoint.unlink(missing_ok=True)  # model rename loss before directory fsync
                self.assertEqual(publish_entries(self.manifest, self.entries, checkpoint, self.ack), ["z-last", "a-first"])
        with tempfile.TemporaryDirectory() as root:
            checkpoint = Path(root) / "bridge-checkpoint.json"
            def crash_after_durable(current):
                if current == "after_directory_fsync":
                    raise SimulatedCrash(current)
            with self.assertRaises(SimulatedCrash):
                publish_entries(self.manifest, self.entries, checkpoint, self.ack, crash_after_durable)
            self.assertEqual(publish_entries(self.manifest, self.entries, checkpoint, self.ack), ["a-first"])
            saved = load_checkpoint(checkpoint, self.manifest["manifest_id"], self.manifest["source_snapshot_at"])
            self.assertEqual(saved["classification_counts"], {"coverage_gap": 2, "publish": 2})


if __name__ == "__main__":
    unittest.main()
