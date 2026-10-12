import copy
import unittest
from test_native_evidence import record
from native_core import plan_core

class NativeCoreConversionTests(unittest.TestCase):
    def test_two_requests_preserve_native_trace_identity_and_have_usable_receipts(self):
        first, second = record('one'), record('two')
        for r, req in ((first,'req_1'),(second,'req_2')):
            e=r['row']['envelope']['event'];e['request_id']=req;e['conversation_id']='conv_'+req
            e['interaction_id']='int_shared';e['operation_id']='op_'+'f'*64;e['attempt']=1
            e['started_at']=e['envelope']['event']['observed_at']
            e['envelope']['event'].update({'bkn.request.id':req,'interaction_id':'int_shared','operation_id':e['operation_id'],'bkn.operation.name':'bkn.metric.get'})
        original=copy.deepcopy([first,second])
        plan=plan_core([first,second],{'u1':'Alice'})
        self.assertEqual(len(plan['receipts']),2)
        self.assertEqual({r['trace_id'] for r in plan['receipts']},{'a'*32})
        self.assertEqual({m['source']['trace_id'] for m in plan['source_map']},{'a'*32})
        self.assertEqual({r['request_id'] for r in plan['receipts']},{'req_1','req_2'})
        self.assertEqual(len({r['interaction_id'] for r in plan['receipts']}),2)
        self.assertTrue(all(len(o['operation_id'])<=64 for o in plan['operations']))
        self.assertTrue(all(r['receipt_status']=='completed' for r in plan['receipts']))
        self.assertEqual(plan['conversations'][0]['actor_name_snapshot'],'Alice')
        self.assertEqual(len(plan['source_map']),2)
        self.assertEqual([first,second],original)
        reversed_plan=plan_core([second,first],{'u1':'Alice'})
        for key in ('conversations','interactions','operations','receipts','call_facts'):
            self.assertEqual(plan[key],reversed_plan[key])

    def test_every_converted_request_remains_in_shared_trace_events(self):
        from native_evidence import plan_aggregates
        from datetime import datetime, timezone
        rows = [record('one'), record('two')]
        for index, r in enumerate(rows):
            outer=r['row']['envelope']['event']
            outer.update(request_id='request_'+str(index), conversation_id='conversation_'+str(index), interaction_id='interaction_'+str(index), operation_id='operation_'+str(index))
            outer['envelope']['event']['bkn.request.id']=outer['request_id']
        plan=plan_core(rows)
        aggregates,rejected=plan_aggregates(plan['records'],datetime.now(timezone.utc))
        self.assertEqual(rejected,{})
        self.assertEqual(len(aggregates), 1)
        events = aggregates[0]['document']['events']
        self.assertEqual({event['bkn.request.id'] for event in events}, {'request_0', 'request_1'})
        for receipt in plan['receipts']:
            self.assertEqual(aggregates[0]['document']['trace_id'], receipt['trace_id'])

    def test_call_facts_preserve_span_identity_from_inner_evidence_event(self):
        source = record('span-linked')
        outer = source['row']['envelope']['event']
        outer.update(conversation_id='c', interaction_id='i', operation_id='o')
        outer['envelope']['event']['span_id'] = '1234567890abcdef'
        plan = plan_core([source])
        self.assertEqual(plan['call_facts'][0]['span_id'], '1234567890abcdef')

    def test_reused_operation_across_traces_keeps_both_receipts(self):
        first,second=record('one'),record('two',trace_id='b'*32)
        for r in (first,second):
            r['row']['envelope']['event'].update(conversation_id='c',interaction_id='i',operation_id='o')
        plan=plan_core([first,second])
        self.assertEqual(len(plan['receipts']),2)
        self.assertEqual({r['trace_id'] for r in plan['receipts']},{'a'*32,'b'*32})
        self.assertEqual(len(plan['operations']),2)

    def test_distinct_requests_preserve_original_trace_and_span_connections(self):
        from native_evidence import plan_aggregates
        from datetime import datetime, timezone
        rows = [record('one'), record('two')]
        for index, source in enumerate(rows):
            outer = source['row']['envelope']['event']
            outer.update(request_id='req_' + str(index), conversation_id='conv_' + str(index),
                         interaction_id='int_' + str(index), operation_id='op_' + str(index),
                         span_id='1234567890abcdef')
            outer['envelope']['event'].update({'bkn.request.id': outer['request_id'],
                                               'span_id': outer['span_id']})
        plan = plan_core(rows)
        self.assertEqual({receipt['trace_id'] for receipt in plan['receipts']}, {'a' * 32})
        self.assertEqual({fact['span_id'] for fact in plan['call_facts']}, {'1234567890abcdef'})
        aggregates, rejected = plan_aggregates(plan['records'], datetime.now(timezone.utc), receipts=plan['receipts'])
        self.assertEqual(rejected, {})
        self.assertEqual(len(aggregates), 1)
        events = aggregates[0]['document']['events']
        self.assertEqual({event['bkn.request.id'] for event in events}, {'req_0', 'req_1'})
        self.assertEqual({event['trace_id'] for event in events}, {'a' * 32})

    def test_external_parent_operation_reference_is_preserved(self):
        source = record('child')
        outer = source['row']['envelope']['event']
        outer.update(conversation_id='c', interaction_id='i', operation_id='child')
        outer['envelope']['event']['parent_operation_id'] = 'original_mcp_parent'
        plan = plan_core([source])
        self.assertEqual(plan['call_facts'][0].get('parent_operation_id'), 'original_mcp_parent')
        self.assertEqual(plan['source_map'][0]['source']['parent_operation_id'], 'original_mcp_parent')

    def test_parent_reference_uses_the_same_remapping_as_parent_operation(self):
        parent, child = record('parent'), record('child')
        long_id = 'op_' + 'f' * 64
        for source, oid in ((parent, long_id), (child, 'child')):
            source['row']['envelope']['event'].update(conversation_id='c', interaction_id='i', operation_id=oid)
        child['row']['envelope']['event']['envelope']['event']['parent_operation_id'] = long_id
        plan = plan_core([parent, child])
        mapped_parent = plan['source_map'][0]['target']['operation_id']
        fact = next(f for f in plan['call_facts'] if f['operation_id'] == 'child')
        self.assertNotEqual(mapped_parent, long_id)
        self.assertEqual(fact.get('parent_operation_id'), mapped_parent)

