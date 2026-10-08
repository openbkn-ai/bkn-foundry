# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Offline retained-history plan; no storage writes or runtime compatibility."""
import json

from agent_history import extract_business_inputs
from business_snapshot_links import link_business_snapshots
from core_history import _restore_artifacts, map_outbox, plan_core_history
from history_plan import map_evidence_records


def plan_retained_history(core_tables, agent_records=(), evidence_records=()):
    """Preserve original Core/ledger/snapshots and supplement uniquely linked text.

    Task inputs remain analysis snapshots, never new business sessions. Temporary
    formatter rows are not returned as original explanations. Existing source
    content conflicts remain unresolved, and new conflicting content cannot
    replace an original Artifact. All inputs stay untouched.
    """
    plan = plan_core_history(core_tables)
    core = plan['core']
    inputs = extract_business_inputs(agent_records, plan['issues'])
    links = link_business_snapshots(inputs, core, plan['ledger'])
    plan['issues'].extend(links['issues'])
    existing = {(artifact['interaction_id'], artifact['artifact_type']): artifact
                for artifact in plan['artifacts']}
    blocked = {(issue['interaction_id'], issue['role'])
               for issue in plan['issues'] if 'interaction_id' in issue and 'role' in issue}
    interactions = {row['interaction_id']: row for row in core['interactions']}
    formatter_rows = []
    for iid, linked in links['linked'].items():
        view = {'interactionId': iid}
        for field, role in (('question', 'question'), ('answer', 'result')):
            text = linked.get(field)
            if not text or (iid, role) in blocked:
                continue
            artifact = existing.get((iid, role))
            if artifact:
                if artifact['content'] != {'text': text}:
                    plan['issues'].append({'interaction_id': iid, 'role': role,
                        'source_task_ids': linked['source_task_ids'],
                        'reason': 'retained_history_content_conflict'})
                continue
            view[field] = text
        if len(view) > 1:
            interaction = interactions[iid]
            formatter_rows.append({'interaction_id': iid, 'view_json': view,
                'generated_at': interaction.get('terminal_at') or interaction['created_at']})
    additions, issues = _restore_artifacts(core, {'ledger': plan['ledger'], 'explanations': formatter_rows})
    plan['artifacts'].extend(additions)
    plan['issues'].extend(issues)
    evidence_records = list(evidence_records)
    mappings = map_outbox(evidence_records, core)
    plan['mappings'] = mappings
    plan['mapped_records'] = map_evidence_records(evidence_records, mappings)

    # Count retained original Artifact refs separately from the presence of text.
    question_refs = set()
    for row in plan['ledger']:
        envelope = row['envelope']
        envelope = json.loads(envelope) if isinstance(envelope, str) else envelope
        event = envelope.get('envelope', {})
        event = event.get('event', event)
        if (event.get('payload') or {}).get('question_artifact_ref'):
            question_refs.add(row['interaction_id'])
    answer_refs = {row['interaction_id'] for row in core['interactions']
                   if (row.get('closure_manifest') or {}).get('answer_artifact_ref')}
    questions = {artifact['interaction_id'] for artifact in plan['artifacts']
                 if artifact['artifact_type'] == 'question'}
    answers = {artifact['interaction_id'] for artifact in plan['artifacts']
               if artifact['artifact_type'] == 'result'}
    plan['stats'].update(
        business_snapshots=links['stats'], additional_artifacts=len(additions),
        recovered_artifacts=len(plan['artifacts']), issues=len(plan['issues']),
        question_interactions=len(questions), answer_interactions=len(answers),
        original_question_refs=len(question_refs), original_answer_refs=len(answer_refs),
        original_question_refs_without_content=len(question_refs - questions),
        original_answer_refs_without_content=len(answer_refs - answers),
    )
    return plan
