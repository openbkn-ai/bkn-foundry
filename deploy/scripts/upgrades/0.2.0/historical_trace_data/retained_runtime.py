# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
"""Offline capture and ordered restoration of original historical Core facts."""

from datetime import datetime, timezone
import os
from pathlib import Path

from core_history_batches import HISTORY_CORE_FIELDS, core_history_import_batches
from core_snapshot import export_core, read_core_backup
from history_restore import plan_retained_history
from snapshot import canonical, digest, read_snapshot, save_snapshot, strict_loads


def _utc(value):
    stamp = datetime.fromisoformat(value.replace('Z', '+00:00'))
    return stamp.replace(tzinfo=timezone.utc) if stamp.tzinfo is None else stamp.astimezone(timezone.utc)


def select_core_before(tables, before):
    """Select original conversations and their dependents at an explicit cutover."""
    cutoff = _utc(before)
    conversations = {row['conversation_id'] for key, rows in tables.items()
                     if key.split('.')[-1] == 'bkn_trace_conversations'
                     for row in rows if _utc(row['created_at']) < cutoff}
    interactions = {row['interaction_id'] for key, rows in tables.items()
                    if key.split('.')[-1] == 'bkn_trace_interactions' for row in rows
                    if row['conversation_id'] in conversations and _utc(row['created_at']) < cutoff}
    def historical(row):
        if row.get('interaction_id') and row['interaction_id'] not in interactions:
            return False
        stamp = next((row.get(field) for field in ('created_at', 'issued_at', 'started_at',
                     'generated_at', 'resolved_at') if row.get(field)), None)
        return not stamp or _utc(stamp) < cutoff
    result = {}
    for key, rows in tables.items():
        table = key.split('.')[-1]
        if table == 'bkn_trace_conversations':
            result[key] = [row for row in rows if row['conversation_id'] in conversations]
        elif any('conversation_id' in row for row in rows):
            result[key] = [row for row in rows if row.get('conversation_id') in conversations and historical(row)]
    for key, rows in tables.items():
        if key in result:
            continue
        if any('interaction_id' in row for row in rows):
            result[key] = [row for row in rows if row.get('interaction_id') in interactions and historical(row)]
        elif key.split('.')[-1] == 'bkn_trace_idempotency_records':
            result[key] = [row for row in rows if row.get('resource_id') in conversations | interactions and historical(row)]
    return result


def provenance_import_batches(plan, max_bytes=64 << 20):
    """Bound snapshots while preserving original rows and their Core ownership."""
    convs = {row['conversation_id']: row for row in plan['core']['conversations']}
    contexts = {row['interaction_id']: {'interaction_id': row['interaction_id'],
        'conversation_id': row['conversation_id'], 'owner': convs[row['conversation_id']]['owner']}
        for row in plan['core']['interactions']}
    keys = ('historical_projections', 'explanations')
    batch = {key: [] for key in keys}
    batch_contexts = {}
    def encoded():
        return canonical({**batch, 'contexts': list(batch_contexts.values())}).encode()
    for key in keys:
        for row in plan[key]:
            iid = row['interaction_id']
            if iid not in contexts:
                raise ValueError('provenance_core_context_missing')
            batch[key].append(row)
            batch_contexts[iid] = contexts[iid]
            if len(encoded()) > max_bytes:
                batch[key].pop()
                in_use = {item['interaction_id'] for field in keys for item in batch[field]}
                batch_contexts = {identity: contexts[identity] for identity in sorted(in_use)}
                if any(batch.values()):
                    yield encoded()
                batch = {field: [] for field in keys}
                batch[key] = [row]
                batch_contexts = {iid: contexts[iid]}
                if len(encoded()) > max_bytes:
                    raise ValueError('provenance_record_exceeds_transport_limit')
    if any(batch.values()):
        yield encoded()



