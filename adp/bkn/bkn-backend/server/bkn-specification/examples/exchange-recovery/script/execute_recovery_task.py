#!/usr/bin/env python3
import csv
import json
import sys
import time
from pathlib import Path
from datetime import datetime

def get_recovery_task_info(task_id):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_task.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['id']) == task_id:
                    return {
                        "id": int(row['id']),
                        "backup_timepoint_id": int(row['backup_timepoint_id']),
                        "task_name": row['task_name'],
                        "timepoint": row['timepoint'],
                        "recovery_type": row['recovery_type'],
                        "recovery_granularity": row['recovery_granularity'],
                        "recovery_destination": row['recovery_destination'],
                        "task_status": row['task_status'],
                        "created_at": row['created_at'],
                        "completed_at": row['completed_at'],
                        "recovery_result": row['recovery_result']
                    }
    except Exception:
        return None
    
    return None

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

def update_task_status(task_id, status, recovery_result=None):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_task.csv'
    
    if not data_file.exists():
        return False
    
    try:
        rows = []
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['id']) == task_id:
                    row['task_status'] = status
                    if recovery_result:
                        row['recovery_result'] = recovery_result
                    if status == 'Completed':
                        row['completed_at'] = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
                rows.append(row)
        
        fieldnames = ['id', 'backup_timepoint_id', 'task_name', 'timepoint', 'recovery_type', 'recovery_granularity', 'recovery_destination', 'task_status', 'created_at', 'completed_at', 'recovery_result']
        with open(data_file, 'w', encoding='utf-8', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()
            writer.writerows(rows)
        return True
    except Exception:
        return False

def get_next_job_id():
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_job.csv'
    
    if not data_file.exists():
        return 1
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            max_id = 0
            for row in reader:
                try:
                    job_id = int(row['id'])
                    if job_id > max_id:
                        max_id = job_id
                except ValueError:
                    continue
            return max_id + 1
    except Exception:
        return 1

def create_recovery_job(recovery_task_id, user_id, duration, recovery_result, error_message=None):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_job.csv'
    
    task_info = get_recovery_task_info(recovery_task_id)
    if task_info is None:
        return None
    
    job_id = get_next_job_id()
    execution_time = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    end_time = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    
    job_name = f"恢复作业-{task_info['task_name']}"
    
    job = {
        "id": job_id,
        "recovery_task_id": recovery_task_id,
        "job_name": job_name,
        "user_id": user_id,
        "execution_time": execution_time,
        "end_time": end_time,
        "duration": duration,
        "recovery_result": recovery_result,
        "error_message": error_message or ""
    }
    
    try:
        file_exists = data_file.exists()
        fieldnames = ['id', 'recovery_task_id', 'job_name', 'user_id', 'execution_time', 'end_time', 'duration', 'recovery_result', 'error_message']
        with open(data_file, 'a', encoding='utf-8', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            if not file_exists:
                writer.writeheader()
            writer.writerow(job)
        return job
    except Exception:
        return None

def execute_recovery_task(task_id, user_id):
    task_info = get_recovery_task_info(task_id)
    
    if task_info is None:
        return {"recovery_job": None, "error": f"未找到恢复任务: {task_id}"}
    
    if task_info['task_status'] != 'Pending':
        return {"recovery_job": None, "error": f"恢复任务状态不是Pending，当前状态: {task_info['task_status']}"}
    
    backup_timepoint_info = get_backup_timepoint_info(task_info['backup_timepoint_id'])
    
    if backup_timepoint_info is None:
        return {"recovery_job": None, "error": f"未找到备份时间点: {task_info['backup_timepoint_id']}"}
    
    if not backup_timepoint_info['is_verified']:
        return {"recovery_job": None, "error": "备份时间点未验证，恢复可能失败或数据不完整"}
    
    if task_info['recovery_destination'] == 'production':
        return {"recovery_job": None, "error": "恢复目的地为生产服务器，会覆盖现有生产数据，需要用户确认"}
    
    if not update_task_status(task_id, 'Running'):
        return {"recovery_job": None, "error": "更新任务状态失败"}
    
    execution_time = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    
    time.sleep(2)
    
    duration = 2
    
    recovery_result = "成功"
    error_message = None
    
    if task_info['recovery_granularity'] == '邮件服务器':
        pass
    elif task_info['recovery_granularity'] == '邮件级别':
        pass
    
    if task_info['recovery_type'] == '挂载恢复':
        pass
    elif task_info['recovery_type'] == '数据恢复':
        pass
    
    job = create_recovery_job(task_id, user_id, duration, recovery_result, error_message)
    
    if job is None:
        update_task_status(task_id, 'Failed', 'Failed')
        return {"recovery_job": None, "error": "创建恢复作业失败"}
    
    if not update_task_status(task_id, 'Completed', recovery_result):
        return {"recovery_job": None, "error": "更新任务状态失败"}
    
    return {
        "recovery_job": job,
        "recovery_task": task_info,
        "backup_timepoint": backup_timepoint_info,
        "execution_time": execution_time,
        "end_time": job['end_time'],
        "duration": duration,
        "recovery_result": recovery_result
    }

if __name__ == '__main__':
    if len(sys.argv) < 3:
        print(json.dumps({"error": "缺少参数：task_id user_id"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    task_id = int(sys.argv[1])
    user_id = sys.argv[2]
    
    result = execute_recovery_task(task_id, user_id)
    print(json.dumps(result, ensure_ascii=False, indent=2))
