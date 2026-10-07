import unittest
from datetime import datetime, timezone

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
            return _Response(200, json.dumps({"docs": [{"_id": i, "found": i in self.documents, "_source": self.documents.get(i), "_seq_no": 1, "_primary_term": 1} for i in ids]}).encode())
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
    def test_native_aggregate_create_then_repeat_preserves_ingestion_time(self):
        from opensearch_history import OpenSearchHistoryWriter
        fake = _OpenSearchFake()
        writer = OpenSearchHistoryWriter("http://opensearch", "evidence", opener=fake)
        item = {"_id": "aggregate-1", "document": {"aggregate": True, "events": [{"event_id": "one"}], "ingested_at": "2026-10-06T00:00:00Z"}}
        self.assertEqual(writer.publish_documents([item])["created"], 1)
        item["document"]["ingested_at"] = "2026-10-07T00:00:00Z"
        self.assertEqual(writer.publish_documents([item])["already_verified"], 1)


class OpenSearchReadbackAccountingTests(unittest.TestCase):
    def test_bulk_failure_counts_one_conflict_not_two(self):
        from opensearch_history import OpenSearchHistoryWriter
        fake = _OpenSearchFake()
        def opener(request, timeout):
            if request.full_url.endswith('/_bulk'):
                return _Response(200, json_bytes({"items": [{"create": {"status": 500}}]}))
            return fake(request, timeout)
        result = OpenSearchHistoryWriter("http://opensearch", "logs", opener=opener).publish_documents([{"_id": "history-1", "document": {"value": 1}}])
        self.assertEqual(result, {"created": 0, "updated": 0, "already_verified": 0, "conflict": 1})

    def test_mismatched_readback_is_not_counted_as_created(self):
        from opensearch_history import OpenSearchHistoryWriter
        fake = _OpenSearchFake()
        reads = 0
        def opener(request, timeout):
            nonlocal reads
            response = fake(request, timeout)
            if request.full_url.endswith('/_mget'):
                reads += 1
                if reads == 2:
                    return _Response(200, json_bytes({"docs": [{"found": True, "_source": {"value": 2}}]}))
            return response
        result = OpenSearchHistoryWriter("http://opensearch", "logs", opener=opener).publish_documents([{"_id": "history-1", "document": {"value": 1}}])
        self.assertEqual(result, {"created": 0, "updated": 0, "already_verified": 0, "conflict": 1})

    def test_short_readback_response_fails_instead_of_skipping_rows(self):
        from opensearch_history import OpenSearchHistoryWriter
        writer = OpenSearchHistoryWriter("http://opensearch", "logs", opener=lambda request, timeout: _Response(200, b'{"docs":[]}'))
        with self.assertRaisesRegex(RuntimeError, "readback count mismatch"):
            writer.publish_documents([{"_id": "history-1", "document": {"value": 1}}])

class NativeProjectionWriteTests(unittest.TestCase):
    def test_native_document_conflict_is_preserved_without_overwrite(self):
        from opensearch_history import OpenSearchHistoryWriter
        fake = _OpenSearchFake()
        fake.documents['aggregate-1'] = {'aggregate': True, 'events': ['existing']}
        writer = OpenSearchHistoryWriter('http://opensearch', 'evidence', opener=fake)
        results = {}
        counts = writer.publish_documents([{'_id': 'aggregate-1', 'document': {'aggregate': True, 'events': ['converted']}}],
                                          allow_update=False, on_result=lambda key, state: results.update({key: state}))
        self.assertEqual(counts['conflict'], 1)
        self.assertEqual(fake.documents['aggregate-1']['events'], ['existing'])
        self.assertEqual(results, {'aggregate-1': 'conflict'})

    def test_update_requires_exact_frozen_source_document(self):
        from opensearch_history import OpenSearchHistoryWriter
        import copy
        fake = _OpenSearchFake()
        old={'aggregate':True,'trace_id':'t','effective_subject_id':'u','events':[{'event_id':'e','operation_id':'old'}]}
        fake.documents['a']=copy.deepcopy(old)
        new=copy.deepcopy(old);new['events'][0]['operation_id']='mapped'
        writer=OpenSearchHistoryWriter('http://opensearch','evidence',opener=fake)
        self.assertEqual(writer.publish_documents([{'_id':'a','document':new,'previous_document':old}],allow_update=True)['updated'],1)
        fake.documents['a']['events'][0]['operation_id']='live'
        self.assertEqual(writer.publish_documents([{'_id':'a','document':new,'previous_document':old}],allow_update=True)['conflict'],1)
        self.assertEqual(fake.documents['a']['events'][0]['operation_id'],'live')


    def test_terminal_enrichment_accepts_only_exact_known_converted_baseline(self):
        from opensearch_history import OpenSearchHistoryWriter
        import copy
        fake = _OpenSearchFake()
        old = {"aggregate": True, "trace_id": "t", "effective_subject_id": "u",
               "events": [{"event_id": "e", "operation_id": "mapped", "payload": {"value": 1}}]}
        fake.documents["a"] = copy.deepcopy(old)
        new = copy.deepcopy(old)
        new["events"].append({"event_id": "receipt:r", "event_type": "retrieval.completed"})
        writer = OpenSearchHistoryWriter("http://opensearch", "evidence", opener=fake)
        item = {"_id": "a", "document": new, "previous_documents": [{}, old]}
        self.assertEqual(writer.publish_documents([item], allow_update=True)["updated"], 1)
        self.assertEqual(writer.publish_documents([item], allow_update=True)["already_verified"], 1)
        fake.documents["a"] = copy.deepcopy(old)
        fake.documents["a"]["events"][0]["payload"]["value"] = 2
        self.assertEqual(writer.publish_documents([item], allow_update=True)["conflict"], 1)
        self.assertEqual(fake.documents["a"]["events"][0]["payload"]["value"], 2)
