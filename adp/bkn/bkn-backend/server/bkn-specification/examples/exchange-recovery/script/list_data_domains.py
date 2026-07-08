#!/usr/bin/env python3
import csv
import json
from pathlib import Path

def list_data_domains():
    script_dir = Path(__file__).parent
    data_file = script_dir.parent / 'data' / 'data_domain.csv'
    
    if not data_file.exists():
        return {"data_domains": [], "error": "数据域文件不存在"}
    
    domains = []
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                details_str = row['details']
                try:
                    details = json.loads(details_str)
                except json.JSONDecodeError:
                    details = {}
                
                domain = {
                    "id": int(row['id']),
                    "domain_name": row['domain_name'],
                    "domain_ip": row['domain_ip'],
                    "domain_type": row['domain_type'],
                    "status": row['status'],
                    "details": details,
                    "created_time": row['created_time'],
                    "updated_time": row['updated_time']
                }
                domains.append(domain)
    except Exception as e:
        return {"data_domains": [], "error": f"读取数据域文件失败: {str(e)}"}
    
    domains.sort(key=lambda x: x['domain_name'])
    
    return {"data_domains": domains}

if __name__ == '__main__':
    result = list_data_domains()
    print(json.dumps(result, ensure_ascii=False, indent=2))
