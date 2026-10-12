# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import copy
import json
import unittest
from core_history import plan_core_history, map_outbox


def fixture():
    start, end = '2026-09-12 01:02:03.123456', '2026-09-12 01:02:04.654321'
    owner = dict(application_principal_id='agent', effective_subject_type='user', effective_subject_id='alice', delegation_id='')
    conv = dict(conversation_id='uuid-conv', **owner, agent_name='Agent', actor_name_snapshot='Alice', creation_auth_method='session', external_conversation_key='thread', generation=2, status='active', one_shot=0, current_slot=1, row_version=3, created_at=start, updated_at=end, closed_at=None)
    interaction = dict(interaction_id='round', conversation_id='uuid-conv', ordinal_no=7, execution_status='completed', evidence_status='complete', start_idempotency_key='start-original', terminal_idempotency_key='finish-original', terminal_payload_hash='payload-original', active_slot=None, closure_manifest=json.dumps(dict(completion_manifest_version='1', answer_artifact_ref='artifact:original-result', completion_reason='done')), assembler_deadline=None, lease_token='lease', lease_epoch=4, lease_version=5, lease_expires_at=end, row_version=9, created_at=start, updated_at=end, terminal_at=end)
    op = dict(operation_id='op', conversation_id='uuid-conv', interaction_id='round', operation_key='key', tool_name='tool', parent_operation_id='parent', causation_event_ids='["cause"]', attempt_no=2, attempt_status='completed', retryable=0, row_version=4, created_at=start, updated_at=end)
    receipt = dict(receipt_id='receipt', schema_version='3.0.0', **owner, conversation_id='uuid-conv', interaction_id='round', operation_id='op', attempt_no=2, operation_key='key', tool_name='tool', receipt_status='completed', evidence_durability='durable', required_receipt=1, request_id='request', trace_id='trace-original', causation_event_ids='["cause"]', observed_evidence_refs='[]', business_refs='[]', artifact_refs='[]', partial_reasons='[]', row_version=4, issued_at=start, terminal_at=end)
    payload = json.dumps(dict(mode='inline', media_type='application/json', byte_length=7, inline={'n': 1}))
    call = dict(operation_id='op', attempt_no=2, conversation_id='uuid-conv', interaction_id='round', receipt_id='receipt', tool_name='tool', protocol='mcp', source_module='module', parent_operation_id='parent', capability_profile=None, input_payload=payload, output_payload=payload, error_payload=None, request_id='request', trace_id='trace-original', span_id='span-original', started_at=start, finished_at=end, status='completed', retryable=0)
    event = dict(event_id='event', interaction_id='round', event_type='agent.interaction.started', observed_at=start, envelope=json.dumps(dict(envelope=dict(event_id='event', interaction_id='round', event_type='agent.interaction.started', payload=dict(question_artifact_ref='artifact:original-question')))), payload_hash='payload-hash', immutable_record_hash='immutable-hash')
    view = dict(interactionId='round', question='Inventory?', answer='500 units')
    return {'bkn_trace_conversations': [conv], 'bkn_trace_interactions': [interaction], 'bkn_trace_operations': [dict(op, operation_id='parent', parent_operation_id=None, attempt_no=1), op], 'bkn_trace_receipts': [receipt], 'bkn_trace_operation_call_facts': [call], 'bkn_trace_evidence_event_ledger': [event], 'bkn_trace_assembly_revisions': [dict(revision_id='revision', interaction_id='round', revision_no=3, parent_revision_id='previous', completion_manifest_version='1', included_receipt_ids='["receipt"]', included_event_ids='["event"]', artifact_manifest_hash='manifest', assembly_completeness='complete', partial_reasons='[]', trigger_type='terminal', created_at=end)], 'bkn_trace_ee_current_explanations': [dict(interaction_id='round', generated_at=end, view_json='0x' + json.dumps(view).encode().hex(), view_hash='original-view-hash')]}


def outbox(**fields):
    event = dict(event_id='obs', trace_id='trace-original', request_id='request', interaction_id='request-round', conversation_id='conv_req_request')
    event.update(fields)
    event['envelope'] = {'owner': dict(application_principal_id='agent', effective_subject_type='user', effective_subject_id='alice', delegation_id=''), 'event': {'event_id': 'obs'}}
    return {'kind': 'evidence', 'row': {'envelope': {'event': event}}}


def producer_observation(**fields):
    record = outbox(event_type='data.query.observed', operation_id='child-operation',
                    request_id='child-request', **fields)
    outer = record['row']['envelope']['event']
    outer['envelope']['owner']['application_principal_id'] = 'producer-module'
    outer['envelope']['event'] = {key: outer[key] for key in
        ('event_type', 'operation_id', 'request_id', 'trace_id', 'interaction_id')}
    return record


