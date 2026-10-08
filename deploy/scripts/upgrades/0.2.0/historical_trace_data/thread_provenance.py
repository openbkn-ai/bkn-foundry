# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Prepare native EE graphs from explicitly identified retained message rounds."""
from snapshot import canonical, strict_loads


def prepare_thread_projections(plan, threads, native):
    core = threads['core']
    if not core['interactions']:
        return
    existing = {p['interaction_id'] for p in plan['historical_projections']}
    conversations = {c['conversation_id']: c for c in core['conversations']}
    questions = {a['interaction_id']: a['content'] for a in threads['artifacts']
                 if a['artifact_type'] == 'question'}
    answers = {a['interaction_id']: a['content'] for a in threads['artifacts']
               if a['artifact_type'] == 'result'}
    inputs = []
    for interaction in core['interactions']:
        iid = interaction['interaction_id']
        if iid in existing:
            raise ValueError('thread_projection_source_overlap')
        if iid not in questions or not isinstance(questions[iid], str):
            raise ValueError('thread_projection_question_missing')
        conversation = conversations[interaction['conversation_id']]
        inputs.append({'summary': {'interaction_id': iid,
            'conversation_id': interaction['conversation_id'],
            'status': interaction['execution_status'],
            'started_at': interaction['created_at'],
            'completed_at': interaction.get('terminal_at') or '',
            'agent_name': conversation.get('agent_name', '')},
            'owner': conversation['owner'],
            'call_facts': [f for f in core['call_facts'] if f['interaction_id'] == iid],
            'source_question': questions[iid], 'source_result': answers.get(iid, ''),
            'created_at': interaction['created_at'], 'updated_at': interaction['updated_at']})
    output = strict_loads(native(canonical({'interactions': inputs}).encode()))
    projections = output.get('historical_projections', [])
    expected = {v['summary']['interaction_id'] for v in inputs}
    if len(projections) != len(inputs) or {p['interaction_id'] for p in projections} != expected:
        raise ValueError('thread_projection_count_mismatch')
    plan['historical_projections'].extend(projections)
    plan.setdefault('stats', {})['message_derived_projections'] = len(projections)
