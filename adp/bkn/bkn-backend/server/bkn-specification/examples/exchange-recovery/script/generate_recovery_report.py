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

def get_recovery_verification_info(job_id):
    data_file = Path(__file__).parent.parent / 'data' / 'recovery_job_verification.csv'
    
    if not data_file.exists():
        return None
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            verifications = []
            for row in reader:
                if int(row['recovery_job_id']) == job_id:
                    verification_details_str = row['verification_details']
                    try:
                        verification_details = json.loads(verification_details_str)
                    except json.JSONDecodeError:
                        verification_details = {}
                    
                    verifications.append({
                        "id": int(row['id']),
                        "recovery_job_id": int(row['recovery_job_id']),
                        "exchange_server_id": int(row['exchange_server_id']),
                        "recovery_task_id": int(row['recovery_task_id']),
                        "verification_name": row['verification_name'],
                        "verification_method": row['verification_method'],
                        "verification_result": row['verification_result'],
                        "verification_time": row['verification_time'],
                        "verification_details": verification_details
                    })
            
            return verifications if verifications else None
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
                        "data_domain_id": int(row['data_domain_id']),
                        "server_name": row['server_name'],
                        "server_address": row['server_address'],
                        "exchange_version": row['exchange_version'],
                        "status": row['status'],
                        "created_at": row['created_at']
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

def format_size(size_bytes):
    if size_bytes >= 1024 * 1024:
        return f"{size_bytes / (1024 * 1024):.2f} MB"
    elif size_bytes >= 1024:
        return f"{size_bytes / 1024:.2f} KB"
    else:
        return f"{size_bytes} Bytes"

def format_duration(seconds):
    if seconds >= 3600:
        hours = seconds // 3600
        minutes = (seconds % 3600) // 60
        return f"{hours}小时{minutes}分钟"
    elif seconds >= 60:
        minutes = seconds // 60
        secs = seconds % 60
        return f"{minutes}分钟{secs}秒"
    else:
        return f"{seconds}秒"