class CoreHistoryTest(unittest.TestCase):
    def test_producer_different_app_same_subject_links_only_round_keeps_child_identity(self):
        from history_plan import map_evidence_records
        core = plan_core_history(fixture())['core']
        row = producer_observation()
        original = copy.deepcopy(row)
        outcomes = map_outbox([row], core)
        self.assertEqual(outcomes[0]['status'], 'matched')
        self.assertEqual(outcomes[0]['target']['interaction_id'], 'round')
        self.assertFalse(outcomes[0]['target'].get('operation_id'))
        self.assertFalse(outcomes[0]['target'].get('receipt_id'))
        mapped = map_evidence_records([row], outcomes)[0]['row']['envelope']['event']
        self.assertEqual((mapped['operation_id'], mapped['request_id'], mapped['trace_id']),
                         ('child-operation', 'child-request', 'trace-original'))
        self.assertEqual(mapped['envelope']['owner']['application_principal_id'], 'producer-module')
        self.assertEqual(row, original)

    def test_multiple_requests_shared_trace_link_unique_round_not_receipt(self):
        source = fixture()
        source['bkn_trace_receipts'].append(dict(source['bkn_trace_receipts'][0], receipt_id='other', request_id='other-request'))
        outcome = map_outbox([producer_observation()], plan_core_history(source)['core'])[0]
        self.assertEqual(outcome['status'], 'matched')
        self.assertEqual(outcome['target']['interaction_id'], 'round')
        self.assertFalse(outcome['target'].get('operation_id'))

    def test_producer_subject_conflict_rejects_link(self):
        row = producer_observation()
        row['row']['envelope']['event']['envelope']['owner']['effective_subject_id'] = 'bob'
        outcome = map_outbox([row], plan_core_history(fixture())['core'])[0]
        self.assertEqual(outcome['status'], 'conflict')

    def test_trace_shared_across_rounds_is_ambiguous_without_explicit_round(self):
        source = fixture()
        source['bkn_trace_interactions'].append(dict(source['bkn_trace_interactions'][0], interaction_id='other-round'))
        source['bkn_trace_receipts'].append(dict(source['bkn_trace_receipts'][0], receipt_id='other', interaction_id='other-round', request_id='other-request'))
        outcome = map_outbox([producer_observation()], plan_core_history(source)['core'])[0]
        self.assertEqual(outcome['status'], 'ambiguous')

    def test_parent_operation_is_link_evidence_never_replaces_child_operation(self):
        row = producer_observation()
        row['row']['envelope']['event']['envelope']['event']['parent_operation_id'] = 'op'
        outcome = map_outbox([row], plan_core_history(fixture())['core'])[0]
        self.assertEqual(outcome['status'], 'matched')
        self.assertEqual(outcome['target']['parent_operation_id'], 'op')
        self.assertFalse(outcome['target'].get('operation_id'))

    def test_parent_and_known_round_conflict_is_explicit(self):
        source = fixture()
        source['bkn_trace_interactions'].append(dict(source['bkn_trace_interactions'][0], interaction_id='other-round'))
        row = producer_observation(interaction_id='other-round')
        row['row']['envelope']['event']['envelope']['event']['parent_operation_id'] = 'op'
        outcome = map_outbox([row], plan_core_history(source)['core'])[0]
        self.assertEqual(outcome['status'], 'conflict')

    def test_producer_event_with_original_operation_or_receipt_keeps_full_owner_check(self):
        for ref in ({'operation_id': 'op', 'attempt': 2}, {'receipt_id': 'receipt'}):
            row = producer_observation()
            row['row']['envelope']['event'].update(ref)
            outcome = map_outbox([row], plan_core_history(fixture())['core'])[0]
            self.assertEqual(outcome['status'], 'conflict')

    def test_preserves_active_ordinals_idempotency_parent_trace_payload_and_times(self):
        tables = fixture(); original = copy.deepcopy(tables)
        answer = plan_core_history(tables); core = answer['core']
        self.assertEqual(tables, original)
        self.assertEqual(core['conversations'][0]['status'], 'active')
        self.assertIsNone(core['conversations'][0]['closed_at'])
        interaction = core['interactions'][0]
        self.assertEqual(interaction['ordinal'], 7)
        for field in ('start_idempotency_key', 'terminal_idempotency_key', 'terminal_payload_hash'):
            self.assertEqual(interaction[field], tables['bkn_trace_interactions'][0][field])
        op = next(op for op in core['operations'] if op['operation_id'] == 'op')
        self.assertEqual((op['attempt'], op['parent_operation_id']), (2, 'parent'))
        call = core['call_facts'][0]
        self.assertEqual((call['trace_id'], call['span_id'], call['parent_operation_id']), ('trace-original', 'span-original', 'parent'))
        self.assertEqual(call['input']['inline'], {'n': 1})
        self.assertEqual(call['started_at'], '2026-09-12T01:02:03.123456Z')
        self.assertEqual(call['finished_at'], '2026-09-12T01:02:04.654321Z')
        self.assertEqual(answer['stats']['source_counts']['operations'], 2)

    def test_pending_state_remains_pending_without_terminal_times(self):
        tables = fixture()
        tables['bkn_trace_receipts'][0].update(receipt_status='pending', terminal_at=None)
        tables['bkn_trace_operation_call_facts'][0].update(status='pending', finished_at=None, output_payload=None)
        answer = plan_core_history(tables)
        self.assertEqual(answer['core']['receipts'][0]['receipt_status'], 'pending')
        self.assertIsNone(answer['core']['receipts'][0]['terminal_at'])
        self.assertIsNone(answer['core']['call_facts'][0]['finished_at'])

    def test_preserves_ledger_hash_bytes_assembly_and_original_artifact_identity(self):
        tables = fixture(); answer = plan_core_history(tables)
        self.assertEqual(answer['ledger'], tables['bkn_trace_evidence_event_ledger'])
        revision = answer['assembly_revisions'][0]
        self.assertEqual(revision['included_event_ids'], ['event'])
        self.assertEqual((revision['revision_id'], revision['revision_no'], revision['parent_revision_id']), ('revision', 3, 'previous'))
        self.assertEqual(revision['trigger'], 'terminal')
        by_type = {a['artifact_type']: a for a in answer['artifacts']}
        self.assertEqual(by_type['question']['artifact_id'], 'original-question')
        self.assertEqual(by_type['result']['artifact_id'], 'original-result')
        self.assertEqual(by_type['result']['content'], {'text': '500 units'})
        self.assertEqual(by_type['result']['interaction_id'], 'round')
        self.assertEqual(by_type['result']['bkn.account.id'], 'alice')
        self.assertFalse(answer['issues'])

    def test_bad_blob_identity_never_lends_answer_to_other_round(self):
        tables = fixture()
        tables['bkn_trace_ee_current_explanations'][0]['view_json'] = json.dumps({'interactionId': 'another-round', 'question': 'Inventory?', 'answer': 'secret'})
        answer = plan_core_history(tables)
        self.assertFalse(answer['artifacts'])
        self.assertEqual(answer['issues'][0]['reason'], 'explanation_interaction_mismatch')

    def test_invalid_json_and_conflicting_duplicate_fail_explicitly(self):
        tables = fixture(); tables['bkn_trace_operations'][0]['causation_event_ids'] = 'broken'
        with self.assertRaises(ValueError): plan_core_history(tables)
        tables = fixture(); tables['bkn_trace_conversations'].append(dict(tables['bkn_trace_conversations'][0], status='closed'))
        with self.assertRaises(ValueError): plan_core_history(tables)

    def test_snapshot_records_and_idempotency_wire(self):
        tables = fixture()
        tables['bkn_trace_idempotency_records'] = [dict(scope='start', application_principal_id='agent', effective_subject_type='user', effective_subject_id='alice', delegation_id='', external_conversation_key='thread', idempotency_key='start-original', request_hash='h', resource_type='interaction', resource_id='round', created_at='2026-09-12 01:02:03.123456')]
        records = [dict(locator='bkn_trace.' + table, kind='core', row=row) for table, rows in tables.items() for row in rows]
        answer = plan_core_history(records)
        record = answer['core']['idempotency_records'][0]
        self.assertEqual(record['owner']['effective_subject_id'], 'alice')
        self.assertEqual(record['idempotency_key'], 'start-original')

    def test_outbox_explicit_and_unique_reference_connections_do_not_create_core(self):
        core = plan_core_history(fixture())['core']; original = copy.deepcopy(core)
        outcomes = map_outbox([outbox(operation_id='op', attempt=2), outbox(interaction_id='round'), outbox()], core)
        self.assertEqual([x['status'] for x in outcomes], ['matched'] * 3)
        self.assertEqual([x['target']['interaction_id'] for x in outcomes], ['round'] * 3)
        self.assertEqual(outcomes[0]['target']['operation_id'], 'op')
        self.assertEqual(core, original)

    def test_multiple_requests_keep_one_original_trace_and_no_text_matching(self):
        tables = fixture(); tables['bkn_trace_receipts'].append(dict(tables['bkn_trace_receipts'][0], receipt_id='r2', request_id='req2'))
        core = plan_core_history(tables)['core']
        self.assertEqual({r['trace_id'] for r in core['receipts']}, {'trace-original'})
        outcomes = map_outbox([outbox(request_id=''), outbox(request_id='req2'), outbox(trace_id='missing', request_id='missing', question='Inventory?')], core)
        self.assertEqual([x['status'] for x in outcomes], ['ambiguous', 'matched', 'unlinked'])
        self.assertEqual(outcomes[1]['target']['receipt_id'], 'r2')

    def test_outbox_owner_and_known_reference_conflicts_are_not_connected(self):
        core = plan_core_history(fixture())['core']
        record = outbox(operation_id='op', attempt=2)
        record['row']['envelope']['event']['envelope']['owner']['effective_subject_id'] = 'bob'
        outcomes = map_outbox([record, outbox(operation_id='op', attempt=2, request_id='wrong')], core)
        self.assertEqual([x['status'] for x in outcomes], ['conflict', 'conflict'])

    def test_original_interaction_without_receipt_still_maps_owned_round(self):
        tables = fixture(); tables['bkn_trace_receipts'] = []; tables['bkn_trace_operation_call_facts'] = []
        core = plan_core_history(tables)['core']
        outcomes = map_outbox([outbox(interaction_id='round')], core)
        self.assertEqual(outcomes[0]['status'], 'matched')
        self.assertEqual(outcomes[0]['target']['interaction_id'], 'round')
        self.assertEqual(outcomes[0]['target']['receipt_id'], '')

    def test_three_conflicting_explanations_are_explicit_without_arbitrary_selection(self):
        tables = fixture()
        for answer in ('different answer', 'third answer'):
            tables['bkn_trace_ee_current_explanations'].append(dict(
                tables['bkn_trace_ee_current_explanations'][0], view_json=json.dumps(
                    {'interactionId': 'round', 'question': 'Inventory?', 'answer': answer})))
        result = plan_core_history(tables)
        self.assertEqual([artifact['artifact_type'] for artifact in result['artifacts']], ['question'])
        self.assertTrue(any(issue['reason'] == 'explanation_content_conflict' for issue in result['issues']))

    def test_invalid_observation_is_reported_per_record_without_losing_other_rows(self):
        core = plan_core_history(fixture())['core']
        outcomes = map_outbox([{'row': {'envelope': 'invalid'}}, outbox()], core)
        self.assertEqual([item['status'] for item in outcomes], ['invalid', 'matched'])
        self.assertEqual(outcomes[0]['source_ordinal'], 0)

    def test_known_operation_wrong_attempt_is_conflict_and_keeps_original_candidate(self):
        core = plan_core_history(fixture())['core']
        outcome = map_outbox([outbox(operation_id='op', attempt=3)], core)[0]
        self.assertEqual(outcome['status'], 'conflict')
        self.assertEqual(outcome['candidates'][0]['attempt'], 2)

    def test_qualified_core_snapshot_tables_and_unprefixed_binary_hex(self):
        tables = fixture()
        tables['bkn_trace_idempotency_records'] = [dict(scope='start', application_principal_id='agent', effective_subject_type='user', effective_subject_id='alice', delegation_id='', external_conversation_key='thread', idempotency_key='start-original', request_hash='h', resource_type='interaction', resource_id='round', created_at='2026-09-12 01:02:03.123456')]
        tables['bkn_trace_ee_current_explanations'][0]['view_json'] = tables['bkn_trace_ee_current_explanations'][0]['view_json'][2:]
        answer = plan_core_history({'bkn_trace.' + table: rows for table, rows in tables.items()})
        self.assertEqual(len(answer['core']['conversations']), 1)
        self.assertEqual(len(answer['core']['idempotency_records']), 1)
        self.assertEqual(len(answer['artifacts']), 2)


if __name__ == '__main__':
    unittest.main()

class ArtifactContextFallbackTests(unittest.TestCase):
    def test_missing_request_uses_reported_artifact_only_marker(self):
        tables = fixture()
        tables['bkn_trace_receipts'][0].update(request_id=None, trace_id=None)
        tables['bkn_trace_operation_call_facts'][0].update(request_id=None, trace_id=None)
        result = plan_core_history(tables)
        self.assertTrue(all(a['bkn.request.id'] for a in result['artifacts']))
        self.assertTrue(all(a['trace_id'] == '' for a in result['artifacts']))
        self.assertIsNone(result['core']['receipts'][0]['request_id'])
        self.assertEqual(len(result['field_defaults']), 2)
