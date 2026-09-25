import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from reconcile import reconcile


class ReconcileTest(unittest.TestCase):
    def test_reconciler_is_sole_writer_for_non_publish_results(self):
        entries = [
            {"entry_id": "p", "classification": "publish", "event_id": "p", "payload_hash": "p", "classification_reason": "pending"},
            {"entry_id": "d", "classification": "verify_delivered", "event_id": "d", "payload_hash": "hash", "classification_reason": "already_delivered"},
            {"entry_id": "g", "classification": "coverage_gap", "event_id": None, "payload_hash": None, "classification_reason": "bad_payload"},
        ]
        self.assertEqual(reconcile(entries, {"d": {"payload_hash": "hash"}}), [
            {"entry_id": "d", "adjudication": "verified_delivered", "reason_code": "already_delivered"},
            {"entry_id": "g", "adjudication": "coverage_gap", "reason_code": "bad_payload"},
        ])
        self.assertEqual(reconcile(entries, {}), [{"entry_id": "g", "adjudication": "coverage_gap", "reason_code": "bad_payload"}])


if __name__ == "__main__":
    unittest.main()
