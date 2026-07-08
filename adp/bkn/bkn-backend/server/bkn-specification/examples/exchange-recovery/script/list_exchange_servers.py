#!/usr/bin/env python3
import csv
import json
import sys
from pathlib import Path

def get_data_domain_name(data_domain_id):
    data_file = Path(__file__).parent.parent / 'data' / 'data_domain.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['id']) == data_domain_id:
                    return row['domain_name']
    except Exception:
        return None
    
    return None

def list_exchange_servers(data_domain_id):
    data_file = Path(__file__).parent.parent / 'data' / 'exchange_server.csv'
    
    if not data_file.exists():
        return {"exchange_servers": [], "error": "Exchange服务器文件不存在"}
    
    servers = []
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['data_domain_id']) == data_domain_id:
                    server = {
                        "id": int(row['id']),
                        "name": row['server_name'],
                        "ip": row['server_address'],
                        "status": row['status'],
                        "capacity": row['mailbox_count'],
                        "version": row['exchange_version']
                    }
                    servers.append(server)
    except Exception as e:
        return {"exchange_servers": [], "error": f"读取Exchange服务器文件失败: {str(e)}"}
    
    servers.sort(key=lambda x: x['name'])
    
    data_domain_name = get_data_domain_name(data_domain_id)
    
    return {
        "data_domain_id": data_domain_id,
        "data_domain_name": data_domain_name,
        "exchange_servers": servers
    }

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print(json.dumps({"error": "缺少参数：data_domain_id"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    try:
        data_domain_id = int(sys.argv[1])
    except ValueError:
        print(json.dumps({"error": "data_domain_id必须是整数"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    result = list_exchange_servers(data_domain_id)
    print(json.dumps(result, ensure_ascii=False, indent=2))
