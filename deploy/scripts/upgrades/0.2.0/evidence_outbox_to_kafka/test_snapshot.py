import json
import unittest

from manifest import entries_digest
from snapshot import issue_manifest, read_event_snapshot, verify_frozen_entries
from source import classify_row


class Cursor:
    description = [(name,) for name in ("outbox_id", "event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence", "envelope", "status", "locked_until")]

    def __init__(self, table, calls):
        self.table, self.calls = table, calls

    def execute(self, query, args):
        self.calls.append((query, args))

    def fetchall(self):
        if self.table == 0:
            return [(2, "evt-2", "h", "bkn-backend", "bkn-backend", 1, 2, "{}", "pending", None)]
        return [(1, "evt-1", "h", "ontology-query", "ontology-query", 1, 1, "{}", "delivered", None)]

    def close(self):
        pass


class Connection:
    def __init__(self):
        self.calls, self.index = [], 0

    def cursor(self):
        cursor = Cursor(self.index, self.calls)
        self.index += 1
        return cursor


class SnapshotTest(unittest.TestCase):
    def test_issue_manifest_keeps_event_payload_out_of_frozen_artifact(self):
        event = {
            "event_id": "evt-1", "payload_hash": "a" * 64,
            "producer_id": "bkn-backend", "producer_stream_id": "bkn-backend",
            "producer_epoch": 1, "producer_sequence": 1,
            "envelope": {"sensitive": "must stay source-only"},
        }
        row = {
            "source_table": "bkn_backend_trace_outbox", "outbox_id": 1,
            "status": "pending", "locked_until": None,
            "envelope": json.dumps({"event": event}), **{key: event[key] for key in (
                "event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence",
            )},
        }
        artifact, events = issue_manifest("mig-1", "2026-09-25T10:00:00.000Z", [row])
        self.assertEqual(artifact["entry_count"], "1")
        self.assertEqual(artifact["entries"][0]["classification"], "publish")
        self.assertNotIn("envelope", json.dumps(artifact))
        self.assertNotIn("sensitive", json.dumps(artifact))
        self.assertEqual(events[("bkn_backend_trace_outbox", "1")], event)

    def test_reads_only_frozen_event_rows_in_stable_source_order(self):
        connection = Connection()
        rows = read_event_snapshot(connection, "2026-09-25T10:00:00Z")
        self.assertEqual([(row["source_table"], row["outbox_id"]) for row in rows], [("bkn_backend_trace_outbox", 2), ("ontology_query_trace_outbox", 1)])
        self.assertEqual(len(connection.calls), 2)
        for query, args in connection.calls:
            self.assertIn("updated_at <= %s", query)
            self.assertIn("ORDER BY outbox_id ASC", query)
            self.assertEqual(args, ("2026-09-25T10:00:00Z",))

    def test_reread_must_exactly_match_payload_free_frozen_entries(self):
        event = {"event_id": "evt-1", "payload_hash": "a" * 64, "producer_id": "bkn-backend", "producer_stream_id": "bkn-backend", "producer_epoch": 1, "producer_sequence": 1}
        row = {"source_table": "bkn_backend_trace_outbox", "outbox_id": 1, "status": "pending", "locked_until": None, "envelope": json.dumps({"event": event}), **event}
        manifest = {"manifest_id": "mig-1", "source_snapshot_at": "2026-09-25T10:00:00Z"}
        entry, _ = classify_row(row, manifest["manifest_id"], manifest["source_snapshot_at"])
        manifest.update(entry_count="1", entries_digest=entries_digest([entry]))
        self.assertEqual(verify_frozen_entries([row], manifest, [entry]), {("bkn_backend_trace_outbox", "1"): event})
        with self.assertRaisesRegex(Exception, "does not match"):
            verify_frozen_entries([dict(row, status="retry")], manifest, [entry])


if __name__ == "__main__":
    unittest.main()
