#!/usr/bin/env python3
import csv
import json
import sys
from pathlib import Path

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

def browse_backup_emails(backup_timepoint_id, email_subject_filter=None, email_sender_filter=None, email_date_filter=None):
    data_file = Path(__file__).parent.parent / 'data' / 'email.csv'
    
    if not data_file.exists():
        return {"emails": [], "error": "邮件文件不存在"}
    
    backup_timepoint_info = get_backup_timepoint_info(backup_timepoint_id)
    
    if backup_timepoint_info is None:
        return {"emails": [], "error": f"未找到备份时间点: {backup_timepoint_id}"}
    
    emails = []
    
    try:
        with open(data_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if int(row['backup_timepoint_id']) == backup_timepoint_id:
                    email = {
                        "id": int(row['id']),
                        "backup_timepoint_id": int(row['backup_timepoint_id']),
                        "email_subject": row['email_subject'],
                        "email_sender": row['email_sender'],
                        "email_recipient": row['email_recipient'],
                        "email_date": row['email_date'],
                        "email_size": int(row['email_size']),
                        "email_status": row['email_status'],
                        "mailbox_name": row['mailbox_name'],
                        "is_important": row['is_important'] == 'TRUE'
                    }
                    
                    if email_subject_filter and email_subject_filter.lower() not in email['email_subject'].lower():
                        continue
                    
                    if email_sender_filter and email_sender_filter.lower() not in email['email_sender'].lower():
                        continue
                    
                    if email_date_filter and email['email_date'] != email_date_filter:
                        continue
                    
                    emails.append(email)
    except Exception as e:
        return {"emails": [], "error": f"读取邮件文件失败: {str(e)}"}
    
    emails.sort(key=lambda x: x['email_date'], reverse=True)
    
    return {
        "backup_timepoint_id": backup_timepoint_id,
        "backup_timepoint": backup_timepoint_info,
        "filters": {
            "email_subject_filter": email_subject_filter,
            "email_sender_filter": email_sender_filter,
            "email_date_filter": email_date_filter
        },
        "emails": emails,
        "total_count": len(emails)
    }

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print(json.dumps({"error": "缺少参数：backup_timepoint_id [email_subject_filter] [email_sender_filter] [email_date_filter]"}, ensure_ascii=False, indent=2))
        sys.exit(1)
    
    backup_timepoint_id = int(sys.argv[1])
    
    email_subject_filter = None
    email_sender_filter = None
    email_date_filter = None
    
    if len(sys.argv) > 2:
        email_subject_filter = sys.argv[2]
    if len(sys.argv) > 3:
        email_sender_filter = sys.argv[3]
    if len(sys.argv) > 4:
        email_date_filter = sys.argv[4]
    
    if email_subject_filter and not email_subject_filter.strip():
        email_subject_filter = None
    if email_sender_filter and not email_sender_filter.strip():
        email_sender_filter = None
    if email_date_filter and not email_date_filter.strip():
        email_date_filter = None
    
    result = browse_backup_emails(backup_timepoint_id, email_subject_filter, email_sender_filter, email_date_filter)
    print(json.dumps(result, ensure_ascii=False, indent=2))
