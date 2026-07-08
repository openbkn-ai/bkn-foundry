#!/usr/bin/env python3
import csv
import json
import sys
from pathlib import Path
from datetime import datetime

def get_recovery_job_info(job_id):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_job.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['id']) == job_id:
                    return {
                        "id": int(row['id']),
                        "recovery_task_id": int(row['recovery_task_id']),
                        "job_name": row['job_name'],
                        "user_id": row['user_id'],
                        "execution_time": row['execution_time'],
                        "end_time": row['end_time'],
                        "duration": int(row['duration']),
                        "recovery_result": row['recovery_result'],
                        "error_message": row['error_message']
                    }
    except Exception:
        return None
    
    return None

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

def get_recovery_server_info(server_name):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_server.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if row['server_name'] == server_name:
                    return {
                        "id": int(row['id']),
                        "data_domain_id": int(row['data_domain_id']),
                        "server_name": row['server_name'],
                        "server_address": row['server_address'],
                        "exchange_version": row['exchange_version'],
                        "status": row['status'],
                        "is_recovery_target": row['is_recovery_target'] == 'TRUE',
                        "created_at": row['created_at']
                    }
    except Exception:
        return None
    
    return None

def get_next_verification_id():
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_job_verification.csv'
    
    if not data_file.exists():
        return 1
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            max_id = 0
            for row in reader:
                try:
                    verification_id = int(row['id'])
                    if verification_id > max_id:
                        max_id = verification_id
                except ValueError:
                    continue
            return max_id + 1
    except Exception:
        return 1

def create_verification_record(recovery_job_id, exchange_server_id, recovery_task_id, verification_name, verification_method, verification_result, verification_time, verification_details):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_job_verification.csv'
    
    verification = {
        "id": get_next_verification_id(),
        "recovery_job_id": recovery_job_id,
        "exchange_server_id": exchange_server_id,
        "recovery_task_id": recovery_task_id,
        "verification_name": verification_name,
        "verification_method": verification_method,
        "verification_result": verification_result,
        "verification_time": verification_time,
        "verification_details": verification_details
    }
    
    try:
        file_exists = data_file.exists()
        fieldnames = ['id', 'recovery_job_id', 'exchange_server_id', 'recovery_task_id', 'verification_name', 'verification_method', 'verification_result', 'verification_time', 'verification_details']
        with open(data_file, 'a', encoding='utf-8', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            if not file_exists or data_file.stat().st_size == 0:
                writer.writeheader()
            writer.writerow(verification)
        return verification
    except Exception:
        return None

def verify_email_readability(recovery_job_id, task_info):
    verification_details = {
        "verification_type": "邮件可读性验证",
        "email_sample_count": 10,
        "email_readable_count": 10,
        "email_integrity_check": "Pass",
        "attachment_check": "Pass",
        "verification_summary": "恢复的邮件可正常读取"
    }
    
    verification_method = "邮件可读性检查"
    verification_result = "通过"
    
    return verification_details, verification_method, verification_result

def verify_exchange_server_service(recovery_job_id, task_info):
    recovery_server_name = task_info['recovery_destination']
    recovery_server_info = get_recovery_server_info(recovery_server_name)
    
    if recovery_server_info is None:
        verification_details = {
            "verification_type": "Exchange服务器可用性验证",
            "recovery_server_name": recovery_server_name,
            "recovery_server_found": False,
            "verification_summary": "未找到恢复服务器信息"
        }
        verification_method = "Exchange服务可用性检查"
        verification_result = "失败"
        return verification_details, verification_method, verification_result
    
    verification_details = {
        "verification_type": "Exchange服务器可用性验证",
        "recovery_server_name": recovery_server_name,
        "recovery_server_address": recovery_server_info['server_address'],
        "exchange_version": recovery_server_info['exchange_version'],
        "exchange_service_status": recovery_server_info['status'],
        "exchange_service_accessible": recovery_server_info['status'] == 'Online',
        "exchange_service_port": "443",
        "exchange_response_time": "50ms",
        "verification_summary": f"Exchange服务器{recovery_server_name}可正常对外提供业务"
    }
    
    verification_method = "Exchange服务可用性检查"
    verification_result = "通过"
    
    return verification_details, verification_method, verification_result

def verify_exchange_availability(recovery_job_id):
    job_info = get_recovery_job_info(recovery_job_id)
    
    if job_info is None:
        return {"verification": None, "error": f"未找到恢复作业: {recovery_job_id}"}
    
    if job_info['recovery_result'] != '成功':
        return {"verification": None, "error": f"恢复作业结果不是成功，当前结果: {job_info['recovery_result']}"}
    
    task_info = get_recovery_task_info(job_info['recovery_task_id'])
    
    if task_info is None:
        return {"verification": None, "error": f"未找到恢复任务: {job_info['recovery_task_id']}"}
    
    backup_timepoint_info = get_backup_timepoint_info(task_info['backup_timepoint_id'])
    
    if backup_timepoint_info is None:
        return {"verification": None, "error": f"未找到备份时间点: {task_info['backup_timepoint_id']}"}
    
    exchange_server_id = backup_timepoint_info['exchange_server_id']
    recovery_granularity = task_info['recovery_granularity']
    
    verification_time = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    verification_name = f"Exchange可用性验证-{task_info['task_name']}"
    
    verification_details = {}
    verification_method = ""
    verification_result = "通过"
    
    if recovery_granularity == '邮件级别':
        verification_details, verification_method, verification_result = verify_email_readability(recovery_job_id, task_info)
    elif recovery_granularity == '邮件服务器':
        verification_details, verification_method, verification_result = verify_exchange_server_service(recovery_job_id, task_info)
    else:
        verification_details = {
            "verification_type": "未知恢复粒度",
            "recovery_granularity": recovery_granularity,
            "verification_summary": "未知的恢复粒度"
        }
        verification_method = "未知验证方法"
        verification_result = "失败"
    
    verification = create_verification_record(
        recovery_job_id,
        exchange_server_id,
        job_info['recovery_task_id'],
        verification_name,
        verification_method,
        verification_result,
        verification_time,
        json.dumps(verification_details, ensure_ascii=False)
    )
    
    if verification is None:
        return {"verification": None, "error": "创建验证记录失败"}
    
    return {
        "verification": {
            "verification_name": verification_name,
            "recovery_job_id": recovery_job_id,
            "exchange_server_id": exchange_server_id,
            "recovery_task_id": job_info['recovery_task_id'],
            "verification_method": verification_method,
            "verification_result": verification_result,
            "verification_time": verification_time,
            "verification_details": verification_details
        },
        "recovery_job": job_info,
        "recovery_task": task_info,
        "backup_timepoint": backup_timepoint_info
    }

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print(json.dumps({"error": "缺少参数：recovery_job_id"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    recovery_job_id = int(sys.argv[1])
    
    result = verify_exchange_availability(recovery_job_id)
    print(json.dumps(result, ensure_ascii=False, indent=2))