def generate_recovery_report(recovery_job_id):
    job_info = get_recovery_job_info(recovery_job_id)
    
    if job_info is None:
        return {"report": None, "error": f"未找到恢复作业: {recovery_job_id}"}
    
    task_info = get_recovery_task_info(job_info['recovery_task_id'])
    
    if task_info is None:
        return {"report": None, "error": f"未找到恢复任务: {job_info['recovery_task_id']}"}
    
    backup_timepoint_info = get_backup_timepoint_info(task_info['backup_timepoint_id'])
    
    if backup_timepoint_info is None:
        return {"report": None, "error": f"未找到备份时间点: {task_info['backup_timepoint_id']}"}
    
    exchange_server_info = get_exchange_server_info(backup_timepoint_info['exchange_server_id'])
    
    recovery_server_info = get_recovery_server_info(task_info['recovery_destination'])
    
    verification_info = get_recovery_verification_info(recovery_job_id)
    
    metadata = backup_timepoint_info['metadata']
    email_count = int(metadata.get('email_count', 0))
    snapshot_size = backup_timepoint_info['snapshot_size']
    
    task_created_time = datetime.strptime(task_info['created_at'], '%Y-%m-%d %H:%M:%S')
    job_end_time = datetime.strptime(job_info['end_time'], '%Y-%m-%d %H:%M:%S')
    total_duration = int((job_end_time - task_created_time).total_seconds())
    
    recovery_status = "成功"
    if job_info['recovery_result'] != '成功':
        recovery_status = "失败"
    elif job_info['error_message']:
        recovery_status = "部分成功"
    
    verification_status = "未验证"
    if verification_info:
        all_passed = all(v['verification_result'] == '通过' for v in verification_info)
        verification_status = "通过" if all_passed else "失败"
    
    report = {
        "report_id": f"RPT-{datetime.now().strftime('%Y%m%d%H%M%S')}",
        "recovery_job_id": recovery_job_id,
        "generated_at": datetime.now().strftime('%Y-%m-%d %H:%M:%S'),
        "recovery_status": recovery_status,
        "verification_status": verification_status,
        "task_info": {
            "task_id": task_info['id'],
            "task_name": task_info['task_name'],
            "backup_timepoint": task_info['timepoint'],
            "recovery_type": task_info['recovery_type'],
            "recovery_granularity": task_info['recovery_granularity'],
            "recovery_destination": task_info['recovery_destination'],
            "task_created_at": task_info['created_at'],
            "task_completed_at": task_info['completed_at']
        },
        "job_info": {
            "job_id": job_info['id'],
            "job_name": job_info['job_name'],
            "user_id": job_info['user_id'],
            "execution_time": job_info['execution_time'],
            "end_time": job_info['end_time'],
            "duration": job_info['duration'],
            "duration_formatted": format_duration(job_info['duration']),
            "recovery_result": job_info['recovery_result'],
            "error_message": job_info['error_message']
        },
        "data_statistics": {
            "email_count": email_count,
            "total_size": snapshot_size,
            "total_size_formatted": format_size(snapshot_size),
            "average_email_size": format_size(snapshot_size / email_count) if email_count > 0 else "0 Bytes"
        },
        "time_evaluation": {
            "task_creation_time": task_info['created_at'],
            "job_completion_time": job_info['end_time'],
            "total_duration": total_duration,
            "total_duration_formatted": format_duration(total_duration)
        },
        "server_info": {
            "source_server": {
                "server_name": exchange_server_info['server_name'] if exchange_server_info else "未知",
                "server_address": exchange_server_info['server_address'] if exchange_server_info else "未知",
                "exchange_version": exchange_server_info['exchange_version'] if exchange_server_info else "未知"
            },
            "destination_server": {
                "server_name": recovery_server_info['server_name'] if recovery_server_info else "未知",
                "server_address": recovery_server_info['server_address'] if recovery_server_info else "未知",
                "exchange_version": recovery_server_info['exchange_version'] if recovery_server_info else "未知"
            }
        },
        "verification_info": {
            "has_verification": verification_info is not None,
            "verification_count": len(verification_info) if verification_info else 0,
            "verifications": verification_info if verification_info else []
        } if verification_info else {
            "has_verification": False,
            "verification_count": 0,
            "verifications": []
        },
        "backup_timepoint_info": {
            "snapshot_name": backup_timepoint_info['snapshot_name'],
            "snapshot_time": backup_timepoint_info['snapshot_time'],
            "snapshot_size": backup_timepoint_info['snapshot_size'],
            "snapshot_size_formatted": format_size(backup_timepoint_info['snapshot_size']),
            "is_verified": backup_timepoint_info['is_verified']
        },
        "summary": {
            "recovery_status": recovery_status,
            "verification_status": verification_status,
            "data_summary": f"恢复邮件{email_count}封，总数据量{format_size(snapshot_size)}",
            "time_summary": f"业务恢复总耗时{format_duration(total_duration)}",
            "final_status": f"恢复{recovery_status}，可用性验证{verification_status}"
        }
    }
    
    return {
        "report": report,
        "recovery_job": job_info,
        "recovery_task": task_info,
        "backup_timepoint": backup_timepoint_info
    }

