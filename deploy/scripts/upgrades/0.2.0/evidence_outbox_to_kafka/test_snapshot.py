import unittest

from snapshot import read_event_snapshot


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
    def test_reads_only_frozen_event_rows_in_stable_source_order(self):
        connection = Connection()
        rows = read_event_snapshot(connection, "2026-09-25T10:00:00Z")
        self.assertEqual([(row["source_table"], row["outbox_id"]) for row in rows], [("bkn_backend_trace_outbox", 2), ("ontology_query_trace_outbox", 1)])
        self.assertEqual(len(connection.calls), 2)
        for query, args in connection.calls:
            self.assertIn("updated_at <= %s", query)
            self.assertIn("ORDER BY outbox_id ASC", query)
            self.assertEqual(args, ("2026-09-25T10:00:00Z",))


if __name__ == "__main__":
    unittest.main()
