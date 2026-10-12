# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Pure offline conversion of retained Core facts; never invents executions."""

from collections import defaultdict
import copy
from datetime import datetime, timezone
import json
import re

from native_core import _id
from snapshot import canonical

TABLES = {
    'conversations': 'bkn_trace_conversations', 'interactions': 'bkn_trace_interactions',
    'operations': 'bkn_trace_operations', 'receipts': 'bkn_trace_receipts',
    'call_facts': 'bkn_trace_operation_call_facts', 'ledger': 'bkn_trace_evidence_event_ledger',
    'assembly_revisions': 'bkn_trace_assembly_revisions',
    'historical_projections': 'bkn_trace_ee_historical_provenance_projections',
    'explanations': 'bkn_trace_ee_current_explanations',
    'idempotency_records': 'bkn_trace_idempotency_records',
}
OWNER_FIELDS = ('application_principal_id', 'effective_subject_type', 'effective_subject_id', 'delegation_id')


def _json(value):
    if isinstance(value, bytes): value = value.decode('utf-8')
    if isinstance(value, str):
        if value.startswith('0x'): value = bytes.fromhex(value[2:]).decode('utf-8')
        return json.loads(value)
    return copy.deepcopy(value)


def _stamp(value):
    if value is None: return None
    stamp = datetime.fromisoformat(value.replace('Z', '+00:00'))
    # SQL DATETIME is the native store's UTC representation, not local wall time.
    if stamp.tzinfo is None: stamp = stamp.replace(tzinfo=timezone.utc)
    return stamp.astimezone(timezone.utc).isoformat(timespec='microseconds').replace('+00:00', 'Z')


def _owner(row):
    return {key: row.get(key) or '' for key in OWNER_FIELDS}


def _owner_key(owner):
    return tuple(owner.get(key) or '' for key in OWNER_FIELDS)


def _rows(source):
    if isinstance(source, dict):
        tables = defaultdict(list)
        for locator, rows in source.items():
            tables[locator.split('.')[-1]].extend(rows)
        return tables
    tables = defaultdict(list)
    for record in source:
        table = record.get('locator', '').split('.')[-1]
        if not table: table = TABLES.get(record.get('kind'), '')
        if table in TABLES.values(): tables[table].append(record['row'])
    return tables


def _convert(rows, fields, rename=None, json_fields=(), time_fields=(), bool_fields=()):
    rename = rename or {}
    result = []
    for row in rows:
        value = {}
        for field in fields:
            target = rename.get(field, field)
            item = row.get(field)
            if field in json_fields: item = _json(item)
            if field in time_fields: item = _stamp(item)
            if field in bool_fields:
                if item not in (0, 1, False, True): raise ValueError('invalid_core_boolean:' + field)
                item = bool(item)
            value[target] = item
        result.append(value)
    return result


def _dedup(rows, keys, category):
    result = {}
    for row in rows:
        identity = tuple(row.get(key) for key in keys)
        if any(value is None or value == '' for value in identity):
            raise ValueError('missing_core_identity:' + category)
        if identity in result and canonical(result[identity]) != canonical(row):
            raise ValueError('core_source_content_conflict:' + category)
        result[identity] = row
    return list(result.values())


