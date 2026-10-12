# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import copy
import unittest

from business_snapshot_links import link_business_snapshots


def core():
    return {'interactions': [
        {'interaction_id': 'round-a', 'conversation_id': 'conv-a',
         'closure_manifest': {'answer_artifact_ref': 'artifact:answer-a'}},
        {'interaction_id': 'round-b', 'conversation_id': 'conv-b'}],
        'operations': [
            {'operation_id': 'op-a', 'interaction_id': 'round-a', 'conversation_id': 'conv-a', 'attempt': 1},
            {'operation_id': 'op-b', 'interaction_id': 'round-b', 'conversation_id': 'conv-b', 'attempt': 1}]}


def snapshot(task='analysis-task', **fields):
    return dict(question='Question', answer='Answer', source_task_ids=[task], **fields)


def ledger(ref='artifact:question-a'):
    return [{'interaction_id': 'round-a', 'event_type': 'agent.interaction.started',
             'envelope': {'envelope': {'payload': {'question_artifact_ref': ref}}}}]


class BusinessSnapshotLinksTest(unittest.TestCase):
    def test_count_fact_identity_contains_an_explicit_original_operation(self):
        body = snapshot(evidence_catalog={'facts': [{'id': 'fact:count:op-a:1:object-type'}]})
        result = link_business_snapshots([body], core(), ledger())
        self.assertEqual(result['linked']['round-a']['operation_refs'], ['op-a'])

    def test_nested_operation_subject_refs_restore_original_artifact_refs(self):
        body = snapshot(evidence_catalog={'facts': [{'subject_refs': ['operation:op-a'],
                                                     'predicate_refs': ['output:op-a:/result']}]})
        original = copy.deepcopy(body)
        result = link_business_snapshots([body], core(), ledger())
        linked = result['linked']['round-a']
        self.assertEqual((linked['question'], linked['answer']), ('Question', 'Answer'))
        self.assertEqual((linked['question_ref'], linked['answer_ref']),
                         ('artifact:question-a', 'artifact:answer-a'))
        self.assertEqual(linked['operation_refs'], ['op-a'])
        self.assertEqual(body, original)

    def test_same_text_and_task_identity_never_establish_execution_identity(self):
        result = link_business_snapshots([snapshot(task='round-a')], core(), ledger())
        self.assertEqual(result['linked'], {})
        self.assertEqual(result['issues'][0]['reason'], 'business_snapshot_no_operation_reference')

    def test_unknown_or_cross_round_refs_are_not_partially_linked(self):
        for refs, reason in [(['op-a', 'missing'], 'business_snapshot_operation_missing'),
                             (['op-a', 'op-b'], 'business_snapshot_interaction_ambiguous')]:
            body = snapshot(operation_catalog=[{'operation_id': value} for value in refs])
            result = link_business_snapshots([body], core(), ledger())
            self.assertFalse(result['linked'])
            self.assertEqual(result['issues'][0]['reason'], reason)

    def test_original_operation_owner_mismatch_is_not_accepted(self):
        value = core(); value['operations'][0]['conversation_id'] = 'conv-b'
        result = link_business_snapshots([snapshot(time_rail=[{'operation_id': 'op-a'}])], value, ledger())
        self.assertFalse(result['linked'])
        self.assertEqual(result['issues'][0]['reason'], 'business_snapshot_operation_identity_conflict')

    def test_same_round_conflicting_text_never_silently_selects_one_task(self):
        first = snapshot(operation_catalog=[{'operation_id': 'op-a'}])
        second = dict(first, answer='Other answer', source_task_ids=['other-task'])
        result = link_business_snapshots([first, second], core(), ledger())
        self.assertEqual(result['linked']['round-a']['question'], 'Question')
        self.assertNotIn('answer', result['linked']['round-a'])
        self.assertEqual(result['issues'][0]['reason'], 'business_snapshot_content_conflict')

    def test_original_artifact_ref_conflict_is_reported_without_choosing(self):
        body = snapshot(evidence_catalog={'facts': [{'evidence_id': 'evidence:op-a:1'}]})
        result = link_business_snapshots([body], core(), ledger() + ledger('artifact:other'))
        self.assertNotIn('question', result['linked']['round-a'])
        self.assertEqual(result['linked']['round-a']['answer'], 'Answer')
        self.assertEqual(result['issues'][0]['reason'], 'business_snapshot_artifact_reference_ambiguous')


if __name__ == '__main__':
    unittest.main()
