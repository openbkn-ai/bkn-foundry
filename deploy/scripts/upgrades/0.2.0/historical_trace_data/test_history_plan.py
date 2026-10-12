import unittest

from history_plan import map_evidence_records


class EvidenceMappingTests(unittest.TestCase):
    def test_mapping_updates_references_without_creating_executions(self):
        original = {'row': {'envelope': {'event': {
            'event_id': 'event-one', 'conversation_id': 'conv_req_old',
            'interaction_id': 'old-i', 'operation_id': 'old-o',
            'trace_id': 'trace-one', 'request_id': 'request-one',
            'envelope': {'event': {'event_id': 'event-one', 'operation_id': 'old-o',
                                  'interaction_id': 'old-i', 'trace_id': 'trace-one'}}}}}}
        mapping = [{'source_ordinal': 0, 'status': 'matched', 'target': {
            'conversation_id': 'conv-original', 'interaction_id': 'int-original',
            'operation_id': 'op-original', 'receipt_id': 'receipt-original', 'attempt': 2}}]
        result = map_evidence_records([original], mapping)
        outer = result[0]['row']['envelope']['event']
        self.assertEqual(outer['conversation_id'], 'conv-original')
        self.assertEqual(outer['operation_id'], 'op-original')
        self.assertEqual(outer['attempt'], 2)
        self.assertEqual(outer['envelope']['event']['interaction_id'], 'int-original')
        self.assertEqual(outer['trace_id'], 'trace-one')
        self.assertEqual(original['row']['envelope']['event']['operation_id'], 'old-o')

    def test_unlinked_observation_keeps_original_identity(self):
        original = {'row': {'envelope': {'event': {'conversation_id': 'conv_req_source'}}}}
        result = map_evidence_records([original], [{'source_ordinal': 0, 'status': 'unlinked'}])
        self.assertEqual(result, [original])

    def test_interaction_only_mapping_does_not_invent_operation(self):
        original = {'row': {'envelope': {'event': {'operation_id': 'observed-operation',
                    'envelope': {'event': {'operation_id': 'observed-operation'}}}}}}
        result = map_evidence_records([original], [{'source_ordinal': 0, 'status': 'matched',
            'target': {'conversation_id': 'conv-one', 'interaction_id': 'int-one',
                       'operation_id': '', 'receipt_id': '', 'attempt': None}}])
        self.assertEqual(result[0]['row']['envelope']['event']['operation_id'], 'observed-operation')
        self.assertNotIn('attempt', result[0]['row']['envelope']['event'])
