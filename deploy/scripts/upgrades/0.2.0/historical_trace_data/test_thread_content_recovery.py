# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import copy
import unittest

from core_history import plan_core_history
from test_core_history import fixture
from thread_content_recovery import plan_thread_content_recovery


def recovery(**fields):
    value = {'source_kind': 'agent_thread', 'source_id': 'thread', 'source_message_id': 'human',
             'conversation_id': 'uuid-conv', 'interaction_id': 'round',
             'question': 'Inventory?', 'answer': '500 units'}
    value.update(fields)
    return value


class ThreadContentRecoveryTest(unittest.TestCase):
    def test_restores_explicit_round_with_original_refs_and_no_source_mutation(self):
        source = fixture(); source['bkn_trace_ee_current_explanations'] = []
        plan = plan_core_history(source); rows = [recovery()]
        original = copy.deepcopy((plan, rows))
        result = plan_thread_content_recovery(plan, rows)
        by_role = {a['artifact_type']: a for a in result['artifacts']}
        self.assertEqual(by_role['question']['artifact_id'], 'original-question')
        self.assertEqual(by_role['result']['artifact_id'], 'original-result')
        self.assertEqual(by_role['result']['content'], {'text': '500 units'})
        self.assertEqual((plan, rows), original)
        self.assertFalse(result['issues'])

    def test_existing_content_conflict_does_not_overwrite_or_create_second_artifact(self):
        plan = plan_core_history(fixture())
        result = plan_thread_content_recovery(plan, [recovery(answer='Different')])
        self.assertFalse(result['artifacts'])
        self.assertTrue(any(i['reason'] == 'thread_content_conflict' for i in result['issues']))
        self.assertEqual(plan['artifacts'][1]['content'], {'text': '500 units'})

    def test_same_question_unknown_round_and_wrong_conversation_never_link(self):
        plan = plan_core_history(fixture())
        for row in [recovery(interaction_id='absent'), recovery(conversation_id='wrong')]:
            result = plan_thread_content_recovery(plan, [row])
            self.assertFalse(result['artifacts'])
            self.assertEqual(result['issues'][0]['reason'], 'thread_content_core_binding_conflict')

    def test_conflicting_message_sources_for_same_original_round_are_rejected(self):
        source = fixture(); source['bkn_trace_ee_current_explanations'] = []
        plan = plan_core_history(source)
        result = plan_thread_content_recovery(plan, [recovery(), recovery(source_message_id='second', answer='Different')])
        self.assertFalse(any(a['artifact_type'] == 'result' for a in result['artifacts']))
        self.assertEqual(result['artifacts'][0]['artifact_type'], 'question')
        self.assertTrue(any(i['reason'] == 'explanation_content_conflict' for i in result['issues']))

    def test_original_content_conflicts_are_not_filled_by_thread(self):
        plan = plan_core_history(fixture()); plan['artifacts'] = []
        plan['issues'] = [{'interaction_id': 'round', 'role': 'result', 'reason': 'explanation_content_conflict'}]
        result = plan_thread_content_recovery(plan, [recovery()])
        self.assertFalse(any(a['artifact_type'] == 'result' for a in result['artifacts']))

    def test_empty_source_content_is_not_written_as_a_placeholder(self):
        plan = plan_core_history(fixture()); plan['artifacts'] = []
        result = plan_thread_content_recovery(plan, [recovery(question='', answer='')])
        self.assertFalse(result['artifacts'])


if __name__ == '__main__':
    unittest.main()
