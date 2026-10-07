import copy
import hashlib
import unittest
from datetime import datetime, timezone

from native_evidence import plan_aggregates


def record(event_id='e1', trace_id='a' * 32, owner='u1'):
    return {'kind': 'evidence', 'source_id': 'backend', 'row': {'event_id': event_id, 'envelope': {
        'event': {'event_id': event_id, 'trace_id': trace_id, 'request_id': 'req_1', 'conversation_id': 'conv_1',
                  'envelope': {'owner': {'effective_subject_id': owner, 'effective_subject_type': 'user',
                                         'application_principal_id': 'backend'},
                               'event': {'event_id': event_id, 'trace_id': trace_id, 'bkn.request.id': 'req_1',
                                         'event_type': 'knowledge.read.observed', 'bkn.trace.schema.version': '2.1.0',
                                         'observed_at': '2026-09-02T01:00:00.123456789Z',
                                         'emitted_at': '2026-09-02T01:00:00.123456789Z',
                                         'payload': {'business_refs': [{'ref_id': 'object:kn1:obj1'}]}}}}}}}


class NativeAggregateTests(unittest.TestCase):
    def test_native_id_schema_and_merged_events_preserve_source_without_receipt_invention(self):
        rows = [record('e2'), record('e1')]
        before = copy.deepcopy(rows)
        items, rejected = plan_aggregates(rows, datetime(2026, 10, 7, tzinfo=timezone.utc))
        self.assertEqual(rejected, {})
        self.assertEqual(len(items), 1)
        item = items[0]
        expected = 'aggregate-' + hashlib.sha256(('trace-aggregate\x00' + 'a' * 32).encode()).hexdigest()
        self.assertEqual(item['_id'], expected)
        doc = item['document']
        self.assertTrue(doc['aggregate'])
        self.assertEqual(doc['bkn.trace.schema.version'], '2.1.0')
        self.assertEqual([e['event_id'] for e in doc['events']], ['e1', 'e2'])
        self.assertEqual(doc['accepted_event_count'], 2)
        self.assertEqual(doc['business_ref_count'], 2)
        self.assertEqual(doc['knowledge_network_ids'], ['kn1'])
        self.assertNotIn('receipt_status', doc)
        self.assertEqual(rows, before)
        self.assertEqual(plan_aggregates(list(reversed(rows)), datetime(2026, 10, 7, tzinfo=timezone.utc))[0], items)

    def test_conflicting_owner_rejects_affected_trace_without_combining_records(self):
        items, rejected = plan_aggregates([record('e1'), record('e2', owner='other')], datetime.now(timezone.utc))
        self.assertEqual(items, [])
        self.assertEqual(list(rejected.values()), ['trace_owner_conflict', 'trace_owner_conflict'])

    def test_duplicate_identity_with_different_payload_is_a_conversion_conflict(self):
        other = record()
        other['row']['envelope']['event']['envelope']['event']['payload']['different'] = True
        items, rejected = plan_aggregates([record(), other], datetime.now(timezone.utc))
        self.assertEqual(items, [])
        self.assertEqual(list(rejected.values()), ['event_content_conflict', 'event_content_conflict'])

    def test_missing_owner_is_loss_not_a_synthetic_principal(self):
        row = record()
        row['row']['envelope']['event']['envelope']['owner'] = {}
        items, rejected = plan_aggregates([row], datetime.now(timezone.utc))
        self.assertEqual(items, [])
        self.assertEqual(list(rejected.values()), ['missing_evidence_owner'])

    def test_malformed_event_does_not_block_an_independent_trace(self):
        bad = record('broken', trace_id='b' * 32)
        del bad['row']['envelope']['event']['envelope']['event']['event_type']
        items, rejected = plan_aggregates([record(), bad], datetime.now(timezone.utc))
        self.assertEqual(len(items), 1)
        self.assertEqual(rejected, {1: 'missing_evidence_projection_fields'})

    def test_null_ref_arrays_preserve_payload_and_match_native_zero_counts(self):
        row = record()
        payload = row['row']['envelope']['event']['envelope']['event']['payload']
        payload['business_refs'] = None
        items, rejected = plan_aggregates([row], datetime.now(timezone.utc))
        self.assertEqual(rejected, {})
        self.assertEqual(items[0]['document']['business_ref_count'], 0)
        self.assertIsNone(items[0]['document']['events'][0]['payload']['business_refs'])

    def test_non_object_owner_is_an_independent_loss(self):
        bad = record('broken', trace_id='b' * 32)
        bad['row']['envelope']['event']['envelope']['owner'] = 'text'
        items, rejected = plan_aggregates([record(), bad], datetime.now(timezone.utc))
        self.assertEqual(len(items), 1)
        self.assertEqual(rejected, {1: 'missing_evidence_owner'})
