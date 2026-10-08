# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Pure offline links from retained business Task inputs to original Core rounds.

The caller supplies extract_business_inputs() results and normalized Core/ledger.
Only explicit operation references establish identity. Text is compared only after
that identity is established, to reject contradictory sources for the same round.
No Task identity, question similarity, timestamps, or parent operations join rounds.
Return content and original refs for the caller's existing Artifact formatter.
"""
from collections import defaultdict
import json


def _operation_refs(body):
    refs = set()
    for field in ('operation_catalog', 'time_rail'):
        for item in body.get(field) or []:
            if item.get('operation_id'):
                refs.add(item['operation_id'])

    def visit(value):
        if isinstance(value, list):
            for item in value:
                visit(item)
        elif isinstance(value, dict):
            for key, item in value.items():
                if key == 'operation_id' and isinstance(item, str) and item:
                    refs.add(item)
                elif key in ('subject_refs', 'predicate_refs') and isinstance(item, list):
                    for ref in item:
                        if not isinstance(ref, str):
                            continue
                        parts = ref.split(':')
                        if len(parts) >= 2 and parts[0] in ('operation', 'output') and parts[1]:
                            refs.add(parts[1])
                elif key == 'evidence_id' and isinstance(item, str):
                    parts = item.split(':')
                    if len(parts) == 3 and parts[0] == 'evidence' and parts[1] and parts[2].isdigit():
                        refs.add(parts[1])
                elif key == 'id' and isinstance(item, str):
                    parts = item.split(':')
                    if (len(parts) >= 5 and parts[:2] == ['fact', 'count']
                            and parts[2] and parts[3].isdigit()):
                        refs.add(parts[2])
                else:
                    visit(item)

    visit((body.get('evidence_catalog') or {}).get('facts') or [])
    return refs


def link_business_snapshots(inputs, core, ledger=()):
    """Return {linked: interaction→content/refs/provenance, issues, stats}.

    Unknown or conflicting operation identities reject the whole snapshot. A
    conflicting content field/reference rejects that field, preserving the other.
    Inputs and Core remain untouched; sensitive content is never logged.
    """
    interactions = {row['interaction_id']: row for row in core['interactions']}
    operations = {row['operation_id']: row for row in core['operations']}
    artifact_refs = defaultdict(set)
    for row in ledger:
        envelope = row['envelope']
        envelope = json.loads(envelope) if isinstance(envelope, str) else envelope
        event = envelope.get('envelope', {})
        event = event.get('event', event)
        for field, key in (('question', 'question_artifact_ref'), ('answer', 'result_artifact_ref')):
            ref = (event.get('payload') or {}).get(key)
            if ref:
                artifact_refs[(row['interaction_id'], field)].add(ref)
    for iid, interaction in interactions.items():
        ref = (interaction.get('closure_manifest') or {}).get('answer_artifact_ref')
        if ref:
            artifact_refs[(iid, 'answer')].add(ref)

    grouped = defaultdict(list)
    issues = []
    matched = 0
    for body in inputs:
        refs = _operation_refs(body)
        source = {'source_task_ids': list(body.get('source_task_ids') or [])}
        reason = None
        if not refs:
            reason = 'business_snapshot_no_operation_reference'
        elif refs - operations.keys():
            reason = 'business_snapshot_operation_missing'
        else:
            candidates = {operations[ref]['interaction_id'] for ref in refs}
            if len(candidates) != 1:
                reason = 'business_snapshot_interaction_ambiguous'
            else:
                iid = next(iter(candidates))
                if iid not in interactions or any(
                    operations[ref]['conversation_id'] != interactions[iid]['conversation_id']
                    for ref in refs
                ):
                    reason = 'business_snapshot_operation_identity_conflict'
        if reason:
            issues.append(dict(source, reason=reason))
            continue
        grouped[iid].append((body, refs))
        matched += 1

    linked = {}
    for iid, snapshots in sorted(grouped.items()):
        value = {
            'source_task_ids': sorted({task for body, _ in snapshots for task in body.get('source_task_ids', [])}),
            'operation_refs': sorted({ref for _, refs in snapshots for ref in refs}),
        }
        for field in ('question', 'answer'):
            texts = {body[field] for body, _ in snapshots if isinstance(body.get(field), str) and body[field]}
            if len(texts) > 1:
                issues.append({'interaction_id': iid, 'field': field, 'reason': 'business_snapshot_content_conflict'})
                continue
            refs = artifact_refs[(iid, field)]
            if len(refs) > 1:
                issues.append({'interaction_id': iid, 'field': field, 'reason': 'business_snapshot_artifact_reference_ambiguous'})
                continue
            ref = next(iter(refs), '')
            if ref and (not isinstance(ref, str) or not ref.startswith('artifact:') or not ref[9:]):
                issues.append({'interaction_id': iid, 'field': field, 'reason': 'business_snapshot_artifact_reference_invalid'})
                continue
            if texts:
                value[field] = next(iter(texts))
                value[field + '_ref'] = ref
        linked[iid] = value
    return {'linked': linked, 'issues': issues, 'stats': {
        'snapshots': len(inputs), 'matched_snapshots': matched,
        'linked_interactions': len(linked),
        'question_interactions': sum('question' in value for value in linked.values()),
        'answer_interactions': sum('answer' in value for value in linked.values()),
    }}
