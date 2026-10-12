import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch, MagicMock
from urllib.error import HTTPError
import apply_spans

from apply_spans import apply as raw_apply, initialize_index as raw_initialize, reconcile as raw_reconcile, OpenSearch
from snapshot import canonical, digest
from test_apply_audit import save
from test_trace import span_document as source_document


def initialize_index(profile):
    return raw_initialize(profile, qualification=True, expected_profile_sha256=digest(canonical(profile).encode()))


def apply(root, checksum, profile, receipt):
    return raw_apply(root, checksum, profile, receipt, qualification=True,
                     expected_plan_sha256=digest((Path(root) / "plan.json").read_bytes()),
                     expected_profile_sha256=digest(canonical(profile).encode()))


def reconcile(root, checksum, profile):
    return raw_reconcile(root, checksum, profile,
                         expected_plan_sha256=digest((Path(root) / "plan.json").read_bytes()),
                         expected_profile_sha256=digest(canonical(profile).encode()))


def span_document():
    document = source_document()
    document["_source"]["attributes"]["nested"]["value"] = [True, False]
    return document


def span_item(document=None, target_id="span-doc-1"):
    document = span_document() if document is None else document
    source = {"kind": "span", "source_id": "collector", "document": document}
    payload = document["_source"]
    checksum = digest(canonical(payload).encode())
    return {"kind": "span", "source_id": "collector", "source": source,
            "source_sha256": digest(canonical(source).encode()), "disposition": "convert",
            "payload": payload, "payload_sha256": checksum, "content_hash": "sha256:" + checksum,
            "target_id": target_id, "sidecar": {"target_id": target_id, "routing": document.get("routing")}}


class FakeIndex:
    def __init__(self):
        self.mapping = None
        self.documents = {}
        self.calls = []
        self.fail_bulk = False

    def request(self, method, path, data=None, ndjson=False):
        self.calls.append((method, path, data))
        if method == "GET" and path == "/bkn-history-test-spans/_mapping":
            if self.mapping is None:
                return 404, {}
            return 200, {"bkn-history-test-spans": {"mappings": self.mapping}}
        if method == "PUT" and path == "/bkn-history-test-spans":
            self.mapping = data["mappings"]
            return 200, {"acknowledged": True}
        if method == "POST" and path == "/bkn-history-test-spans/_bulk?refresh=wait_for":
            lines = [json.loads(line) for line in data.splitlines()]
            replies = []
            for action, payload in zip(lines[::2], lines[1::2]):
                metadata = action["create"]
                key = metadata["_id"], metadata.get("routing")
                status = 400 if self.fail_bulk else 409 if key in self.documents else 201
                if status == 201:
                    self.documents[key] = copy.deepcopy(payload)
                replies.append({"create": {"_id": metadata["_id"], "status": status}})
            return 200, {"items": replies}
        if method == "GET" and "/_doc/" in path:
            from urllib.parse import unquote, urlsplit, parse_qs
            parts = urlsplit(path)
            identity = unquote(parts.path.rsplit("/", 1)[1])
            routing = parse_qs(parts.query).get("routing", [None])[0]
            key = identity, routing
            if key not in self.documents:
                return 404, {"found": False}
            return 200, {"found": True, "_id": identity, "_routing": routing,
                         "_source": copy.deepcopy(self.documents[key])}
        raise AssertionError("unexpected transport request " + method + " " + path)


class SpanWriterTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.profile = {"endpoint": "http://localhost:9200", "index": "bkn-history-test-spans"}
        self.fake = FakeIndex()
        self.patch = patch.object(OpenSearch, "request", side_effect=self.fake.request)
        self.patch.start()
        self.addCleanup(self.patch.stop)

    def test_initialize_new_index_freezes_native_query_mapping(self):
        initialize_index(self.profile)
        mapping = self.fake.mapping
        for field in ("traceId", "spanId", "parentSpanId"):
            self.assertEqual(mapping["properties"][field]["fields"]["keyword"]["type"], "keyword")
        self.assertEqual(mapping["properties"]["startTime"]["type"], "date_nanos")
        self.assertEqual(mapping["properties"]["events"]["properties"]["@timestamp"]["type"], "date_nanos")
        self.assertEqual(mapping["properties"]["@timestamp"]["type"], "date")
        self.assertFalse(mapping["date_detection"])
        self.assertIn("historical_span_mapping_sha256", mapping["_meta"])
        initialize_index(self.profile)
        self.assertEqual(sum(method == "PUT" for method, _, _ in self.fake.calls), 1)

    def test_existing_unconfirmed_index_is_never_modified(self):
        self.fake.mapping = {"properties": {}}
        with self.assertRaisesRegex(ValueError, "mapping"):
            initialize_index(self.profile)
        self.assertFalse(any(method == "PUT" for method, _, _ in self.fake.calls))

    def test_existing_native_mapping_string_boolean_is_equivalent(self):
        initialize_index(self.profile)
        self.fake.mapping["dynamic"] = "true"
        self.fake.mapping["properties"]["attributes"]["dynamic"] = "true"
        initialize_index(self.profile)
        self.assertEqual(sum(method == "PUT" for method, _, _ in self.fake.calls), 1)

    def test_native_omitted_object_type_is_semantically_equivalent_not_nested(self):
        initialize_index(self.profile)
        self.fake.mapping["properties"]["events"].pop("type")
        initialize_index(self.profile)
        self.fake.mapping["properties"]["events"]["type"] = "nested"
        with self.assertRaisesRegex(ValueError, "mapping"):
            initialize_index(self.profile)

    def test_mapping_http_errors_are_transport_not_existing_conflicts(self):
        for status in (302, 502):
            with patch.object(OpenSearch, "request", return_value=(status, {})):
                with self.assertRaisesRegex(ValueError, "transport unavailable"):
                    initialize_index(self.profile)

    def test_full_source_readback_routing_and_large_integer(self):
        value = span_item()
        checksum = save(self.root, [value])
        receipt = apply(self.root, checksum, self.profile, self.root / "receipt.json")
        self.assertEqual(receipt["stage"], "native_span_readback_verified")
        self.assertEqual(receipt["created_count"], 1)
        self.assertEqual(receipt["verified_count"], 1)
        stored = self.fake.documents[(value["target_id"], "route-1")]
        self.assertEqual(stored, value["payload"])
        self.assertEqual(stored["attributes"]["large"], 9007199254740993)
        self.assertEqual((self.root / "receipt.json").stat().st_mode & 0o777, 0o600)
        readback = reconcile(self.root, checksum, self.profile)
        self.assertEqual(readback["verified_count"], 1)

    def test_repeat_creates_zero_new_documents_and_verifies_409(self):
        checksum = save(self.root, [span_item()])
        apply(self.root, checksum, self.profile, self.root / "first.json")
        receipt = apply(self.root, checksum, self.profile, self.root / "second.json")
        self.assertEqual(receipt["created_count"], 0)
        self.assertEqual(receipt["existing_verified_count"], 1)
        self.assertEqual(len(self.fake.documents), 1)

    def test_409_changed_source_is_conflict_not_overwrite(self):
        value = span_item()
        checksum = save(self.root, [value])
        self.fake.documents[(value["target_id"], "route-1")] = {"different": True}
        with self.assertRaises(ValueError):
            apply(self.root, checksum, self.profile, self.root / "conflict.json")
        receipt = json.loads((self.root / "conflict.json").read_text())
        self.assertEqual(receipt["stage"], "span_write_failed_requires_reconciliation")
        self.assertEqual(receipt["verified_count"], 0)
        self.assertEqual(self.fake.documents[(value["target_id"], "route-1")], {"different": True})

    def test_partial_bulk_failure_is_not_completion(self):
        checksum = save(self.root, [span_item()])
        self.fake.fail_bulk = True
        with self.assertRaises(ValueError):
            apply(self.root, checksum, self.profile, self.root / "failure.json")
        self.assertEqual(json.loads((self.root / "failure.json").read_text())["verified_count"], 0)

    def test_cross_document_types_conflict_before_any_http_write(self):
        first = span_document()
        for replacement in ("text", 1.2, {}, True):
            second = copy.deepcopy(first)
            second["_source"]["attributes"]["large"] = replacement
            checksum = save(self.root, [span_item(first), span_item(second, "span-2")])
            with self.subTest(value=replacement), self.assertRaisesRegex(ValueError, "type conflict"):
                apply(self.root, checksum, self.profile, self.root / "unused.json")
            self.assertEqual(self.fake.calls, [])

    def test_date_nanos_out_of_range_refuses_before_index_creation(self):
        for field in ("startTime", "endTime"):
            document = span_document()
            document["_source"][field] = "2500-01-01T00:00:00Z"
            checksum = save(self.root, [span_item(document)])
            with self.assertRaisesRegex(ValueError, "date_nanos"):
                apply(self.root, checksum, self.profile, self.root / "unused.json")
            self.assertEqual(self.fake.calls, [])

    def test_exporter_top_level_zero_timestamp_is_preserved(self):
        document = span_document()
        document["_source"]["@timestamp"] = "0001-01-01T00:00:00Z"
        checksum = save(self.root, [span_item(document)])
        apply(self.root, checksum, self.profile, self.root / "zero.json")
        self.assertEqual(next(iter(self.fake.documents.values()))["@timestamp"], "0001-01-01T00:00:00Z")

    def test_blocked_span_and_hash_changes_refuse_all_writes(self):
        for field, bad in (("disposition", "blocked"), ("payload_sha256", "bad"), ("source_sha256", "bad")):
            value = span_item()
            value[field] = bad
            checksum = save(self.root, [value])
            with self.assertRaises(ValueError):
                apply(self.root, checksum, self.profile, self.root / "unused.json")
            self.assertEqual(self.fake.calls, [])

    def test_reconcile_missing_or_changed_document_is_not_verified(self):
        value = span_item()
        checksum = save(self.root, [value])
        with self.assertRaises(ValueError):
            reconcile(self.root, checksum, self.profile)

    def test_existing_receipt_refuses_resend(self):
        checksum = save(self.root, [span_item()])
        receipt = self.root / "existing.json"
        receipt.write_text("existing")
        with self.assertRaises(ValueError):
            apply(self.root, checksum, self.profile, receipt)
        self.assertEqual(self.fake.calls, [])

    def test_profile_rejects_embedded_credentials_and_invalid_index(self):
        for profile in ({"endpoint": "http://user:secret@localhost:9200", "index": "test"},
                        {"endpoint": "http://localhost:9200", "index": "_all"}):
            with self.assertRaises(ValueError):
                OpenSearch(profile)

    def test_dotted_attribute_leaf_object_conflict_is_preflighted(self):
        first, second = span_document(), span_document()
        first["_source"]["attributes"]["business.name"] = "Example"
        second["_source"]["attributes"]["business"] = 17
        checksum = save(self.root, [span_item(first), span_item(second, "span-2")])
        with self.assertRaisesRegex(ValueError, "type conflict"):
            apply(self.root, checksum, self.profile, self.root / "unused.json")
        self.assertEqual(self.fake.calls, [])

    def test_complete_manifest_and_target_profile_approval_are_required(self):
        checksum = save(self.root, [span_item()])
        with self.assertRaises(ValueError):
            raw_apply(self.root, checksum, self.profile, self.root / "none.json", qualification=True)
        approved = digest((self.root / "plan.json").read_bytes())
        manifest = json.loads((self.root / "plan.json").read_text())
        manifest["validation_time"] = "2025-01-01T00:00:00Z"
        (self.root / "plan.json").write_text(canonical(manifest))
        with self.assertRaises(ValueError):
            raw_apply(self.root, checksum, self.profile, self.root / "changed.json", qualification=True,
                      expected_plan_sha256=approved, expected_profile_sha256=digest(canonical(self.profile).encode()))
        self.assertEqual(self.fake.calls, [])

    def test_release_and_formal_index_writes_are_unqualified(self):
        for profile in (self.profile, {"endpoint": "http://production.example:9200", "index": "bkn-history-test-spans"},
                        {"endpoint": "http://127.0.0.1:9200", "index": "ss4o-traces-default"}):
            with self.assertRaises(ValueError):
                raw_initialize(profile, qualification=profile != self.profile,
                               expected_profile_sha256=digest(canonical(profile).encode()))
        self.assertEqual(self.fake.calls, [])


