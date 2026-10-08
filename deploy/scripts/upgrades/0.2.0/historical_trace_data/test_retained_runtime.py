import copy
import unittest

from retained_runtime import select_core_before, import_retained_history
from test_core_history import fixture


class RetainedRuntimeTests(unittest.TestCase):
    def test_thread_target_derivations_do_not_inflate_source_count(self):
        from retained_runtime import merge_thread_history
        from core_history_batches import HISTORY_CORE_FIELDS
        class Runtime:
            def _native(self, flags, data):
                self.flags = flags
                return b'{"ledger":[{"event_id":"message-one"}]}'
        runtime = Runtime()
        plan = {'core': {key: [] for key in HISTORY_CORE_FIELDS}, 'ledger': [],
                'artifacts': [], 'issues': [], 'stats': {'source_counts': {'conversations': 1, 'ledger': 2}}}
        threads = {'core': {key: [] for key in HISTORY_CORE_FIELDS}, 'artifacts': [],
                   'message_events': [{'event_id': 'message-one'}], 'source_map': [{'thread_id': 't'}],
                   'issues': [], 'message_content_recovery': [], 'defaults': [], 'stats': {'thread_count': 1}}
        threads['core']['operations'] = [{'operation_id': 'derived'}]
        merge_thread_history(plan, threads, runtime)
        self.assertEqual(plan['stats']['verified_source_count'], 4)
        self.assertEqual(plan['core']['operations'], [{'operation_id': 'derived'}])
        self.assertEqual(plan['ledger'], [{'event_id': 'message-one'}])
        self.assertEqual(runtime.flags, ['--prepare-message-ledger'])

    def test_cutover_selects_original_conversation_dependencies(self):
        tables = fixture()
        tables['bkn_trace_conversations'].append(dict(tables['bkn_trace_conversations'][0],
            conversation_id='october', created_at='2026-10-01 01:00:00'))
        original = copy.deepcopy(tables)
        selected = select_core_before(tables, '2026-10-01T00:00:00Z')
        self.assertEqual(len(selected['bkn_trace_conversations']), 1)
        self.assertEqual(len(selected['bkn_trace_operation_call_facts']), 1)
        self.assertEqual(tables, original)

    def test_old_conversation_does_not_pull_in_new_round(self):
        tables = fixture()
        tables['bkn_trace_interactions'].append(dict(tables['bkn_trace_interactions'][0],
            interaction_id='october-round', created_at='2026-10-02 00:00:00'))
        tables['bkn_trace_operations'].append(dict(tables['bkn_trace_operations'][0],
            operation_id='october-operation', interaction_id='october-round',
            created_at='2026-10-02 00:00:00'))
        selected = select_core_before(tables, '2026-10-01T00:00:00Z')
        self.assertEqual(len(selected['bkn_trace_interactions']), 1)
        self.assertNotIn('october-operation', {r['operation_id'] for r in selected['bkn_trace_operations']})

    def test_all_dependency_preflight_precedes_first_import(self):
        class Runtime:
            def __init__(self): self.calls = []
            def _native(self, flags, data):
                self.calls.append(flags[0])
                if flags == ['--validate-ledger-records']:
                    raise ValueError('source ledger invalid')
                return b'{"verified":true}'
        runtime = Runtime()
        prepared = {'batches': [b'{}'], 'artifact_batches': [], 'ledger': b'{"ledger":[]}',
                    'snapshots': None, 'plan': {'core': {}, 'artifacts': []}}
        with self.assertRaises(ValueError):
            import_retained_history(runtime, prepared)
        self.assertEqual(runtime.calls, ['--validate-core-records', '--validate-ledger-records'])

class ProvenanceBatchTests(unittest.TestCase):
    def test_provenance_batches_include_original_context_and_preserve_rows(self):
        from retained_runtime import provenance_import_batches
        from snapshot import strict_loads
        core = {'conversations': [{'conversation_id': 'conv-one', 'owner': {'effective_subject_id': 'alice'}}],
                'interactions': [{'interaction_id': 'int-one', 'conversation_id': 'conv-one'}]}
        plan = {'core': core, 'historical_projections': [{'interaction_id': 'int-one', 'graph_payload': 'x' * 200}],
                'explanations': [{'interaction_id': 'int-one', 'view_json': 'ab' * 200}]}
        batches = [strict_loads(b) for b in provenance_import_batches(plan, max_bytes=750)]
        self.assertEqual(len(batches), 2)
        self.assertEqual([r for b in batches for r in b['historical_projections']], plan['historical_projections'])
        self.assertEqual([r for b in batches for r in b['explanations']], plan['explanations'])
        for batch in batches:
            self.assertEqual(batch['contexts'], [{'interaction_id': 'int-one', 'conversation_id': 'conv-one',
                                                   'owner': {'effective_subject_id': 'alice'}}])

class RetainedArtifactNormalizationTests(unittest.TestCase):
    def test_rejected_artifact_is_lossy_converted_without_blocking_batch(self):
        from retained_runtime import normalize_artifacts_lossy
        from snapshot import strict_loads

        class Runtime:
            def _native(self, flags, data):
                answers = []
                for line in data.splitlines():
                    artifact = strict_loads(line)['payload']
                    text = artifact.get('content', {}).get('text', '')
                    if 'token=' in text:
                        answers.append({'accepted': False, 'reason': 'artifact_native_invalid'})
                    else:
                        answers.append({'accepted': True, 'canonical_payload': artifact})
                return ''.join(__import__('json').dumps(answer, separators=(',', ':')) + '\n' for answer in answers).encode()

        plan = {
            'artifacts': [
                {'artifact_id': 'a1', 'interaction_id': 'i1', 'artifact_type': 'question', 'content': {'text': 'safe'}},
                {'artifact_id': 'a2', 'interaction_id': 'i1', 'artifact_type': 'result', 'content': {'text': 'token=secret'}},
            ],
            'thread_mappings': [{'source_kind': 'agent_thread', 'source_id': 't1', 'interaction_ids': ['i1']}],
        }
        normalized = normalize_artifacts_lossy(Runtime(), plan)
        self.assertEqual([item['artifact_id'] for item in normalized], ['a1', 'a2'])
        self.assertIn('Historical content omitted during upgrade', normalized[1]['content']['text'])
        self.assertEqual(plan['defaults'][0]['reason'], 'artifact_native_invalid')
        self.assertEqual(plan['defaults'][0]['source_id'], 't1')
