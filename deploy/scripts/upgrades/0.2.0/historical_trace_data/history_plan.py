# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Offline observation reference mapping against retained original Core facts."""

import copy

from snapshot import strict_loads


def map_evidence_records(records, mappings):
    """Map only explicit, resolved references; unlinked observations stay intact.

    Trace and Request IDs are always retained. Observation-only records do not
    create business sessions, rounds, calls, receipts or synthetic spans.
    """
    if len(records) != len(mappings):
        raise ValueError('evidence_mapping_count_mismatch')
    converted = copy.deepcopy(records)
    for ordinal, (record, mapping) in enumerate(zip(converted, mappings)):
        if mapping['source_ordinal'] != ordinal:
            raise ValueError('evidence_mapping_order_mismatch')
        if mapping['status'] != 'matched':
            continue
        raw = record['row']['envelope']
        wrapper = strict_loads(raw) if isinstance(raw, str) else raw
        outer = wrapper['event']
        event = outer.get('envelope', {}).get('event', {})
        target = mapping['target']
        for key in ('conversation_id', 'interaction_id', 'operation_id'):
            if target.get(key):
                outer[key] = target[key]
                event[key] = target[key]
        if target.get('receipt_id'):
            outer['receipt_id'] = target['receipt_id']
        if target.get('attempt'):
            outer['attempt'] = target['attempt']
        record['row']['envelope'] = wrapper
    return converted
