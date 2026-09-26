import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from manifest import ManifestError
from migration_cli import MAX_KAFKA_REQUEST_SIZE, run
from snapshot import issue_manifest


class Cursor:
    description = [(name,) for name in ("outbox_id", "event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence", "envelope", "status", "locked_until")]

    def __init__(self, connection):
        self.connection = connection
        self.table = ""

    def execute(self, query, _args=None):
        self.connection.calls.append(query)
        if "DATE_FORMAT(UTC_TIMESTAMP" in query:
            self.connection.snapshot_cursor = True
        elif "FROM bkn_backend_trace_outbox" in query:
            self.table = "backend"
        elif "FROM ontology_query_trace_outbox" in query:
            self.table = "ontology"

    def fetchone(self):
        return ("2026-09-25T10:00:00.000000Z",)

    def fetchall(self):
        if self.table != "backend":
            return []
        event = self.connection.event
        return [(1, event["event_id"], event["payload_hash"], event["producer_id"], event["producer_stream_id"], event["producer_epoch"], event["producer_sequence"], json.dumps({"event": event}), "pending", None)]

    def close(self):
        pass


class Source:
    def __init__(self, event):
        self.event = event
        self.calls = []
        self.snapshot_cursor = False

    def cursor(self):
        return Cursor(self)

    def rollback(self):
        self.calls.append("ROLLBACK")

    def close(self):
        pass


class Metadata:
    topic, partition, offset = "openbkn.evidence.v1", 0, 17


class Producer:
    def __init__(self):
        self.sent = []
        self.closed = False

    def send(self, topic, **kwargs):
        self.sent.append((topic, kwargs))
        return type("Future", (), {"get": lambda _self, timeout: Metadata()})()

    def flush(self, timeout=None):
        pass

    def close(self, timeout=None):
        self.closed = True


class MigrationCLITest(unittest.TestCase):
    def setUp(self):
        self.event = {
            "event_id": "evt-1", "payload_hash": "a" * 64, "producer_id": "bkn-backend",
            "producer_stream_id": "bkn-backend", "producer_epoch": 1, "producer_sequence": 1,
        }

    def test_kafka_request_limit_includes_record_overhead_beyond_c1_event_value_limit(self):
        self.assertGreater(MAX_KAFKA_REQUEST_SIZE, 1_048_576)

    def test_cli_directory_does_not_shadow_kafka_python_package(self):
        script_dir = Path(__file__).resolve().parent
        environment = os.environ.copy()
        with tempfile.TemporaryDirectory() as root:
            kafka_package = Path(root) / "kafka"
            kafka_package.mkdir()
            (kafka_package / "__init__.py").write_text("class KafkaProducer: pass\n", encoding="utf-8")
            environment["PYTHONPATH"] = root
            result = subprocess.run(
                [
                    sys.executable,
                    "-c",
                    "from kafka import KafkaProducer; from kafka_ack import publish_with_ack; assert KafkaProducer and publish_with_ack",
                ],
                cwd=script_dir,
                env=environment,
                capture_output=True,
                text=True,
                check=False,
            )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_snapshot_command_uses_consistent_readonly_transaction_and_writes_payload_free_artifact(self):
        source = Source(self.event)
        with tempfile.TemporaryDirectory() as root:
            target = Path(root) / "manifest.json"
            run(["snapshot", "--manifest-id", "mig-1", "--artifact", str(target)], lambda: source, lambda: None, output=lambda _value: None)
            artifact = json.loads(target.read_text(encoding="utf-8"))
        self.assertIn("START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY", source.calls)
        self.assertEqual(artifact["entry_count"], "1")
        self.assertNotIn("envelope", json.dumps(artifact))

    def test_publish_command_requires_matching_active_receipt_before_creating_producer(self):
        source = Source(self.event)
        created = []
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            artifact = {
                "manifest_id": "mig-1", "contract_sha": "0016ad359b11d162e04bb11a78784c33fad0ec8d",
                "source_snapshot_at": "2026-09-25T10:00:00.000Z", "entry_count": "1",
                "entries_digest": "bad", "entries": [],
            }
            artifact_path, receipt_path = root / "manifest.json", root / "active.json"
            artifact_path.write_text(json.dumps(artifact), encoding="utf-8")
            receipt_path.write_text(json.dumps({**{key: value for key, value in artifact.items() if key != "entries"}, "state": "active"}), encoding="utf-8")
            with self.assertRaises(ManifestError):
                run(["publish", "--artifact", str(artifact_path), "--receipt", str(receipt_path), "--checkpoint", str(root / "checkpoint.json"), "--producer-instance-id", "bridge#boot"], lambda: source, lambda: created.append(True), output=lambda _value: None)
        self.assertEqual(created, [])

    def test_publish_command_rereads_source_then_checkpoints_only_after_kafka_ack(self):
        source = Source(self.event)
        producer = Producer()
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            row = {
                "source_table": "bkn_backend_trace_outbox", "outbox_id": 1,
                "event_id": self.event["event_id"], "payload_hash": self.event["payload_hash"],
                "producer_id": self.event["producer_id"], "producer_stream_id": self.event["producer_stream_id"],
                "producer_epoch": self.event["producer_epoch"], "producer_sequence": self.event["producer_sequence"],
                "envelope": json.dumps({"event": self.event}), "status": "pending", "locked_until": None,
            }
            artifact, _ = issue_manifest("mig-1", "2026-09-25T10:00:00.000Z", [row])
            artifact_path, receipt_path = root / "manifest.json", root / "active.json"
            artifact_path.write_text(json.dumps(artifact), encoding="utf-8")
            receipt_path.write_text(json.dumps({
                key: artifact[key] for key in ("manifest_id", "contract_sha", "source_snapshot_at", "entry_count", "entries_digest")
            } | {"state": "active"}), encoding="utf-8")
            checkpoint_path = root / "checkpoint.json"

            result = run([
                "publish", "--artifact", str(artifact_path), "--receipt", str(receipt_path),
                "--checkpoint", str(checkpoint_path), "--producer-instance-id", "bridge#boot",
            ], lambda: source, lambda: producer, output=lambda _value: None)

            checkpoint = json.loads(checkpoint_path.read_text(encoding="utf-8"))
        self.assertEqual(result, 0)
        self.assertEqual(len(producer.sent), 1)
        self.assertTrue(checkpoint["completed"])
        self.assertEqual(checkpoint["last_kafka_ack"], {"topic": "openbkn.evidence.v1", "partition": 0, "offset": 17})
        self.assertTrue(producer.closed)
        self.assertIn("START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY", source.calls)


if __name__ == "__main__":
    unittest.main()
