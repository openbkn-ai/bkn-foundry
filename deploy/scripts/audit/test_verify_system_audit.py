# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
import unittest
from unittest.mock import patch
import verify_system_audit as verifier


class VerificationTest(unittest.TestCase):
    source = (200, {"data": [{"source_id": "audit-ledger", "status": "healthy"}]})
    record = {"event_id": "event-1", "source_id": "agent-observability", "log_category": "audit.admin"}

    def verify(self):
        return verifier.verify("https://example.invalid", "private-token", "event-1",
                               "agent-observability", "2026-10-09T00:00:00Z", "2026-10-09T01:00:00Z")

    def test_exact_record_passes_using_get_only(self):
        with patch.object(verifier, "read_json", side_effect=[self.source, (200, {"data": [self.record]})]) as read:
            self.assertTrue(self.verify()["expected_event_visible"])
            self.assertEqual(read.call_count, 2)
            self.assertIn("categories=audit.admin", read.call_args.args[1])

    def test_empty_or_wrong_identity_cannot_prove_collection(self):
        for records in ([], [dict(self.record, event_id="other")], [dict(self.record, source_id="other")],
                        [dict(self.record, log_category="runtime.business")]):
            with self.subTest(records=records), patch.object(verifier, "read_json", side_effect=[self.source, (200, {"data": records})]):
                with self.assertRaises(ValueError):
                    self.verify()

    def test_unconfigured_or_denied_source_fails(self):
        for source in ((403, {}), (503, {}), (200, {"data": []}),
                       (200, {"data": [{"source_id": "audit-ledger", "status": "not_integrated"}]})):
            with self.subTest(source=source), patch.object(verifier, "read_json", return_value=source):
                with self.assertRaises(ValueError):
                    self.verify()

    def test_denied_record_query_fails(self):
        with patch.object(verifier, "read_json", side_effect=[self.source, (403, {})]):
            with self.assertRaises(ValueError):
                self.verify()

    def test_token_not_forwarded_on_redirect(self):
        self.assertIsNone(verifier.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.invalid"))

    def test_missing_token_or_credential_url_never_sends(self):
        with patch.object(verifier, "read_json") as read:
            for url, token in (("https://example.invalid", ""), ("https://user:secret@example.invalid", "private-token")):
                with self.assertRaises(ValueError):
                    verifier.verify(url, token, "event-1", "agent-observability", "start", "end")
            read.assert_not_called()


if __name__ == "__main__":
    unittest.main()
