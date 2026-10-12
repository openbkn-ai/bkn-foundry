# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import copy
import unittest
from agent_thread_history import plan_agent_thread_history
from snapshot import strict_loads, canonical
from test_agent_history import fixture
from thread_provenance import prepare_thread_projections


class ThreadProjectionTests(unittest.TestCase):
    def test_native_builder_receives_exact_message_round_and_call_facts(self):
        threads = plan_agent_thread_history(fixture(), '2026-10-01T00:00:00Z')
        original = copy.deepcopy(threads)
        plan = {'historical_projections': []}
        captured = []
        def native(data):
            values = strict_loads(data)['interactions']
            captured.extend(values)
            return canonical({'historical_projections': [
                {'interaction_id': v['summary']['interaction_id']} for v in values]}).encode()
        prepare_thread_projections(plan, threads, native)
        self.assertEqual([v['source_question'] for v in captured], ['Inventory?', 'BOM?'])
        self.assertEqual([v['source_result'] for v in captured], ['500 units', '3 materials'])
        self.assertTrue(all(not f['trace_id'] and not f['request_id'] for v in captured for f in v['call_facts']))
        self.assertEqual(len(plan['historical_projections']), 2)
        self.assertEqual(threads, original)

    def test_existing_source_graph_is_never_replaced(self):
        threads = plan_agent_thread_history(fixture(), '2026-10-01T00:00:00Z')
        iid = threads['core']['interactions'][0]['interaction_id']
        plan = {'historical_projections': [{'interaction_id': iid}]}
        with self.assertRaisesRegex(ValueError, 'thread_projection_source_overlap'):
            prepare_thread_projections(plan, threads, lambda _: b'{}')

    def test_missing_or_duplicate_native_output_is_incomplete(self):
        threads = plan_agent_thread_history(fixture(), '2026-10-01T00:00:00Z')
        with self.assertRaisesRegex(ValueError, 'thread_projection_count_mismatch'):
            prepare_thread_projections({'historical_projections': []}, threads,
                                       lambda _: b'{"historical_projections":[]}')
