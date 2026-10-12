"""Synthetic fixtures for offline native Trace format extraction."""

import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest


def evidence_row():
    event = {
        "bkn.trace.schema.version": "3.0.0", "event_id": "event-1",
        "payload_hash": "a" * 64, "producer_id": "bkn-backend",
        "producer_stream_id": "bkn-backend:boot", "producer_epoch": 1,
        "producer_sequence": 9007199254740993, "envelope": {"number": 7},
        "owner": {"effective_subject": "synthetic-user"},
    }
    row = {key: event[key] for key in (
        "event_id", "payload_hash", "producer_id", "producer_stream_id",
        "producer_epoch", "producer_sequence",
    )}
    row.update({"outbox_id": 1, "source_table": "bkn_backend_trace_outbox",
                "envelope": {"event": event}})
    return row


def span_document():
    return {"index": "synthetic-spans", "id": "doc-1", "routing": "route-1", "_source": {
        "traceId": "a" * 32, "spanId": "b" * 16, "parentSpanId": "0" * 16,
        "startTime": "2026-09-01T00:00:00.123456789Z",
        "endTime": "2026-09-01T00:00:00.123456790Z", "name": "test", "kind": "Client",
        "attributes": {"large": 9007199254740993, "nested": {"value": [True, "test"]}},
        "events": [{"name": "done", "@timestamp": "2026-09-01T00:00:00.123456790Z"}],
        "links": [{"traceId": "c" * 32, "spanId": "d" * 16, "attributes": {"x": 1}}],
        "resource": {"service.name": "synthetic"}, "extra": {"retain": True},
    }}