def plan_core_history(source):
    """Return native import DTOs, untouched ledger/snapshots and recovery plans.

    Source may be a backup table dictionary or frozen locator/kind/row records.
    No writes, lifecycle transitions, identity splitting or state defaults occur.
    Ledger hashes and envelope bytes remain unchanged for explicit native import.
    """
    tables = _rows(source)
    rows = {key: tables.get(table, []) for key, table in TABLES.items()}
    core = {}
    core['conversations'] = _convert(rows['conversations'], (
        'conversation_id', 'agent_name', 'actor_name_snapshot', 'creation_auth_method',
        'external_conversation_key', 'generation', 'status', 'one_shot', 'row_version',
        'created_at', 'updated_at', 'closed_at'),
        time_fields=('created_at', 'updated_at', 'closed_at'), bool_fields=('one_shot',))
    for value, row in zip(core['conversations'], rows['conversations']): value['owner'] = _owner(row)
    core['interactions'] = _convert(rows['interactions'], (
        'interaction_id', 'conversation_id', 'ordinal_no', 'execution_status', 'evidence_status',
        'start_idempotency_key', 'terminal_idempotency_key', 'terminal_payload_hash',
        'closure_manifest', 'lease_token', 'lease_epoch', 'lease_version', 'lease_expires_at',
        'row_version', 'created_at', 'updated_at', 'terminal_at'), rename={'ordinal_no': 'ordinal'},
        json_fields=('closure_manifest',), time_fields=('lease_expires_at', 'created_at', 'updated_at', 'terminal_at'))
    core['operations'] = _convert(rows['operations'], (
        'operation_id', 'conversation_id', 'interaction_id', 'operation_key', 'tool_name',
        'parent_operation_id', 'causation_event_ids', 'attempt_no', 'attempt_status',
        'retryable', 'row_version', 'created_at', 'updated_at'), rename={'attempt_no': 'attempt'},
        json_fields=('causation_event_ids',), time_fields=('created_at', 'updated_at'), bool_fields=('retryable',))
    core['receipts'] = _convert(rows['receipts'], (
        'receipt_id', 'schema_version', 'conversation_id', 'interaction_id', 'operation_id',
        'attempt_no', 'operation_key', 'tool_name', 'receipt_status', 'evidence_durability',
        'required_receipt', 'request_id', 'trace_id', 'causation_event_ids', 'observed_evidence_refs',
        'business_refs', 'artifact_refs', 'partial_reasons', 'row_version', 'issued_at', 'terminal_at'),
        rename={'attempt_no': 'attempt', 'required_receipt': 'required'},
        json_fields=('causation_event_ids', 'observed_evidence_refs', 'business_refs', 'artifact_refs', 'partial_reasons'),
        time_fields=('issued_at', 'terminal_at'), bool_fields=('required_receipt',))
    for value, row in zip(core['receipts'], rows['receipts']): value['owner'] = _owner(row)
    core['call_facts'] = _convert(rows['call_facts'], (
        'operation_id', 'attempt_no', 'conversation_id', 'interaction_id', 'receipt_id',
        'tool_name', 'protocol', 'source_module', 'parent_operation_id', 'capability_profile',
        'input_payload', 'output_payload', 'error_payload', 'request_id', 'trace_id', 'span_id',
        'started_at', 'finished_at', 'status', 'retryable'),
        rename={'attempt_no': 'attempt', 'input_payload': 'input', 'output_payload': 'output', 'error_payload': 'error'},
        json_fields=('capability_profile', 'input_payload', 'output_payload', 'error_payload'),
        time_fields=('started_at', 'finished_at'), bool_fields=('retryable',))
    core['idempotency_records'] = _convert(rows['idempotency_records'], (
        'scope', 'external_conversation_key', 'idempotency_key', 'request_hash', 'resource_type',
        'resource_id', 'created_at'), time_fields=('created_at',))
    for value, row in zip(core['idempotency_records'], rows['idempotency_records']): value['owner'] = _owner(row)
    keys = {'conversations': ('conversation_id',), 'interactions': ('interaction_id',),
            'operations': ('operation_id',), 'receipts': ('receipt_id',),
            'call_facts': ('operation_id', 'attempt'),
            'idempotency_records': ('scope', 'external_conversation_key', 'idempotency_key', 'owner')}
    # Owner is an object, so idempotency keys require its canonical representation.
    for key, fields in keys.items():
        if key != 'idempotency_records': core[key] = _dedup(core[key], fields, key)
    ids = {}
    for value in core['idempotency_records']:
        identity = canonical([value.get(key) for key in keys['idempotency_records']])
        if identity in ids and canonical(ids[identity]) != canonical(value): raise ValueError('core_source_content_conflict:idempotency_records')
        ids[identity] = value
    core['idempotency_records'] = list(ids.values())
    revisions = _convert(rows['assembly_revisions'], (
        'revision_id', 'interaction_id', 'revision_no', 'parent_revision_id', 'completion_manifest_version',
        'included_receipt_ids', 'included_event_ids', 'artifact_manifest_hash', 'assembly_completeness',
        'partial_reasons', 'trigger_type', 'created_at'), rename={'trigger_type': 'trigger'},
        json_fields=('included_receipt_ids', 'included_event_ids', 'partial_reasons'), time_fields=('created_at',))
    core['assembly_revisions'] = revisions
    artifacts, issues = _restore_artifacts(core, rows)
    field_defaults = [{'interaction_id': artifact['interaction_id'], 'artifact_id': artifact['artifact_id'],
                       'field': 'bkn.request.id', 'value': artifact['bkn.request.id'],
                       'reason': 'artifact_projection_marker_source_request_missing'}
                      for artifact in artifacts if artifact['bkn.request.id'] == _id('request', ('artifact-projection', artifact['interaction_id']))]
    return {'core': core, 'ledger': copy.deepcopy(rows['ledger']),
            'assembly_revisions': revisions, 'historical_projections': copy.deepcopy(rows['historical_projections']),
            'explanations': copy.deepcopy(rows['explanations']), 'artifacts': artifacts, 'issues': issues,
            'field_defaults': field_defaults,
            'stats': {'source_counts': {key: len(value) for key, value in rows.items()},
                      'target_counts': {key: len(value) for key, value in core.items()},
                      'recovered_artifacts': len(artifacts), 'issues': len(issues)}}


