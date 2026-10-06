import unittest
from datetime import datetime, timezone

from opensearch_history import (document_from_event, document_from_legacy_audit,
                                evidence_document_from_legacy_row, stable_log_id)


class OpenSearchHistoryDocumentTests(unittest.TestCase):
    def test_audit_event_maps_to_queryable_ss4o_document_without_inventing_trace(self):
        event = {
            "schema_version": "1.0", "event_id": "event-1", "source_id": "vega",
            "category": "audit.admin", "event_name": "vega.operation.observed",
            "occurred_at": "2026-09-12T21:25:44.168466Z",
            "actor": {"id": "user-1", "type": "user", "auth_method": "oauth", "effective_subject": "user-1", "display_name_snapshot": "Administrator"},
            "target": {"type": "catalog", "id": "catalog:1", "name": "worldcup_mysql_vega"},
            "outcome": "failure", "scope": {"business_module": "data_resource_knowledge_network", "environment": "test", "platform_scope": True, "knowledge_network_ids": []},
            "request_context": {"source_channel": "api", "transport": "http", "method": "POST"},
            "correlation": {"request_id": "request-1"}, "summary": "vega.operation.observed create catalog", "http_status": 400, "failure_code": "HTTP_400",
        }
        document = document_from_event(event, source_log_id="event-1", observed_at=datetime(2026, 10, 6, 12, tzinfo=timezone.utc))
        self.assertEqual(document["_id"], stable_log_id("vega", "event-1"))
        value = document["document"]
        self.assertEqual(value["@timestamp"], event["occurred_at"])
        self.assertEqual(value["observedTimestamp"], "2026-10-06T12:00:00Z")
        self.assertEqual(value["attributes"]["log_category"], "audit.admin")
        self.assertEqual(value["attributes"]["event_name"], "vega.operation.observed")
        self.assertEqual(value["attributes"]["source_log_id"], "event-1")
        self.assertEqual(value["attributes"]["target_name"], "worldcup_mysql_vega")
        self.assertEqual(value["attributes"]["http_status"], 400)
        self.assertEqual(value["resource"]["service"]["name"], "vega")
        self.assertNotIn("traceId", value)
        self.assertNotIn("spanId", value)

    def test_log_id_is_stable_for_same_source_identity(self):
        self.assertEqual(stable_log_id("vega", "event-1"), stable_log_id("vega", "event-1"))
        self.assertNotEqual(stable_log_id("vega", "event-1"), stable_log_id("bkn-backend", "event-1"))

    def test_legacy_audit_row_is_migrated_even_when_online_contract_would_reject_it(self):
        item = document_from_legacy_audit({"source_id": "execution-factory", "row": {
            "event_id": "evt-legacy", "event_time": "2026-09-02T10:43:29.255720Z",
            "action": "create", "outcome": "success", "actor_id": "u",
            "actor_name": "Administrator", "target_type": "tool", "target_id": "tool-1",
            "target_name": "tool-1", "request_id": "req-1",
        }}, datetime(2026, 10, 6, tzinfo=timezone.utc), "production")
        self.assertEqual(item["document"]["resource"]["deployment"]["environment"], "production")
        self.assertEqual(item["document"]["attributes"]["source_log_id"], "evt-legacy")
        self.assertEqual(item["document"]["attributes"]["target_name"], "tool-1")
        self.assertEqual(item["document"]["attributes"]["actor_name_snapshot"], "Administrator")
        self.assertEqual(item["document"]["attributes"]["business_module_id"], "execution_factory")
        self.assertEqual(item["document"]["attributes"]["source_channel"], "api")

    def test_legacy_evidence_row_maps_to_native_evidence_document(self):
        row = {"event_id": "evt-evidence", "created_at": "2026-09-02T10:43:29.255720Z",
               "envelope": json_bytes({"event": {"event_id": "evt-evidence",
               "event_type": "knowledge.read.observed", "trace_id": "a" * 32,
               "span_id": "b" * 16, "request_id": "req-1", "producer_id": "bkn-backend",
               "producer_epoch": 1, "producer_sequence": 1, "observed_at": "2026-09-02T10:43:29.255720Z",
               "envelope": {"event": {"event_type": "knowledge.read.observed", "payload": {"kn_id": "kn-1"}}}}}).decode()}
        item = evidence_document_from_legacy_row({"source_id": "bkn-backend", "row": row}, datetime(2026, 10, 6, tzinfo=timezone.utc))
        self.assertNotIn("aggregate", item["document"])
        self.assertEqual(item["document"]["trace_id"], "a" * 32)
        self.assertEqual(item["document"]["knowledge_network_ids"], ["kn-1"])


if __name__ == "__main__":
    unittest.main()

class _Response:
    def __init__(self, status, body):
        self.status = status
        self._body = body
    def read(self):
        return self._body
    def __enter__(self):
        return self
    def __exit__(self, *args):
        return False


class _OpenSearchFake:
    def __init__(self):
        self.documents = {}
    def __call__(self, request, timeout):
        import json
        path = request.full_url.split("/", 3)[-1]
        if request.method == "POST" and path == "_bulk":
            lines = request.data.decode().splitlines()
            response = []
            for i in range(0, len(lines), 2):
                action_name, action = next(iter(json.loads(lines[i]).items()))
                doc_id = action["_id"]
                if action_name == "create" and doc_id in self.documents:
                    response.append({"create": {"status": 409}})
                else:
                    self.documents[doc_id] = json.loads(lines[i + 1])
                    response.append({action_name: {"status": 201}})
            return _Response(200, json.dumps({"items": response}).encode())
        if request.method == "POST" and path.endswith("/_mget"):
            ids = [item["_id"] for item in json.loads(request.data)["docs"]]
            return _Response(200, json.dumps({"docs": [{"_id": i, "found": i in self.documents, "_source": self.documents.get(i)} for i in ids]}).encode())
        parts = path.split("/")
        from urllib.parse import unquote
        log_id = unquote(parts[-1])
        if request.method == "GET":
            if log_id not in self.documents:
                return _Response(404, b"{}")
            return _Response(200, json_bytes({"_source": self.documents[log_id]}))
        self.documents[log_id] = json_loads(request.data)
        return _Response(201, b"{}")


def json_bytes(value):
    import json
    return json.dumps(value).encode()


def json_loads(value):
    import json
    return json.loads(value)


class OpenSearchHistoryWriterTests(unittest.TestCase):
    def test_publish_creates_then_rerun_verifies_existing_document(self):
        from opensearch_history import OpenSearchHistoryWriter
        fake = _OpenSearchFake()
        event = {"event_id": "event-1", "source_id": "vega", "category": "audit.admin", "event_name": "vega.operation.observed", "occurred_at": "2026-09-12T21:25:44Z", "actor": {"id": "u", "effective_subject": "u", "display_name_snapshot": "U"}, "target": {"type": "catalog", "id": "c", "name": "C"}, "outcome": "success", "scope": {"business_module": "data", "environment": "test", "platform_scope": True}, "request_context": {"method": "POST"}, "summary": "summary"}
        writer = OpenSearchHistoryWriter("http://opensearch", "logs", opener=fake)
        self.assertEqual(writer.publish([event], datetime(2026, 10, 6, tzinfo=timezone.utc))["created"], 1)
        self.assertEqual(writer.publish([event], datetime(2026, 10, 7, tzinfo=timezone.utc))["already_verified"], 1)


if __name__ == "__main__":
    unittest.main()
