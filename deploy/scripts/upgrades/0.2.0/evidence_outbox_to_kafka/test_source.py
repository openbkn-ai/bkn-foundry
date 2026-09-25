import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from manifest import ManifestError
from source import ActiveLeaseError, classify_row, encode_migration_record


class SourceTest(unittest.TestCase):
    def setUp(self):
        self.event = {
            "event_id": "evt-1", "payload_hash": "a" * 64, "producer_id": "bkn-backend",
            "producer_stream_id": "bkn-backend", "producer_epoch": 1, "producer_sequence": 7,
            "event_type": "object_type.get.observed", "bkn.trace.schema.version": "3.0.0",
            "envelope": {"event": {"event_type": "object_type.get.observed"}, "owner": {"application_principal_id": "bkn-backend", "effective_subject_type": "service", "effective_subject_id": "svc"}},
        }
        self.row = {"source_table": "bkn_backend_trace_outbox", "outbox_id": 17, "status": "pending", "locked_until": None, "envelope": json.dumps({"event": self.event}), **{key: self.event[key] for key in ("event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence")}}
        self.snapshot = "2026-09-25T10:00:00Z"

    def test_classifies_publish_and_encodes_exact_migration_headers(self):
        entry, event = classify_row(self.row, "mig-1", self.snapshot)
        self.assertEqual((entry["classification"], entry["classification_reason"], entry["source_primary_key"]), ("publish", "pending", "17"))
        record = encode_migration_record(entry, event, "bridge#boot-1")
        self.assertEqual(record["key"], "bkn-backend")
        self.assertEqual(record["headers"], {"content-type": "application/json", "bkn-trace-schema-version": "3.0.0", "capture_policy_revision": "0", "producer_instance_id": "bridge#boot-1", "bkn-evidence-record-class": "migration", "bkn-evidence-migration-id": "mig-1"})
        self.assertEqual(json.loads(record["value"]), self.event)

    def test_statuses_classify_without_source_mutation(self):
        for status, classification, reason in (("retry", "publish", "retry"), ("delivered", "verify_delivered", "delivered"), ("conflict", "coverage_gap", "conflict"), ("abandoned", "coverage_gap", "abandoned"), ("dlq", "coverage_gap", "dlq")):
            with self.subTest(status=status):
                entry, event = classify_row(dict(self.row, status=status), "mig-1", self.snapshot)
                self.assertEqual((entry["classification"], entry["classification_reason"]), (classification, reason))
                self.assertEqual(event is None, classification == "coverage_gap")

    def test_expired_lease_publishes_but_active_lease_blocks_snapshot(self):
        entry, _ = classify_row(dict(self.row, status="processing", locked_until="2026-09-25T09:59:59Z"), "mig-1", self.snapshot)
        self.assertEqual((entry["classification"], entry["classification_reason"]), ("publish", "expired_lease"))
        with self.assertRaises(ActiveLeaseError):
            classify_row(dict(self.row, status="processing", locked_until="2026-09-25T10:00:01Z"), "mig-1", self.snapshot)
        with self.assertRaises(ManifestError):
            classify_row(self.row, "mig-1", "not-a-timestamp")

    def test_bad_or_mismatched_payload_becomes_coverage_gap(self):
        entry, event = classify_row(dict(self.row, envelope="{"), "mig-1", self.snapshot)
        self.assertEqual((entry["classification"], entry["classification_reason"], event), ("coverage_gap", "bad_payload", None))
        entry, event = classify_row(dict(self.row, envelope=json.dumps({"event": []})), "mig-1", self.snapshot)
        self.assertEqual((entry["classification"], entry["classification_reason"], event), ("coverage_gap", "bad_payload", None))
        entry, event = classify_row(dict(self.row, event_id="different"), "mig-1", self.snapshot)
        self.assertEqual((entry["classification"], entry["classification_reason"], event), ("coverage_gap", "source_identity_mismatch", None))


if __name__ == "__main__":
    unittest.main()
