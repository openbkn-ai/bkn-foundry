#!/usr/bin/env python3
import csv
import json
import sys
from pathlib import Path

def get_exchange_server_id(server_name, server_address):
    data_file = Path(__file__).parent.parent / 'data' / 'exchange_server.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if row['server_name'] == server_name and row['server_address'] == server_address:
                    return int(row['id'])
    except Exception:
        return None
    
    return None

def get_exchange_server_info(server_id):
    data_file = Path(__file__).parent.parent / 'data' / 'exchange_server.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['id']) == server_id:
                    return {
                        "id": int(row['id']),
                        "name": row['server_name'],
                        "ip": row['server_address'],
                        "status": row['status'],
                        "capacity": row['mailbox_count'],
                        "version": row['exchange_version']
                    }
    except Exception:
        return None
    
    return None

def find_backup_timepoints(server_name, server_address):
    data_file = Path(__file__).parent.parent / 'data' / 'backup_timepoint.csv'
    
    if not data_file.exists():
        return {"backup_timepoints": [], "error": "备份时间点文件不存在"}
    
    exchange_server_id = get_exchange_server_id(server_name, server_address)
    
    if exchange_server_id is None:
        return {"backup_timepoints": [], "error": f"未找到服务器: {server_name} ({server_address})"}
    
    timepoints = []
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['exchange_server_id']) == exchange_server_id:
                    metadata_str = row['metadata']
                    try:
                        metadata = json.loads(metadata_str)
                    except json.JSONDecodeError:
                        metadata = {}
                    
                    timepoint = {
                        "id": int(row['id']),
                        "exchange_server_id": int(row['exchange_server_id']),
                        "snapshot_name": row['snapshot_name'],
                        "backup_set_id": int(row['backup_set_id']),
                        "snapshot_time": row['snapshot_time'],
                        "snapshot_size": int(row['snapshot_size']),
                        "metadata": metadata,
                        "is_verified": row['is_verified'] == 'TRUE'
                    }
                    timepoints.append(timepoint)
    except Exception as e:
        return {"backup_timepoints": [], "error": f"读取备份时间点文件失败: {str(e)}"}
    
    timepoints.sort(key=lambda x: x['snapshot_time'], reverse=True)
    
    exchange_server_info = get_exchange_server_info(exchange_server_id)
    
    return {
        "server_name": server_name,
        "server_address": server_address,
        "exchange_server": exchange_server_info,
        "backup_timepoints": timepoints
    }

if __name__ == '__main__':
    if len(sys.argv) < 3:
        print(json.dumps({"error": "缺少参数：server_name server_address"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    server_name = sys.argv[1]
    server_address = sys.argv[2]
    
    result = find_backup_timepoints(server_name, server_address)
    print(json.dumps(result, ensure_ascii=False, indent=2))
