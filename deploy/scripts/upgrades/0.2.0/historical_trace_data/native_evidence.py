"""Offline 015 Evidence -> 020 native aggregates and receipt projections."""

from collections import defaultdict
import copy
import hashlib
import json
import re

from opensearch_history import _iso
from snapshot import canonical
from trace import _timestamp_ns
from native_core import _time


def _networks(value, bare=False):
    result = set()
    if isinstance(value, dict):
        if isinstance(value.get('kn_id'), str) and value['kn_id'].strip():
            result.add(value['kn_id'].strip())
        for key, nested in value.items():
            result.update(_networks(nested, key == 'ref_id' or key.strip().lower().endswith(('_ref', '_refs'))))
    elif isinstance(value, list):
        for nested in value:
            result.update(_networks(nested, bare))
    elif bare and isinstance(value, str):
        ref = value.strip().removeprefix('business:')
        parts = ref.split(':')
        segments = {'kn': 2, 'object': 3, 'object_instance': 4, 'property': 4,
                    'relation': 3, 'metric': 3, 'logic': 4, 'function': 3,
                    'action_type': 3, 'action_instance': 4}
        expected = segments.get(parts[0])
        if expected:
            if parts[0] in ('object_instance', 'action_instance'):
                parts = ref.split(':', expected - 1)
            if all(parts) and len(parts) == expected:
                result.add(parts[1])
    return result


def _array_count(value):
    return len(value) if isinstance(value, list) else 0


def plan_aggregates(records, observed_at, receipts=()):
    terminal_events = defaultdict(list)
    for receipt in receipts:
        stamp = _time(receipt["issued_at"])
        terminal_events[receipt["trace_id"]].append({
            "event_id": "receipt:" + receipt["receipt_id"], "event_type": "retrieval.completed",
            "bkn.trace.schema.version": receipt["schema_version"],
            "observed_at": stamp, "emitted_at": _time(receipt.get("terminal_at") or receipt["issued_at"]),
            "producer_module": receipt["owner"]["application_principal_id"],
            "trace_id": receipt["trace_id"], "bkn.request.id": receipt["request_id"],
            "interaction_id": receipt["interaction_id"], "operation_id": receipt["operation_id"],
            "bkn.operation.name": receipt["tool_name"],
            "payload": {"status": receipt["receipt_status"], "evidence_durability": receipt["evidence_durability"],
                        "operation_key": receipt["operation_key"],
                        "business_refs": [{"ref_id": ref["ref_id"], "ref_type": ref["ref_type"],
                                           "visibility": "visible"} for ref in receipt["business_refs"]],
                        "partial_reasons": receipt["partial_reasons"]}})
    groups, rejected = defaultdict(list), {}
    for ordinal, record in enumerate(records):
        try:
            raw = record['row']['envelope']
            wrapper = json.loads(raw) if isinstance(raw, str) else raw
            outer = wrapper['event']
            envelope = outer['envelope']
            if not isinstance(envelope, dict) or not isinstance(envelope.get('event'), dict):
                raise ValueError('invalid_evidence_wrapper')
            event = copy.deepcopy(envelope['event'])
            owner = envelope.get('owner') or {}
            if not isinstance(owner, dict):
                raise ValueError('missing_evidence_owner')
            if not all(isinstance(owner.get(k), str) and owner[k] for k in
                       ('effective_subject_id', 'effective_subject_type', 'application_principal_id')):
                raise ValueError('missing_evidence_owner')
            if event.get('bkn.trace.schema.version') not in ('2.0.0', '2.1.0', '2.2.0'):
                raise ValueError('unsupported_evidence_projection_schema')
            trace_id = outer['trace_id']
            request_id = outer['request_id']
            if (not re.fullmatch(r'[0-9a-f]{32}', trace_id) or not request_id or
                    event.get('trace_id') != trace_id or event.get('bkn.request.id') != request_id or
                    event.get('event_id') != outer['event_id']):
                raise ValueError('evidence_identity_mismatch')
            if not isinstance(event.get('event_type'), str) or not event['event_type'] or not event.get('observed_at') or not isinstance(event.get('payload'), dict):
                raise ValueError('missing_evidence_projection_fields')
            _timestamp_ns(event['observed_at'])
            groups[trace_id].append((ordinal, outer, event, owner))
        except (KeyError, TypeError, ValueError) as error:
            reason = str(error) if isinstance(error, ValueError) else 'invalid_evidence_wrapper'
            rejected[ordinal] = reason
    items = []
    for trace_id, rows in sorted(groups.items()):
        # Root fields select the earliest event as the native primary context.
        # Every event keeps its own request/interaction/operation relationships.
        # Native Core receipt projection uses the same primary-context model.
        rows = sorted(rows, key=lambda row: (_timestamp_ns(row[2]['observed_at']), row[2]['event_id']))
        subjects = {(owner['effective_subject_type'], owner['effective_subject_id']) for _, _, _, owner in rows}
        reason = 'trace_owner_conflict' if len(subjects) != 1 else None
        events = {}
        for _, _, event, _ in rows:
            prior = events.get(event['event_id'])
            if prior is not None and canonical(prior) != canonical(event):
                reason = 'event_content_conflict'
            events[event['event_id']] = event
        for event in terminal_events[trace_id]:
            prior = events.get(event["event_id"])
            if prior is not None and canonical(prior) != canonical(event):
                reason = "event_content_conflict"
            events[event["event_id"]] = event
        if reason:
            rejected.update({ordinal: reason for ordinal, _, _, _ in rows})
            continue
        _, outer, _, owner = rows[0]
        events = sorted(events.values(), key=lambda e: (_timestamp_ns(e['observed_at']), e['event_id']))
        document_id = 'aggregate-' + hashlib.sha256(('trace-aggregate\x00' + trace_id).encode()).hexdigest()
        claims = [e['payload']['claim_id'] for e in events if e['event_type'] == 'claim.created' and isinstance(e['payload'].get('claim_id'), str) and e['payload']['claim_id']]
        doc = {'document_id': document_id, 'aggregate': True, 'trace_id': trace_id,
               'bkn.request.id': outer['request_id'], 'bkn.conversation.id': outer.get('conversation_id', ''),
               'bkn.account.id': owner['effective_subject_id'], 'bkn.account.type': owner['effective_subject_type'],
               'effective_subject_id': owner['effective_subject_id'],
               'application_principal_id': owner['application_principal_id'],
               'bkn.trace.schema.version': rows[0][2]['bkn.trace.schema.version'],
               'events': events, 'accepted_event_count': len(events),
               'claim_count': sum(e['event_type'] == 'claim.created' for e in events),
               'evidence_ref_count': sum(_array_count(e['payload'].get('evidence_refs')) for e in events if e['event_type'] == 'evidence.refs.created'),
               'business_ref_count': sum(_array_count(e['payload'].get('business_refs')) for e in events if e['event_type'] in ('business.refs.resolved', 'knowledge.read.observed')),
               'observed_start': events[0]['observed_at'], 'ingested_at': _iso(observed_at)}
        networks = sorted(set().union(*(_networks(e['payload']) for e in events)))
        if networks:
            doc['knowledge_network_ids'] = networks
        if claims:
            doc['claim_ids'] = claims
        if len(events) > 10000 or len(canonical(doc).encode()) > 8 << 20:
            rejected.update({ordinal: 'native_aggregate_capacity_exceeded' for ordinal, _, _, _ in rows})
            continue
        items.append({'_id': document_id, 'document': doc, 'source_ordinals': sorted(ordinal for ordinal, _, _, _ in rows)})
    return items, rejected
