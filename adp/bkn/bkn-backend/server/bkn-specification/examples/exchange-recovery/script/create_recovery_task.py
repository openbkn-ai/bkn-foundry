#!/usr/bin/env python3
import csv
import json
import sys
from pathlib import Path
from datetime import datetime

def get_backup_timepoint_info(backup_timepoint_id):
    data_file = Path(__file__).parent.parent / 'data' / 'backup_timepoint.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['id']) == backup_timepoint_id:
                    metadata_str = row['metadata']
                    try:
                        metadata = json.loads(metadata_str)
                    except json.JSONDecodeError:
                        metadata = {}
                    
                    return {
                        "id": int(row['id']),
                        "exchange_server_id": int(row['exchange_server_id']),
                        "snapshot_name": row['snapshot_name'],
                        "backup_set_id": int(row['backup_set_id']),
                        "snapshot_time": row['snapshot_time'],
                        "snapshot_size": int(row['snapshot_size']),
                        "metadata": metadata,
                        "is_verified": row['is_verified'] == 'TRUE'
                    }
    except Exception:
        return None
    
    return None

def get_next_task_id():
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_task.csv'
    
    if not data_file.exists():
        return 1
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            max_id = 0
            for row in reader:
                try:
                    task_id = int(row['id'])
                    if task_id > max_id:
                        max_id = task_id
                except ValueError:
                    continue
            return max_id + 1
    except Exception:
        return 1

def create_recovery_task(backup_timepoint_id, recovery_type, recovery_granularity, recovery_destination, target_emails=None):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_task.csv'
    
    backup_timepoint_info = get_backup_timepoint_info(backup_timepoint_id)
    
    if backup_timepoint_info is None:
        return {"recovery_task": None, "error": f"未找到备份时间点: {backup_timepoint_id}"}
    
    task_id = get_next_task_id()
    created_at = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    
    task_name = f"恢复任务-{backup_timepoint_info['snapshot_name']}-{recovery_granularity}"
    
    task = {
        "id": task_id,
        "backup_timepoint_id": backup_timepoint_id,
        "task_name": task_name,
        "timepoint": backup_timepoint_info['snapshot_time'],
        "recovery_type": recovery_type,
        "recovery_granularity": recovery_granularity,
        "recovery_destination": recovery_destination,
        "task_status": "Pending",
        "created_at": created_at,
        "completed_at": None,
        "recovery_result": None
    }
    
    try:
        file_exists = data_file.exists()
        fieldnames = ['id', 'backup_timepoint_id', 'task_name', 'timepoint', 'recovery_type', 'recovery_granularity', 'recovery_destination', 'task_status', 'created_at', 'completed_at', 'recovery_result']
        with open(data_file, 'a', encoding='utf-8', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            if not file_exists or data_file.stat().st_size == 0:
                writer.writeheader()
            writer.writerow(task)
    except Exception as e:
        return {"recovery_task": None, "error": f"写入恢复任务文件失败: {str(e)}"}
    
    return {
        "recovery_task": task,
        "backup_timepoint": backup_timepoint_info,
        "target_emails": target_emails
    }

if __name__ == '__main__':
    if len(sys.argv) < 5:
        print(json.dumps({"error": "缺少参数：backup_timepoint_id recovery_type recovery_granularity recovery_destination [target_emails_json]"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    backup_timepoint_id = int(sys.argv[1])
    recovery_type = sys.argv[2]
    recovery_granularity = sys.argv[3]
    recovery_destination = sys.argv[4]
    
    target_emails = None
    if len(sys.argv) > 5:
        try:
            target_emails = json.loads(sys.argv[5])
        except json.JSONDecodeError:
            print(json.dumps({"error": "target_emails参数必须是有效的JSON数组"}, ensure_ascii=False, indent=2))
            sys.exit(1)
    
    result = create_recovery_task(backup_timepoint_id, recovery_type, recovery_granularity, recovery_destination, target_emails)
    print(json.dumps(result, ensure_ascii=False, indent=2))
