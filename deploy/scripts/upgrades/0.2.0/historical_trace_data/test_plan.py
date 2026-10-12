import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import plan
import snapshot


class PlanTests(unittest.TestCase):
    def test_validator_result_count_must_match_input(self):
        with patch("plan.subprocess.run") as run:
            run.return_value.returncode = 0
            run.return_value.stdout = ""
            with self.assertRaisesRegex(ValueError, "count"):
                plan.native_validate([{"kind": "audit", "payload": {}}], "validator")

    def test_validator_reply_must_match_its_request(self):
        with patch("plan.subprocess.run") as run:
            run.return_value.returncode = 0
            run.return_value.stdout = '{"accepted":true,"canonical_payload":{"event_id":"other"},"content_hash":"hash"}\n'
            with self.assertRaisesRegex(ValueError, "payload"):
                plan.native_validate([{"kind": "audit", "payload": {"event_id": "one"}}], "validator")

    def test_plan_covers_archive_and_convert_and_is_deterministic(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / "source"
            records = [{"kind": "audit", "source_id": "vega", "row": {"event_id": "one"}},
                       {"kind": "evidence", "source_id": "bkn-backend", "row": {"event_id": "two"}}]
            snapshot.save_snapshot(source, records, "instance", {})
            def audit(*args):
                return {"disposition": "archive", "reason": "missing_http_status", "event": None, "sidecar": {}}
            def evidence(*args):
                return {"disposition": "convert", "kind": "evidence", "payload": {"event_id": "two"}, "sidecar": {}}
            with patch("plan.convert_log", audit), patch("plan.convert_evidence", evidence), patch("plan.native_validate", return_value=[{"accepted": True, "canonical_payload": {"event_id": "two"}, "content_hash": "hash", "reason": "format_only"}]):
                a = plan.create(source, root / "a", "test", "2026-10-06T00:00:00Z", "validator")
                b = plan.create(source, root / "b", "test", "2026-10-06T00:00:00Z", "validator")
            self.assertEqual(a, b)
            self.assertEqual(a["counts"], {"archive": 1, "convert": 1})
            self.assertEqual(plan.verify(root / "a")["counts"], a["counts"])
            self.assertEqual((root / "a" / "items.jsonl").read_bytes(), (root / "b" / "items.jsonl").read_bytes())

    def test_unknown_kind_blocks_instead_of_disappearing(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "source"
            snapshot.save_snapshot(source, [{"kind": "unknown", "source_id": "unregistered", "row": {}}], "instance", {})
            result = plan.create(source, Path(tmp) / "plan", "test", "2026-10-06T00:00:00Z", "validator")
            self.assertEqual(result["counts"], {"blocked": 1})

    def test_modified_plan_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "source"
            snapshot.save_snapshot(source, [], "instance", {})
            output = Path(tmp) / "plan"
            plan.create(source, output, "test", "2026-10-06T00:00:00Z", "validator")
            (output / "items.jsonl").write_text("{}\n")
            with self.assertRaisesRegex(ValueError, "hash"):
                plan.verify(output)

    def test_native_rejection_is_blocked_not_hidden_as_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "source"
            snapshot.save_snapshot(source, [{"kind": "evidence", "source_id": "x", "row": {}}], "instance", {})
            with patch("plan.convert_evidence", return_value={"disposition": "convert", "payload": {}}), patch("plan.native_validate", return_value=[{"accepted": False, "reason": "invalid_hash"}]):
                result = plan.create(source, Path(tmp) / "out", "test", "2026-10-06T00:00:00Z", "validator")
            self.assertEqual(result["counts"], {"blocked": 1})

    def test_nested_span_counts_documents_separately_from_expansion(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "source"
            record = {"kind": "span", "source_id": "otel", "document": {"index": "old", "id": "doc", "_source": {"resourceSpans": []}}}
            snapshot.save_snapshot(source, [record], "instance", {})
            expanded = [{"disposition": "convert", "payload": {"traceId": "a" * 32, "spanId": str(n) * 16}, "sidecar": {"target_id": "target" + str(n)}} for n in (1, 2)]
            replies = [{"accepted": True, "canonical_payload": item["payload"], "content_hash": str(n), "reason": "format_only"} for n, item in enumerate(expanded)]
            with patch("plan.convert_span", return_value={"disposition": "blocked", "reason": "official_otlp_codec_required"}), patch("plan.expand_otlp", return_value=expanded), patch("plan.native_validate", return_value=replies):
                result = plan.create(source, Path(tmp) / "out", "test", "2026-10-06T00:00:00Z", "validator", span_codec="codec")
            self.assertEqual(result["record_count"], 1)
            self.assertEqual(result["item_count"], 2)
            self.assertEqual(result["counts"], {"convert": 2})
            self.assertEqual(plan.verify(Path(tmp) / "out")["item_count"], 2)

    def test_conflicting_identity_blocks_all_later_copies(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "source"
            snapshot.save_snapshot(source, [{"kind": "evidence", "source_id": "x", "row": {"ordinal": n}} for n in range(3)], "instance", {})
            responses = [{"accepted": True, "event_id": "same", "canonical_payload": {"event_id": "same"}, "content_hash": h, "reason": "format_only"} for h in ("A", "B", "A")]
            with patch("plan.convert_evidence", return_value={"disposition": "convert", "payload": {}}), patch("plan.native_validate", return_value=responses):
                result = plan.create(source, Path(tmp) / "out", "test", "2026-10-06T00:00:00Z", "validator")
            self.assertEqual(result["counts"], {"blocked": 3})


if __name__ == "__main__":
    unittest.main()