class SpanTransportTests(unittest.TestCase):
    def test_request_explicitly_disables_proxy_and_redirect_handlers(self):
        response = MagicMock()
        response.status = 200
        response.read.return_value = b'{"ok":true}'
        response.__enter__.return_value = response
        opener = MagicMock()
        opener.open.return_value = response
        with patch("apply_spans.build_opener", create=True, return_value=opener) as builder, \
                patch("apply_spans.urlopen", create=True, side_effect=AssertionError("global environment opener prohibited")):
            client = OpenSearch({"endpoint": "http://127.0.0.1:9200", "index": "test"})
            self.assertEqual(client.request("GET", "/test"), (200, {"ok": True}))
        handlers = builder.call_args.args
        self.assertTrue(any(getattr(handler, "proxies", None) == {} for handler in handlers))
        self.assertTrue(any(isinstance(handler, apply_spans.NoRedirect) for handler in handlers))

    def test_redirect_is_refused_without_forwarding_authentication(self):
        self.assertTrue(hasattr(apply_spans, "NoRedirect"), "redirect-refusal transport missing")
        from urllib.request import Request
        request = Request("http://127.0.0.1:9200/test", headers={"Authorization": "Basic private"})
        with self.assertRaises(HTTPError):
            apply_spans.NoRedirect().redirect_request(request, None, 302, "redirect", {}, "https://external.example/")
        opener = MagicMock()
        opener.open.side_effect = HTTPError(request.full_url, 302, "redirect", {}, None)
        with patch("apply_spans.build_opener", return_value=opener):
            with self.assertRaisesRegex(ValueError, "redirect refused"):
                OpenSearch({"endpoint": "http://127.0.0.1:9200", "index": "test"}).request("GET", "/test")


if __name__ == "__main__":
    unittest.main()