def _restore_artifacts(core, rows):
    interactions = {row['interaction_id']: row for row in core['interactions']}
    conversations = {row['conversation_id']: row for row in core['conversations']}
    receipts = defaultdict(list)
    for row in core['receipts']: receipts[row['interaction_id']].append(row)
    refs = defaultdict(set)
    for row in rows['ledger']:
        envelope = _json(row['envelope'])
        event = envelope.get('envelope', {})
        if 'event' in event: event = event['event']
        payload = event.get('payload') or {}
        for role, field in (('question', 'question_artifact_ref'), ('result', 'result_artifact_ref')):
            if payload.get(field): refs[(row['interaction_id'], role)].add(payload[field])
    artifacts, issues, values = [], [], {}
    for row in rows['explanations']:
        iid = row['interaction_id']
        try:
            raw = row['view_json']
            # core_snapshot encodes binary columns as HEX without a SQL 0x prefix.
            if isinstance(raw, str) and re.fullmatch(r'(?:[0-9a-fA-F]{2})+', raw):
                raw = bytes.fromhex(raw).decode('utf-8')
            view = _json(raw)
            if not isinstance(view, dict) or view.get('interactionId', view.get('interaction_id')) != iid:
                raise ValueError('explanation_interaction_mismatch')
            if iid not in interactions: raise ValueError('explanation_core_interaction_missing')
        except (ValueError, UnicodeError, TypeError) as error:
            issues.append({'interaction_id': iid, 'reason': str(error) if str(error).startswith('explanation_') else 'explanation_blob_invalid'})
            continue
        for role, field in (('question', 'question'), ('result', 'answer')):
            text = view.get(field)
            if text is None or text == '': continue
            if not isinstance(text, str):
                issues.append({'interaction_id': iid, 'reason': 'explanation_content_invalid', 'role': role})
                continue
            key = (iid, role)
            if key in values and values[key] is None: continue
            if key in values and values[key][0] != text:
                values[key] = None
                issues.append({'interaction_id': iid, 'reason': 'explanation_content_conflict', 'role': role})
            elif key not in values: values[key] = (text, row)
    for (iid, role), value in values.items():
        if value is None: continue
        text, source = value
        interaction = interactions[iid]
        conversation = conversations[interaction['conversation_id']]
        owner = conversation['owner']
        closure = interaction.get('closure_manifest') or {}
        candidates = set(refs[(iid, role)])
        if role == 'result' and closure.get('answer_artifact_ref'): candidates.add(closure['answer_artifact_ref'])
        if len(candidates) > 1:
            issues.append({'interaction_id': iid, 'role': role, 'reason': 'artifact_reference_ambiguous'})
            continue
        ref = next(iter(candidates), '')
        if ref and (not isinstance(ref, str) or not ref.startswith('artifact:') or not ref[9:]):
            issues.append({'interaction_id': iid, 'role': role, 'reason': 'artifact_reference_invalid'})
            continue
        aid = ref[9:] if ref else _id('artifact', ('core-history', iid, role))
        calls = sorted(receipts[iid], key=lambda row: (row.get('issued_at') or '', row['receipt_id']))
        primary = next((row for row in calls if row.get('request_id')), calls[0] if calls else {})
        ledger_requests = [row.get('request_id') for row in rows['ledger']
                           if row['interaction_id'] == iid and row.get('request_id')]
        request_id = primary.get('request_id') or (ledger_requests[0] if ledger_requests else '')
        if not request_id:
            # Artifact contract requires a projection locator. This marker does
            # not become a Receipt Request or a technical Trace execution.
            request_id = _id('request', ('artifact-projection', iid))
        artifact = {'artifact_id': aid, 'artifact_type': role, 'bkn.request.id': request_id,
                    'trace_id': primary.get('trace_id') or '', 'interaction_id': iid, 'operation_id': '',
                    'content_type': 'application/json', 'schema_version': '2.2.0',
                    'observed_at': (interaction['created_at'] if role == 'question' else interaction.get('terminal_at')) or _stamp(source['generated_at']),
                    'content': {'text': text}, 'bkn.account.id': owner['effective_subject_id'],
                    'bkn.account.type': owner['effective_subject_type'], 'effective_subject_id': owner['effective_subject_id'],
                    'application_principal_id': owner['application_principal_id'], 'agent_or_app': conversation.get('agent_name') or owner['application_principal_id']}
        artifacts.append(artifact)
    return artifacts, issues


