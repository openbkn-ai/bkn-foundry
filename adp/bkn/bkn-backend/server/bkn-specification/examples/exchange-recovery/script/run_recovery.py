#!/usr/bin/env python3
import sys
import json
from pathlib import Path

script_dir = Path(__file__).parent
sys.path.insert(0, str(script_dir))

from execute_recovery_task import execute_recovery_task, get_recovery_task_info
from verify_exchange_availability import verify_exchange_availability
from generate_recovery_report import generate_recovery_report

def main():
    print("=" * 80)
    print("Exchange邮件恢复流程")
    print("=" * 80)
    print()

    # Use existing task ID
    task_id = 9
    
    # Verify task exists
    task_info = get_recovery_task_info(task_id)
    if not task_info:
        print(f"Error: Recovery task {task_id} not found")
        return
    
    print(f"Found existing recovery task (ID: {task_id})")
    print(f"  Task name: {task_info['task_name']}")
    print(f"  Backup timepoint: {task_info['timepoint']}")
    print(f"  Recovery type: {task_info['recovery_type']}")
    print(f"  Recovery granularity: {task_info['recovery_granularity']}")
    print(f"  Recovery destination: {task_info['recovery_destination']}")
    print(f"  Status: {task_info['task_status']}")
    print()

    # Step 1: Execute recovery task
    print("Step 1: Executing recovery task...")
    user_id = "admin"
    
    result = execute_recovery_task(task_id, user_id)
    
    if result.get("error"):
        print(f"Error executing recovery task: {result['error']}")
        return
    
    recovery_job = result["recovery_job"]
    job_id = recovery_job["id"]
    print(f"✓ Recovery task executed successfully (Job ID: {job_id})")
    print(f"  Job name: {recovery_job['job_name']}")
    print(f"  User: {recovery_job['user_id']}")
    print(f"  Duration: {result['duration']} seconds")
    print(f"  Result: {result['recovery_result']}")
    print()

    # Step 2: Verify Exchange availability
    print("Step 2: Verifying Exchange availability...")
    result = verify_exchange_availability(job_id)
    
    if result.get("error"):
        print(f"Error verifying Exchange availability: {result['error']}")
        print("Continuing to generate report...")
    else:
        verification = result["verification"]
        print(f"✓ Exchange availability verified")
        print(f"  Verification method: {verification['verification_method']}")
        print(f"  Verification result: {verification['verification_result']}")
        print(f"  Verification time: {verification['verification_time']}")
        print()

    # Step 3: Generate recovery report
    print("Step 3: Generating recovery report...")
    result = generate_recovery_report(job_id)
    
    if result.get("error"):
        print(f"Error generating recovery report: {result['error']}")
        return
    
    report = result["report"]
    print(f"✓ Recovery report generated (Report ID: {report['report_id']})")
    print()
    
    # Print report summary
    print("=" * 80)
    print("RECOVERY REPORT SUMMARY")
    print("=" * 80)
    print(f"Report ID: {report['report_id']}")
    print(f"Recovery Status: {report['recovery_status']}")
    print(f"Verification Status: {report['verification_status']}")
    print()
    print("Task Information:")
    print(f"  Task ID: {report['task_info']['task_id']}")
    print(f"  Task Name: {report['task_info']['task_name']}")
    print(f"  Backup Timepoint: {report['task_info']['backup_timepoint']}")
    print(f"  Recovery Type: {report['task_info']['recovery_type']}")
    print(f"  Recovery Granularity: {report['task_info']['recovery_granularity']}")
    print(f"  Recovery Destination: {report['task_info']['recovery_destination']}")
    print()
    print("Job Information:")
    print(f"  Job ID: {report['job_info']['job_id']}")
    print(f"  User: {report['job_info']['user_id']}")
    print(f"  Duration: {report['job_info']['duration_formatted']}")
    print(f"  Result: {report['job_info']['recovery_result']}")
    print()
    print("Data Statistics:")
    print(f"  Email Count: {report['data_statistics']['email_count']}")
    print(f"  Total Size: {report['data_statistics']['total_size_formatted']}")
    print()
    print("Time Evaluation:")
    print(f"  Total Duration: {report['time_evaluation']['total_duration_formatted']}")
    print()
    print("Summary:")
    print(f"  {report['summary']['data_summary']}")
    print(f"  {report['summary']['time_summary']}")
    print(f"  {report['summary']['final_status']}")
    print("=" * 80)

if __name__ == '__main__':
    main()
