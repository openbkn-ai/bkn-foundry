# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Pure offline Artifact supplementation for explicitly bound original messages."""
from core_history import _restore_artifacts


def plan_thread_content_recovery(plan, recoveries):
    """Return additional artifacts/issues; never mutate Core or original snapshots.

    Recoveries must already contain source message and original conversation /
    Interaction IDs. No question text, timestamps, or ordinal can establish a
    binding. Caller appends additions only after checking every reported issue.
    """
    core = plan['core']
    interactions = {row['interaction_id']: row for row in core['interactions']}
    existing = {(row['interaction_id'], row['artifact_type']): row for row in plan['artifacts']}
    blocked = {(issue['interaction_id'], issue['role'])
               for issue in plan['issues'] if 'interaction_id' in issue and 'role' in issue}
    rows = []
    issues = []
    for recovery in recoveries:
        iid = recovery.get('interaction_id')
        identity = {key: recovery.get(key) for key in
                    ('source_id', 'source_message_id', 'conversation_id', 'interaction_id')}
        interaction = interactions.get(iid)
        if (recovery.get('source_kind') != 'agent_thread' or
                not recovery.get('source_id') or not recovery.get('source_message_id') or
                interaction is None or interaction['conversation_id'] != recovery.get('conversation_id')):
            issues.append(dict(identity, reason='thread_content_core_binding_conflict'))
            continue
        view = {'interactionId': iid}
        for field, role in (('question', 'question'), ('answer', 'result')):
            text = recovery.get(field)
            if text is None or text == '' or (iid, role) in blocked:
                continue
            if not isinstance(text, str):
                issues.append(dict(identity, role=role, reason='thread_content_invalid'))
                continue
            artifact = existing.get((iid, role))
            if artifact:
                if artifact['content'] != {'text': text}:
                    issues.append(dict(identity, role=role, reason='thread_content_conflict'))
                continue
            view[field] = text
        if len(view) > 1:
            rows.append({'interaction_id': iid, 'view_json': view,
                         'generated_at': interaction.get('terminal_at') or interaction['created_at']})
    artifacts, formatter_issues = _restore_artifacts(core, {'ledger': plan['ledger'], 'explanations': rows})
    issues.extend(formatter_issues)
    return {'artifacts': artifacts, 'issues': issues, 'stats': {
        'source_message_recoveries': len(recoveries),
        'additional_artifacts': len(artifacts),
        'question_interactions': sum(a['artifact_type'] == 'question' for a in artifacts),
        'answer_interactions': sum(a['artifact_type'] == 'result' for a in artifacts),
    }}