def _map_producer_observation(event, owner, core, ordinal):
    """015 module observations own child IDs, not the original Core receipt.

    Their process application principal differs from the executing application.
    Keep that owner and child request/operation intact; only relate the original
    round using explicit references and the same effective subject/delegation.
    """
    interactions = {row['interaction_id']: row for row in core['interactions']}
    conversations = {row['conversation_id']: row for row in core['conversations']}
    operations = {row['operation_id']: row for row in core['operations']}
    inner = event.get('envelope', {}).get('event', {})
    parent_id = event.get('parent_operation_id') or inner.get('parent_operation_id')
    parent = operations.get(parent_id)
    explicit = interactions.get(event.get('interaction_id'))
    trace_rounds = {row['interaction_id'] for row in core['receipts']
                    if event.get('trace_id') and row.get('trace_id') == event['trace_id']}
    request_rounds = {row['interaction_id'] for row in core['receipts']
                      if event.get('request_id') and row.get('request_id') == event['request_id']}
    if parent:
        candidates, method = {parent['interaction_id']}, 'observation_parent_operation'
    elif explicit:
        candidates, method = {explicit['interaction_id']}, 'observation_interaction'
    elif trace_rounds:
        candidates, method = trace_rounds, 'observation_trace'
    else:
        candidates, method = request_rounds, 'observation_request'
    outcome = {'source_ordinal': ordinal, 'event_id': event.get('event_id'), 'method': method,
               'candidates': [{'interaction_id': iid} for iid in sorted(candidates)]}
    if not candidates:
        outcome.update(status='unlinked', reason='no_original_candidate')
        return outcome
    if len(candidates) > 1:
        outcome.update(status='ambiguous', reason='multiple_original_interactions')
        return outcome
    iid = next(iter(candidates))
    interaction = interactions.get(iid)
    conversation = conversations.get(interaction['conversation_id']) if interaction else None
    subject_fields = ('effective_subject_type', 'effective_subject_id', 'delegation_id')
    conflict = (
        not conversation or
        any((owner.get(key) or '') != (conversation['owner'].get(key) or '') for key in subject_fields) or
        (explicit and explicit['interaction_id'] != iid) or
        (trace_rounds and iid not in trace_rounds) or
        (request_rounds and iid not in request_rounds) or
        (event.get('conversation_id') in conversations and event['conversation_id'] != conversation['conversation_id'])
    )
    if conflict:
        outcome.update(status='conflict', reason='subject_or_reference_conflict')
        return outcome
    outcome.update(status='matched', target={
        'conversation_id': conversation['conversation_id'], 'interaction_id': iid,
        'operation_id': '', 'receipt_id': '', 'attempt': None,
        'trace_id': event.get('trace_id') or '', 'request_id': event.get('request_id') or '',
    })
    if parent:
        outcome['target']['parent_operation_id'] = parent_id
    return outcome