def format_report_text(report):
    lines = []
    lines.append("=" * 80)
    lines.append("Exchange邮件恢复报告")
    lines.append("=" * 80)
    lines.append("")
    
    lines.append(f"报告ID: {report['report_id']}")
    lines.append(f"恢复作业ID: {report['recovery_job_id']}")
    lines.append(f"生成时间: {report['generated_at']}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("一、恢复状态")
    lines.append("-" * 80)
    lines.append(f"恢复状态: {report['recovery_status']}")
    lines.append(f"可用性验证: {report['verification_status']}")
    lines.append(f"最终状态: {report['summary']['final_status']}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("二、数据统计")
    lines.append("-" * 80)
    lines.append(f"恢复邮件数量: {report['data_statistics']['email_count']}封")
    lines.append(f"总数据量: {report['data_statistics']['total_size_formatted']}")
    lines.append(f"平均邮件大小: {report['data_statistics']['average_email_size']}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("三、时效评估")
    lines.append("-" * 80)
    lines.append(f"任务创建时间: {report['time_evaluation']['task_creation_time']}")
    lines.append(f"作业完成时间: {report['time_evaluation']['job_completion_time']}")
    lines.append(f"业务恢复总耗时: {report['time_evaluation']['total_duration_formatted']}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("四、任务信息")
    lines.append("-" * 80)
    lines.append(f"任务ID: {report['task_info']['task_id']}")
    lines.append(f"任务名称: {report['task_info']['task_name']}")
    lines.append(f"备份时间点: {report['task_info']['backup_timepoint']}")
    lines.append(f"恢复类型: {report['task_info']['recovery_type']}")
    lines.append(f"恢复粒度: {report['task_info']['recovery_granularity']}")
    lines.append(f"恢复目的地: {report['task_info']['recovery_destination']}")
    lines.append(f"任务创建时间: {report['task_info']['task_created_at']}")
    lines.append(f"任务完成时间: {report['task_info']['task_completed_at']}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("五、作业信息")
    lines.append("-" * 80)
    lines.append(f"作业ID: {report['job_info']['job_id']}")
    lines.append(f"作业名称: {report['job_info']['job_name']}")
    lines.append(f"执行用户: {report['job_info']['user_id']}")
    lines.append(f"执行时间: {report['job_info']['execution_time']}")
    lines.append(f"结束时间: {report['job_info']['end_time']}")
    lines.append(f"恢复耗时: {report['job_info']['duration_formatted']}")
    lines.append(f"恢复结果: {report['job_info']['recovery_result']}")
    lines.append(f"错误信息: {report['job_info']['error_message'] if report['job_info']['error_message'] else '无'}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("六、服务器信息")
    lines.append("-" * 80)
    lines.append("源服务器:")
    lines.append(f"  服务器名称: {report['server_info']['source_server']['server_name']}")
    lines.append(f"  服务器地址: {report['server_info']['source_server']['server_address']}")
    lines.append(f"  Exchange版本: {report['server_info']['source_server']['exchange_version']}")
    lines.append("")
    lines.append("目标服务器:")
    lines.append(f"  服务器名称: {report['server_info']['destination_server']['server_name']}")
    lines.append(f"  服务器地址: {report['server_info']['destination_server']['server_address']}")
    lines.append(f"  Exchange版本: {report['server_info']['destination_server']['exchange_version']}")
    lines.append("")
    
    lines.append("-" * 80)
    lines.append("七、备份时间点信息")
    lines.append("-" * 80)
    lines.append(f"快照名称: {report['backup_timepoint_info']['snapshot_name']}")
    lines.append(f"快照时间: {report['backup_timepoint_info']['snapshot_time']}")
    lines.append(f"快照大小: {report['backup_timepoint_info']['snapshot_size_formatted']}")
    lines.append(f"已验证: {'是' if report['backup_timepoint_info']['is_verified'] else '否'}")
    lines.append("")
    
    if report['verification_info']['has_verification']:
        lines.append("-" * 80)
        lines.append("八、验证信息")
        lines.append("-" * 80)
        lines.append(f"验证数量: {report['verification_info']['verification_count']}")
        lines.append("")
        for idx, verification in enumerate(report['verification_info']['verifications'], 1):
            lines.append(f"验证{idx}:")
            lines.append(f"  验证ID: {verification['id']}")
            lines.append(f"  验证方法: {verification['verification_method']}")
            lines.append(f"  验证结果: {verification['verification_result']}")
            lines.append(f"  验证时间: {verification['verification_time']}")
            if verification['verification_details']:
                lines.append(f"  验证详情:")
                for key, value in verification['verification_details'].items():
                    lines.append(f"    {key}: {value}")
            lines.append("")
    
    lines.append("-" * 80)
    lines.append("九、总结")
    lines.append("-" * 80)
    lines.append(f"恢复状态: {report['summary']['recovery_status']}")
    lines.append(f"可用性验证: {report['summary']['verification_status']}")
    lines.append(f"数据摘要: {report['summary']['data_summary']}")
    lines.append(f"时间摘要: {report['summary']['time_summary']}")
    lines.append(f"最终状态: {report['summary']['final_status']}")
    lines.append("")
    
    lines.append("=" * 80)
    lines.append("报告结束")
    lines.append("=" * 80)
    
    return "\n".join(lines)

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print(json.dumps({"error": "缺少参数：recovery_job_id"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    recovery_job_id = int(sys.argv[1])
    
    result = generate_recovery_report(recovery_job_id)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    
    if result.get('report'):
        print("\n" + "=" * 80)
        print("文本格式报告:")
        print("=" * 80)
        print(format_report_text(result['report']))