class CoreTransportBatchTests(unittest.TestCase):
    def plan(self, count=3):
        rows = [record(str(i), trace_id=format(i + 1, '032x')) for i in range(count)]
        for i, row in enumerate(rows):
            row['row']['envelope']['event'].update(conversation_id='c', interaction_id='i', operation_id='op_' + str(i))
        return plan_core(rows)

    def test_batches_keep_dependencies_and_every_receipt_once(self):
        from native_core import core_import_batches
        from snapshot import strict_loads
        plan = self.plan()
        batches = [strict_loads(body) for body in core_import_batches(plan, max_receipts=1)]
        self.assertEqual(len(batches), 3)
        self.assertEqual({r['receipt_id'] for batch in batches for r in batch['receipts']},
                         {r['receipt_id'] for r in plan['receipts']})
        for batch in batches:
            self.assertEqual([len(batch[key]) for key in ('conversations', 'interactions', 'operations', 'receipts', 'call_facts')], [1] * 5)
            self.assertEqual(batch['call_facts'][0]['receipt_id'], batch['receipts'][0]['receipt_id'])
            self.assertEqual(batch['operations'][0]['operation_id'], batch['receipts'][0]['operation_id'])

    def test_transport_size_bound_splits_and_oversized_unit_fails_preflight(self):
        from native_core import core_import_batches
        plan = self.plan()
        single = list(core_import_batches(plan, max_receipts=1))
        limit = max(map(len, single))
        batches = list(core_import_batches(plan, max_bytes=limit))
        self.assertEqual(len(batches), 3)
        self.assertTrue(all(len(body) <= limit for body in batches))
        with self.assertRaisesRegex(ValueError, 'native_core_record_exceeds_transport_limit'):
            list(core_import_batches(plan, max_bytes=min(map(len, single)) - 1))