def normalize_artifacts_lossy(runtime, plan):
    """Normalize artifacts individually; retain identity when content is rejected."""
    from run_upgrade import artifact_import_batches
    source_by_interaction = {}
    for mapping in plan.get('thread_mappings', []):
        source = {'source_kind': mapping.get('source_kind', 'agent_thread'),
                  'source_id': mapping.get('source_id', '')}
        for interaction_id in mapping.get('interaction_ids', []):
            source_by_interaction[interaction_id] = source
    normalized = []
    for batch in artifact_import_batches(plan['artifacts'], {}):
        artifacts = strict_loads(batch)['artifacts']
        answers = [strict_loads(line) for line in runtime._native([], ''.join(
            canonical({'kind': 'artifact', 'payload': artifact}) + '\n'
            for artifact in artifacts).encode()).splitlines()]
        if len(answers) != len(artifacts):
            raise ValueError('retained_artifact_validation_failed')
        for artifact, answer in zip(artifacts, answers):
            if answer.get('accepted') is not True:
                content_hash = digest(canonical(artifact['content']).encode())
                converted = dict(artifact)
                converted['content'] = {'text': 'Historical content omitted during upgrade; original content is preserved in the source snapshot (sha256:' + content_hash + ').'}
                converted.pop('content_hash', None)
                source = source_by_interaction.get(artifact.get('interaction_id'), {})
                plan.setdefault('defaults', []).append({**source,
                    'interaction_id': artifact.get('interaction_id', ''),
                    'artifact_id': artifact.get('artifact_id', ''),
                    'artifact_type': artifact.get('artifact_type', ''),
                    'reason': answer.get('reason', 'artifact_native_invalid'),
                    'conversion': 'native_content_replaced',
                    'source_content_sha256': content_hash})
                retry = runtime._native([], (canonical({'kind': 'artifact', 'payload': converted}) + '\n').encode()).splitlines()
                if len(retry) != 1:
                    raise ValueError('retained_artifact_fallback_invalid')
                answer = strict_loads(retry[0])
                if answer.get('accepted') is not True:
                    raise ValueError('retained_artifact_fallback_invalid')
            normalized.append(answer['canonical_payload'])
    plan['artifacts'] = normalized
    return normalized

def prepare_retained_history(runtime, state_root, deployment, evidence_records, before):
    """Freeze source once, then build and validate every original dependency."""
    if not before:
        raise ValueError('historical_cutover_required')
    backup = os.environ.get('BKN_HISTORY_CORE_BACKUP')
    capture_key = digest(canonical([before, str(Path(backup).resolve()) if backup else 'live']).encode())[:16]
    root = Path(state_root) / deployment['instance'] / ('core-source-' + capture_key)
    if not root.exists():
        tables, metadata = read_core_backup(backup) if backup else export_core(runtime.source)
        selected = select_core_before(tables, before)
        records = [{'kind': 'core_source', 'source_id': 'trace-core', 'locator': key, 'row': row}
                   for key, rows in selected.items() for row in rows]
        save_snapshot(root, records, deployment['instance'],
                      {**metadata, 'before': before, 'cluster_uid': deployment['cluster_uid']})
    manifest = strict_loads((root / 'snapshot.json').read_text())
    if (manifest['source_deployment'] != deployment['instance']
            or manifest['metadata'].get('cluster_uid') != deployment['cluster_uid']
            or manifest['metadata'].get('before') != before):
        raise ValueError('core_snapshot_deployment_mismatch')
    tables = {}
    for record in read_snapshot(root):
        tables.setdefault(record['locator'], []).append(record['row'])
    from agent_history import export_agent_source
    agent_root = Path(state_root) / deployment['instance'] / ('agent-source-' + digest(before.encode())[:16])
    if not agent_root.exists():
        records, metadata = export_agent_source(runtime.source, before)
        save_snapshot(agent_root, records, deployment['instance'],
                      {'before': before, 'cluster_uid': deployment['cluster_uid'], 'tables': metadata})
    agent_manifest = strict_loads((agent_root / 'snapshot.json').read_text())
    if (agent_manifest['source_deployment'] != deployment['instance']
            or agent_manifest['metadata'].get('cluster_uid') != deployment['cluster_uid']
            or agent_manifest['metadata'].get('before') != before):
        raise ValueError('agent_snapshot_deployment_mismatch')
    agent_records = list(read_snapshot(agent_root))
    plan = plan_retained_history(tables, agent_records, evidence_records)
    from agent_thread_history import plan_agent_thread_history
    threads = plan_agent_thread_history(agent_records, before, existing_core=plan['core'],
                                        actor_names=getattr(runtime, 'actor_names', {}))
    merge_thread_history(plan, threads, runtime)
    supplemental_gaps = {'business_snapshot_no_operation_reference', 'business_snapshot_operation_missing'}
    if any(issue['reason'] not in supplemental_gaps for issue in plan['issues']):
        raise ValueError('retained_source_conversion_issues')
    batches = list(core_history_import_batches(plan['core']))
    # Normalize each Artifact through the unchanged native contract.
    normalize_artifacts_lossy(runtime, plan)
    from run_upgrade import artifact_import_batches
    settings = {'tls_verify': runtime._tls_options(False)['verify_tls']}
    ca_file = runtime._tls_options(False).get('ca_file')
    if ca_file:
        settings['ca_pem'] = Path(ca_file).read_text()
    return {'plan': plan, 'batches': batches,
            'artifact_batches': list(artifact_import_batches(plan['artifacts'], settings)),
            'ledger': canonical({'ledger': plan['ledger']}).encode(),
            'snapshots': list(provenance_import_batches(plan)),
            'source_manifest': manifest}