def map_outbox(records, core):
    """Identify original Core candidates without changing observations or Core.

    Strong original operation/receipt references retain full-owner validation.
    Independent producer observations link only a uniquely identified original
    round with the same effective subject; their child call identities remain.
    Other Trace/request fallbacks require a unique owned receipt; text is unused.
    Ambiguous/conflicting candidates are retained explicitly for reconciliation.
    """
    conversations = {row['conversation_id']: row for row in core['conversations']}
    interactions = {row['interaction_id']: row for row in core['interactions']}
    operations = {row['operation_id']: row for row in core['operations']}
    receipts = core['receipts']
    outcomes = []
    for ordinal, record in enumerate(records):
        try:
            raw = _json(record['row']['envelope'])
            event = raw.get('event', raw)
            owner = event.get('envelope', {}).get('owner', {})
            if not isinstance(event, dict) or not isinstance(owner, dict):
                raise ValueError('invalid_outbox_envelope')
        except (ValueError, TypeError, KeyError, AttributeError, UnicodeError):
            outcomes.append({'source_ordinal': ordinal, 'status': 'invalid',
                             'reason': 'invalid_outbox_envelope', 'candidates': []})
            continue
        target_op = operations.get(event.get('operation_id'))
        target_int = interactions.get(event.get('interaction_id'))
        route = 'references'
        direct = [r for r in receipts if event.get('receipt_id') and r['receipt_id'] == event['receipt_id']]
        # A recognized receipt/operation retains the full original-owner checks
        # below. Module observations with independent child IDs link only rounds.
        if not direct and not target_op and str(event.get('event_type') or '').endswith('.observed'):
            outcomes.append(_map_producer_observation(event, owner, core, ordinal))
            continue
        if direct: candidates = direct; route = 'receipt'
        elif target_op:
            candidates = [r for r in receipts if r['operation_id'] == target_op['operation_id']]
            route = 'operation'
        elif target_int:
            candidates = [r for r in receipts if r['interaction_id'] == target_int['interaction_id']]
            route = 'interaction'
        else:
            candidates = [r for r in receipts if (event.get('trace_id') and r.get('trace_id') == event['trace_id']) or (event.get('request_id') and r.get('request_id') == event['request_id'])]
        original = list(candidates)
        # Known original references are checked against each other, regardless of
        # which selector established the candidate; conv_req_* is not a Core ID.
        known_conv = event.get('conversation_id') in conversations
        known_int = event.get('interaction_id') in interactions
        candidates = [r for r in candidates if _owner_key(r['owner']) == _owner_key(owner)
                      and (not known_conv or r['conversation_id'] == event['conversation_id'])
                      and (not known_int or r['interaction_id'] == event['interaction_id'])
                      and (not target_op or r['operation_id'] == target_op['operation_id'])
                      and (not event.get('attempt') or r['attempt'] == event['attempt'])
                      and (not event.get('trace_id') or r.get('trace_id') == event['trace_id'])
                      and (not event.get('request_id') or r.get('request_id') == event['request_id'])]
        if route == 'interaction' and len(candidates) != 1:
            conversation = conversations[target_int['conversation_id']]
            owned = _owner_key(conversation['owner']) == _owner_key(owner)
            outside = any((event.get('trace_id') and r.get('trace_id') == event['trace_id']
                           or event.get('request_id') and r.get('request_id') == event['request_id'])
                          and r['interaction_id'] != target_int['interaction_id'] for r in receipts)
            if owned and not outside and (not known_conv or conversation['conversation_id'] == event['conversation_id']):
                outcomes.append({'source_ordinal': ordinal, 'event_id': event.get('event_id'),
                    'status': 'matched', 'method': 'interaction',
                    'candidates': [{key: r[key] for key in ('conversation_id', 'interaction_id', 'operation_id', 'receipt_id', 'attempt', 'trace_id', 'request_id')} for r in original],
                    'target': {'conversation_id': conversation['conversation_id'],
                               'interaction_id': target_int['interaction_id'], 'operation_id': '',
                               'receipt_id': '', 'attempt': None, 'trace_id': event.get('trace_id') or '',
                               'request_id': event.get('request_id') or ''}})
                continue
        status = 'matched' if len(candidates) == 1 else 'ambiguous' if candidates else 'conflict' if original else 'unlinked'
        def identity(row):
            return {key: row[key] for key in ('conversation_id', 'interaction_id', 'operation_id', 'receipt_id', 'attempt', 'trace_id', 'request_id')}
        outcome = {'source_ordinal': ordinal, 'event_id': event.get('event_id'), 'status': status, 'method': route,
                   'candidates': [identity(row) for row in candidates or original]}
        if status == 'matched': outcome['target'] = identity(candidates[0])
        else: outcome['reason'] = {'ambiguous': 'multiple_original_candidates', 'conflict': 'owner_or_reference_conflict', 'unlinked': 'no_original_candidate'}[status]
        outcomes.append(outcome)
    return outcomes