class TraceConverterTests(unittest.TestCase):
    def setUp(self):
        path = Path(__file__).with_name("trace.py")
        if not path.exists():
            self.fail("Trace format converter has not been implemented")
        spec = importlib.util.spec_from_file_location("historical_trace_converter", path)
        self.converter = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.converter)

    def test_evidence_extracts_body_without_rewriting_identity(self):
        row = evidence_row()
        original = copy.deepcopy(row)
        result = self.converter.convert_evidence(row, "deployment-a")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["kind"], "evidence")
        self.assertEqual(result["payload"], row["envelope"]["event"])
        self.assertEqual(row, original)
        self.assertEqual(result["sidecar"]["validation_required"], "native_ledger")
        self.assertEqual(result, self.converter.convert_evidence(row, "deployment-a"))

    def test_evidence_accepts_json_storage_wrapper(self):
        row = evidence_row()
        row["envelope"] = json.dumps(row["envelope"])
        self.assertEqual(self.converter.convert_evidence(row, "deployment-a")["disposition"], "convert")

    def test_015_minimal_row_keeps_wrapper_owner_outside_event(self):
        row = evidence_row()
        for key in ("producer_id", "producer_stream_id", "producer_epoch", "producer_sequence"):
            del row[key]
        del row["envelope"]["event"]["owner"]
        row["envelope"]["owner"] = {"effective_subject_id": "synthetic-user"}
        row["schema_version"] = "3.0.0"
        result = self.converter.convert_evidence(row, "deployment-a")
        self.assertEqual(result["disposition"], "convert")
        self.assertNotIn("owner", result["payload"])
        self.assertEqual(result["sidecar"]["source"]["envelope"]["owner"], row["envelope"]["owner"])

    def test_evidence_identity_conflicts_are_blocked(self):
        for key, value in (("event_id", "different"), ("payload_hash", "b" * 64),
                           ("producer_sequence", 2), ("producer_epoch", True)):
            with self.subTest(key=key):
                row = evidence_row()
                row[key] = value
                result = self.converter.convert_evidence(row, "deployment-a")
                self.assertEqual(result["disposition"], "blocked")
                self.assertEqual(result["reason"], "source_identity_mismatch")

    def test_evidence_invalid_wrapper_and_schema_cannot_convert(self):
        for value in ("{", {"event": []}, {"event": {"bkn.trace.schema.version": "2.0.0"}}):
            with self.subTest(value=value):
                row = evidence_row()
                row["envelope"] = value
                self.assertNotEqual(self.converter.convert_evidence(row, "deployment-a")["disposition"], "convert")

    def test_span_preserves_complete_native_document_and_nanoseconds(self):
        document = span_document()
        original = copy.deepcopy(document)
        result = self.converter.convert_span(document, "deployment-a")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["payload"], document["_source"])
        self.assertEqual(result["sidecar"]["source_id"], "doc-1")
        self.assertEqual(result["sidecar"]["routing"], "route-1")
        self.assertIn("target_id", result["sidecar"])
        self.assertNotEqual(result["sidecar"]["target_id"], self.converter.convert_span(document, "deployment-b")["sidecar"]["target_id"])
        self.assertEqual(result, self.converter.convert_span(document, "deployment-a"))
        self.assertEqual(document, original)

    def test_metadata_hashes_are_explicit_and_content_sensitive(self):
        document = span_document()
        result = self.converter.convert_span(document, "deployment-a")
        encoded = json.dumps(document["_source"], sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
        self.assertEqual(result["sidecar"]["hash_codec"], "python-json-sorted-v1")
        self.assertEqual(result["sidecar"]["native_sha256"], hashlib.sha256(encoded).hexdigest())
        document["_source"]["extra"]["retain"] = False
        changed = self.converter.convert_span(document, "deployment-a")
        self.assertNotEqual(result["sidecar"]["source_sha256"], changed["sidecar"]["source_sha256"])
        self.assertNotEqual(result["sidecar"]["native_sha256"], changed["sidecar"]["native_sha256"])
        self.assertEqual(result["sidecar"]["target_id"], changed["sidecar"]["target_id"])

    def test_json_text_preserves_large_integer_and_refuses_nonfinite_numbers(self):
        document = span_document()
        document["_source"] = json.dumps(document["_source"])
        result = self.converter.convert_span(document, "deployment-a")
        self.assertEqual(result["payload"]["attributes"]["large"], 9007199254740993)
        document["_source"] = '{"attributes":{"value":NaN}}'
        result = self.converter.convert_span(document, "deployment-a")
        self.assertEqual(result["disposition"], "archive")
        self.assertEqual(result["reason"], "invalid_source_json")

    def test_span_rejects_invalid_ids_and_negative_nanosecond_duration(self):
        for key, value in (("traceId", "0" * 32), ("traceId", "A" * 32),
                           ("spanId", "short"), ("parentSpanId", "z" * 16),
                           ("endTime", "2026-09-01T00:00:00.123456788Z"),
                           ("startTime", 123.0)):
            with self.subTest(key=key):
                document = span_document()
                document["_source"][key] = value
                self.assertNotEqual(self.converter.convert_span(document, "deployment-a")["disposition"], "convert")

    def test_nested_otlp_is_explicitly_blocked_without_official_codec(self):
        document = {"index": "legacy", "id": "one", "_source": {"resourceSpans": []}}
        result = self.converter.convert_span(document, "deployment-a")
        self.assertEqual(result["disposition"], "blocked")
        self.assertEqual(result["reason"], "official_otlp_codec_required")

    def test_span_rejects_duplicate_json_keys_and_invalid_link_ids(self):
        document = span_document()
        document["_source"] = '{"traceId":"a","traceId":"b"}'
        self.assertNotEqual(self.converter.convert_span(document, "deployment-a")["disposition"], "convert")
        document = span_document()
        document["_source"]["links"][0]["spanId"] = "0" * 16
        self.assertNotEqual(self.converter.convert_span(document, "deployment-a")["disposition"], "convert")

    def test_span_rejects_non_native_field_shapes(self):
        for key, value in (("attributes", []), ("resource", {"service.name": 5}),
                           ("instrumentationScope", []), ("status", "Ok"),
                           ("droppedEventsCount", True), ("name", 17)):
            with self.subTest(key=key):
                document = span_document()
                document["_source"][key] = value
                self.assertNotEqual(self.converter.convert_span(document, "deployment-a")["disposition"], "convert")


if __name__ == "__main__":
    unittest.main()
