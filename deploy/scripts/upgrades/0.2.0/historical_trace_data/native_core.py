"""Convert stored observations into ordinary 020 Core records for native queries."""
import copy
from collections import defaultdict
from datetime import datetime, timedelta, timezone
import hashlib
import json

from snapshot import canonical
from trace import _timestamp_ns


def _id(prefix, value):
    return prefix + '_' + hashlib.sha256(canonical(value).encode()).hexdigest()[:48]


def _map_ids(keys, old_index, prefix):
    occurrences = defaultdict(set)
    for key in keys:
        occurrences[key[old_index]].add(key)
    result = {}
    for key in keys:
        old = key[old_index]
        result[key] = old if isinstance(old, str) and old.isascii() and 0 < len(old) <= 64 and len(occurrences[old]) == 1 else _id(prefix, key)
    return result


def _time(value):
    # Core SQL uses DATETIME(6); event timestamps keep their original precision.
    return (datetime(1970, 1, 1, tzinfo=timezone.utc) + timedelta(microseconds=_timestamp_ns(value)//1000)).isoformat(timespec='microseconds').replace('+00:00', 'Z')


def plan_core(records, actor_names=None):
    actor_names = actor_names or {}
    parsed = []
    converted = copy.deepcopy(records)
    for ordinal, record in enumerate(converted):
        raw = record['row']['envelope']
        wrapper = json.loads(raw) if isinstance(raw, str) else raw
        outer = wrapper['event']
        event = outer['envelope']['event']
        owner = outer['envelope']['owner']
        started = _time(outer.get('started_at') or event['observed_at'])
        finished = _time(event['observed_at'])
        conv_key = (outer['conversation_id'], canonical(owner))
        parsed.append({'ordinal':ordinal,'outer':outer,'event':event,'owner':owner,'wrapper':wrapper,
                       'start':min(started,finished),'end':max(started,finished),'conv_key':conv_key})
    # Ordinary 020 Evidence lookups address the aggregate root request/context.
    # Split reused historical Trace identities by native context, consistently
    # remapping both Core rows and events; the report keeps every original ID.
    trace_contexts = defaultdict(set)
    for x in parsed:
        x['source_trace_id'] = x['outer']['trace_id']
        x['trace_context'] = (x['outer']['request_id'], x['conv_key'])
        trace_contexts[x['source_trace_id']].add(x['trace_context'])
    for x in parsed:
        old = x['source_trace_id']
        mapped = old if len(trace_contexts[old]) == 1 else hashlib.sha256(canonical((old, x['trace_context'])).encode()).hexdigest()[:32]
        x['outer']['trace_id'] = mapped
        x['event']['trace_id'] = mapped
    conv_ids = _map_ids({x['conv_key'] for x in parsed},0,'conv')
    for x in parsed:
        x['conv_id'] = conv_ids[x['conv_key']]
        x['int_key'] = (x['conv_id'], x['outer']['interaction_id'])
    int_ids = _map_ids({x['int_key'] for x in parsed},1,'int')
    for x in parsed:
        x['int_id'] = int_ids[x['int_key']]
        x['op_key'] = (x['int_id'],x['outer']['operation_id'],x['outer']['request_id'])
    operation_traces=defaultdict(set)
    for x in parsed: operation_traces[x['op_key']].add(x['outer']['trace_id'])
    for x in parsed:
        if len(operation_traces[x['op_key']])>1:
            x['op_key']=x['op_key']+(x['outer']['trace_id'],)
    op_ids = _map_ids({x['op_key'] for x in parsed},1,'op')
    conversations, interactions, operations, receipts, calls = {}, {}, {}, {}, {}
    by_conversation = defaultdict(list)
    for x in parsed: by_conversation[x['conv_id']].append(x)
    for cid, source_rows in sorted(by_conversation.items()):
        rows = sorted(source_rows,key=lambda x:(x['start'],x['event']['event_id']))
        first = rows[0]; owner=first['owner']; begin=min(x['start'] for x in rows); end=max(x['end'] for x in rows)
        conversations[cid] = {'conversation_id':cid,'owner':owner,'agent_name':owner['application_principal_id'],
                              'actor_name_snapshot':actor_names.get(owner['effective_subject_id'],owner['effective_subject_id']),
                              'creation_auth_method':'unknown','external_conversation_key':first['outer']['conversation_id'],
                              'generation':1,'status':'closed','one_shot':False,'row_version':1,
                              'created_at':begin,'updated_at':end,'closed_at':end}
        groups = defaultdict(list)
        for x in rows:groups[x['int_id']].append(x)
        for ordinal,(iid,group) in enumerate(sorted(groups.items(),key=lambda pair:(min(x['start'] for x in pair[1]),pair[0])),1):
            begin=min(x['start'] for x in group); end=max(x['end'] for x in group)
            interactions[iid] = {'interaction_id':iid,'conversation_id':cid,'ordinal':ordinal,
                                 'execution_status':'completed','evidence_status':'partial','row_version':1,
                                 'lease_token':'','lease_epoch':0,'lease_version':0,'lease_expires_at':end,
                                 'created_at':begin,'updated_at':end,'terminal_at':end}
    groups = defaultdict(list)
    for x in parsed:
        x['op_id']=op_ids[x['op_key']]
        x['attempt']=int(x['outer'].get('attempt') or 1)
        x['receipt_id']=_id('rcpt',(x['op_id'],x['attempt']))
        groups[(x['op_id'],x['attempt'])].append(x)
    for (oid,attempt),rows in sorted(groups.items()):
        rows.sort(key=lambda x:(x['start'],x['event']['event_id']))
        first=rows[0]; tool=first['event'].get('bkn.operation.name') or first['event']['event_type']
        begin=min(x['start'] for x in rows); end=max(x['end'] for x in rows)
        status='failed' if any(x['event']['payload'].get('status') in ('failed','failure','denied') for x in rows) else 'completed'
        prior=operations.get(oid)
        operation={'operation_id':oid,'conversation_id':first['conv_id'],'interaction_id':first['int_id'],
                   'operation_key':oid,'tool_name':tool,'attempt':attempt,'attempt_status':status,'retryable':False,
                   'row_version':1,'created_at':begin,'updated_at':end}
        if prior:
            operation['created_at']=min(prior['created_at'],begin)
            if prior['attempt']>attempt:operation=prior
        operations[oid]=operation
        refs={}
        aliases={'object':'object_type','relation':'relation_type','action':'action_type','resource':'data_resource','kn':'knowledge_network'}
        for x in rows:
            payload=x['event']['payload']
            for key in ('business_refs','resource_refs','field_refs'):
                for ref in payload.get(key) or []:
                    if isinstance(ref,dict) and ref.get('ref_id'):
                        kind=ref.get('ref_type') or ref['ref_id'].split(':')[0]
                        refs[ref['ref_id']]={'ref_id':ref['ref_id'],'ref_type':aliases.get(kind,kind),'version':ref.get('version') or 'unversioned'}
        rid=first['receipt_id']
        receipts[rid]={'receipt_id':rid,'schema_version':'3.0.0','owner':first['owner'],
                       'conversation_id':first['conv_id'],'interaction_id':first['int_id'],'operation_id':oid,
                       'attempt':attempt,'operation_key':oid,'tool_name':tool,'receipt_status':status,
                       'evidence_durability':'durable','required':False,'request_id':first['outer']['request_id'],
                       'trace_id':first['outer']['trace_id'],'causation_event_ids':[],'observed_evidence_refs':[],
                       'business_refs':sorted(refs.values(),key=lambda r:r['ref_id']),'artifact_refs':[],
                       'partial_reasons':[],'row_version':1,'issued_at':begin,'terminal_at':end}
        payload=first['event']['payload']
        body=canonical(payload)
        envelope={'mode':'inline','media_type':'application/json','byte_length':len(body.encode()),'inline':payload}
        calls[(oid,attempt)]={'operation_id':oid,'attempt':attempt,'conversation_id':first['conv_id'],
                              'interaction_id':first['int_id'],'receipt_id':rid,'tool_name':tool,
                              'protocol':'mcp','source_module':first['event'].get('producer_module') or records[first['ordinal']]['source_id'],
                              'input':envelope,'output':envelope if status=='completed' else None,
                              'error':envelope if status=='failed' else None,'request_id':first['outer']['request_id'],
                              'trace_id':first['outer']['trace_id'],'span_id':first['event'].get('span_id') or first['outer'].get('span_id',''),
                              'started_at':begin,'finished_at':end,'status':status,'retryable':False}
    mapping=[]
    for x in sorted(parsed,key=lambda x:x['ordinal']):
        old={'span_id':x['event'].get('span_id') or x['outer'].get('span_id',''),'request_id':x['outer']['request_id'],'trace_id':x['source_trace_id'],'conversation_id':x['outer']['conversation_id'],'interaction_id':x['outer']['interaction_id'],'operation_id':x['outer']['operation_id']}
        new={'trace_id':x['outer']['trace_id'],'conversation_id':x['conv_id'],'interaction_id':x['int_id'],'operation_id':x['op_id']}
        x['outer'].update(new);x['event'].update({'interaction_id':x['int_id'],'operation_id':x['op_id']})
        converted[x['ordinal']]['row']['envelope']=x['wrapper']
        mapping.append({'source_ordinal':x['ordinal'],'event_id':x['event']['event_id'],'source':old,
                        'target':new,'receipt_id':x['receipt_id'],'span_id':old['span_id'],'request_id':old['request_id'],'defaults':{'auth_method':'unknown','protocol':'mcp','terminal_status':receipts[x['receipt_id']]['receipt_status']}})
    return {'conversations':[conversations[k] for k in sorted(conversations)],
            'interactions':[interactions[k] for k in sorted(interactions)],
            'operations':[operations[k] for k in sorted(operations)],
            'receipts':[receipts[k] for k in sorted(receipts)],
            'call_facts':[calls[k] for k in sorted(calls)],'source_map':mapping,'records':converted}


CORE_FIELDS = ('conversations', 'interactions', 'operations', 'receipts', 'call_facts')


def core_import_batches(plan, max_bytes=64 << 20, max_receipts=1000):
    """Bound stdin size while keeping each receipt and its native dependencies."""
    identifiers = ('conversation_id', 'interaction_id', 'operation_id', 'receipt_id', 'receipt_id')
    indexes = {key: {row[identity]: row for row in plan[key]}
               for key, identity in zip(CORE_FIELDS, identifiers)}
    encoded_sizes = {key: {identity: len(canonical(row).encode()) for identity, row in rows.items()}
                     for key, rows in indexes.items()}
    batch = {key: {} for key in CORE_FIELDS}
    base_size = len(canonical({key: [] for key in CORE_FIELDS}).encode())
    size = base_size
    for receipt in plan['receipts']:
        ids = (receipt['conversation_id'], receipt['interaction_id'], receipt['operation_id'],
               receipt['receipt_id'], receipt['receipt_id'])
        def added_size():
            return sum(encoded_sizes[key][identity] + bool(batch[key])
                       for key, identity in zip(CORE_FIELDS, ids) if identity not in batch[key])
        extra = added_size()
        if batch['receipts'] and (size + extra > max_bytes or len(batch['receipts']) >= max_receipts):
            yield canonical({key: list(rows.values()) for key, rows in batch.items()}).encode()
            batch = {key: {} for key in CORE_FIELDS}
            size = base_size
            extra = added_size()
        if size + extra > max_bytes:
            raise ValueError('native_core_record_exceeds_transport_limit')
        for key, identity in zip(CORE_FIELDS, ids):
            batch[key][identity] = indexes[key][identity]
        size += extra
    if batch['receipts']:
        yield canonical({key: list(rows.values()) for key, rows in batch.items()}).encode()
