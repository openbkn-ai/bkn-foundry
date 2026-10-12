# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import copy
import json
import unittest

from history_restore import plan_retained_history
from test_core_history import fixture, outbox


def task(ref='new-op', question='Another question', answer='Another answer'):
    body = {'contract': 'claim-attribution/1', 'question': question, 'answer': answer,
            'evidence_catalog': {'facts': [{'subject_refs': ['operation:' + ref]}]}}
    return {'kind': 'agent_task', 'row': {'f_task_id': 'analysis-task',
            'f_account_id': 'alice', 'f_agent_id': 'business_provenance_claim_attribution',
            'f_input': {'message': 'INPUT_JSON:' + json.dumps(body)}}}


def two_rounds():
    source = fixture()
    source['bkn_trace_interactions'].append(dict(source['bkn_trace_interactions'][0],
        interaction_id='new-round', ordinal_no=8,
        closure_manifest=json.dumps({'answer_artifact_ref': 'artifact:new-answer'})))
    source['bkn_trace_operations'].append(dict(source['bkn_trace_operations'][1],
        operation_id='new-op', interaction_id='new-round'))
    event = copy.deepcopy(source['bkn_trace_evidence_event_ledger'][0])
    event.update(event_id='new-event', interaction_id='new-round',
        envelope=json.dumps({'envelope': {'payload': {'question_artifact_ref': 'artifact:new-question'}}}))
    source['bkn_trace_evidence_event_ledger'].append(event)
    return source


class HistoryRestoreTest(unittest.TestCase):
    def test_new_round_content_restored_without_new_original_explanation_or_core(self):
        source = two_rounds(); agent = [task()]
        originals = copy.deepcopy((source, agent))
        plan = plan_retained_history(source, agent)
        artifacts = {a['artifact_id']: a for a in plan['artifacts']}
        self.assertEqual(artifacts['new-question']['content'], {'text': 'Another question'})
        self.assertEqual(artifacts['new-answer']['content'], {'text': 'Another answer'})
        self.assertEqual(artifacts['new-answer']['interaction_id'], 'new-round')
        self.assertEqual(len(plan['core']['conversations']), 1)
        self.assertEqual(len(plan['core']['interactions']), 2)
        self.assertEqual(plan['explanations'], source['bkn_trace_ee_current_explanations'])
        self.assertEqual(plan['ledger'], source['bkn_trace_evidence_event_ledger'])
        self.assertEqual((source, agent), originals)
        self.assertEqual(plan['stats']['additional_artifacts'], 2)

    def test_different_task_content_never_overwrites_original_explanation_artifact(self):
        source = fixture()
        plan = plan_retained_history(source, [task(ref='op', answer='Conflicting answer')])
        answers = [a for a in plan['artifacts'] if a['artifact_type'] == 'result']
        self.assertEqual(answers[0]['content'], {'text': '500 units'})
        self.assertEqual(plan['stats']['additional_artifacts'], 0)
        self.assertTrue(any(i['reason'] == 'retained_history_content_conflict' for i in plan['issues']))

    def test_question_text_does_not_link_unknown_task_operation(self):
        plan = plan_retained_history(fixture(), [task(ref='absent', question='Inventory?', answer='500 units')])
        self.assertEqual(plan['stats']['additional_artifacts'], 0)
        self.assertTrue(any(i['reason'] == 'business_snapshot_operation_missing' for i in plan['issues']))

    def test_mapping_changes_only_copy_and_preserves_trace_request(self):
        event = outbox(operation_id='op', attempt=2)
        event['row']['envelope']['event']['envelope']['event'] = {'trace_id': 'trace-original', 'request_id': 'request'}
        original = copy.deepcopy(event)
        plan = plan_retained_history(fixture(), evidence_records=[event])
        self.assertEqual(plan['mappings'][0]['status'], 'matched')
        mapped = plan['mapped_records'][0]['row']['envelope']['event']
        self.assertEqual((mapped['conversation_id'], mapped['interaction_id']), ('uuid-conv', 'round'))
        self.assertEqual((mapped['trace_id'], mapped['request_id']), ('trace-original', 'request'))
        self.assertEqual(event, original)

    def test_conflicting_original_explanations_are_not_replaced_with_task_content(self):
        source = fixture(); row = copy.deepcopy(source['bkn_trace_ee_current_explanations'][0])
        row['view_json'] = json.dumps({'interactionId': 'round', 'question': 'Inventory?', 'answer': 'Different'})
        source['bkn_trace_ee_current_explanations'].append(row)
        plan = plan_retained_history(source, [task(ref='op', question='Inventory?', answer='500 units')])
        self.assertFalse(any(a['artifact_type'] == 'result' for a in plan['artifacts']))


if __name__ == '__main__':
    unittest.main()