def merge_thread_history(plan, threads, runtime):
    """Add actual stored message rounds without replacing original Core facts."""
    if threads['issues']:
        raise ValueError('thread_source_conversion_issues')
    from thread_content_recovery import plan_thread_content_recovery
    recovery = plan_thread_content_recovery(plan, threads['message_content_recovery'])
    if recovery['issues']:
        raise ValueError('thread_original_content_conflict')
    plan['artifacts'].extend(recovery['artifacts'])
    original_count = sum(plan['stats']['source_counts'].get(key, 0)
                         for key in (*HISTORY_CORE_FIELDS, 'ledger', 'historical_projections', 'explanations'))
    for key in HISTORY_CORE_FIELDS:
        plan['core'][key].extend(threads['core'][key])
    plan['artifacts'].extend(threads['artifacts'])
    if threads['message_events']:
        derived = strict_loads(runtime._native(['--prepare-message-ledger'],
                              canonical({'message_events': threads['message_events']}).encode()))
        if len(derived['ledger']) != len(threads['message_events']):
            raise ValueError('thread_message_ledger_count_mismatch')
        plan['ledger'].extend(derived['ledger'])
    plan['thread_mappings'] = threads['source_map']
    plan['thread_defaults'] = threads['defaults']
    if threads['core']['interactions']:
        from thread_provenance import prepare_thread_projections
        prepare_thread_projections(plan, threads, runtime._native_ee_projection)
    plan['stats']['agent_threads'] = threads['stats']
    plan['stats']['target_counts'] = {key: len(plan['core'][key]) for key in HISTORY_CORE_FIELDS}
    plan['stats']['recovered_artifacts'] = len(plan['artifacts'])
    for role, name in (('question', 'question_interactions'), ('result', 'answer_interactions')):
        plan['stats'][name] = len({a['interaction_id'] for a in plan['artifacts'] if a['artifact_type'] == role})
    # Message-derived target rows are not additional original source records.
    plan['stats']['verified_source_count'] = original_count + len(threads['source_map'])


def import_retained_history(runtime, prepared):
    """Validate all inputs first; restore artifacts, Core, Ledger, then snapshots."""
    for batch in prepared['batches']:
        if strict_loads(runtime._native(['--validate-core-records'], batch)).get('verified') is not True:
            raise ValueError('retained_core_preflight_failed')
    if strict_loads(runtime._native(['--validate-ledger-records'], prepared['ledger'])).get('verified') is not True:
        raise ValueError('retained_ledger_preflight_failed')
    for snapshot in prepared['snapshots'] or []:
        if strict_loads(runtime._native(['--validate-provenance-records'], snapshot)).get('verified') is not True:
            raise ValueError('retained_provenance_preflight_failed')
    results = {'verified': True, 'created': 0, 'already_verified': 0}
    for flags, batches in [(['--import-artifact-records'], prepared['artifact_batches']),
                          (['--import-core-records'], prepared['batches']),
                          (['--import-ledger-records'], [prepared['ledger']]),
                          (['--import-provenance-records'], prepared['snapshots'] or [])]:
        for batch in batches:
            answer = strict_loads(runtime._native(flags, batch))
            if answer.get('verified') is not True:
                raise ValueError('retained_history_readback_failed')
            results['created'] += answer.get('created', 0)
            results['already_verified'] += answer.get('already_verified', 0)
    results['source_count'] = prepared['plan']['stats']['verified_source_count']
    return results
