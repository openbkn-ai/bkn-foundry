import copy
import unittest
from test_native_evidence import record
from native_core import plan_core

class NativeCoreConversionTests(unittest.TestCase):
    def test_two_requests_split_native_trace_contexts_and_have_usable_receipts(self):
        first, second = record('one'), record('two')
        for r, req in ((first,'req_1'),(second,'req_2')):
            e=r['row']['envelope']['event'];e['request_id']=req;e['conversation_id']='conv_'+req
            e['interaction_id']='int_shared';e['operation_id']='op_'+'f'*64;e['attempt']=1
            e['started_at']=e['envelope']['event']['observed_at']
            e['envelope']['event'].update({'bkn.request.id':req,'interaction_id':'int_shared','operation_id':e['operation_id'],'bkn.operation.name':'bkn.metric.get'})
        original=copy.deepcopy([first,second])
        plan=plan_core([first,second],{'u1':'Alice'})
        self.assertEqual(len(plan['receipts']),2)
        self.assertEqual(len({r['trace_id'] for r in plan['receipts']}),2)
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

    def test_every_converted_request_is_an_aggregate_root(self):
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
        roots={item['document']['bkn.request.id']:item['document']['trace_id'] for item in aggregates}
        self.assertEqual(set(roots),{'request_0','request_1'})
        for receipt in plan['receipts']:
            self.assertEqual(roots[receipt['request_id']],receipt['trace_id'])

    def test_reused_operation_across_traces_keeps_both_receipts(self):
        first,second=record('one'),record('two',trace_id='b'*32)
        for r in (first,second):
            r['row']['envelope']['event'].update(conversation_id='c',interaction_id='i',operation_id='o')
        plan=plan_core([first,second])
        self.assertEqual(len(plan['receipts']),2)
        self.assertEqual({r['trace_id'] for r in plan['receipts']},{'a'*32,'b'*32})
        self.assertEqual(len(plan['operations']),2)

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
