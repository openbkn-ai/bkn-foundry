#!/usr/bin/env python3
"""
恢复工具箱
提供Exchange邮件恢复相关的所有工具
"""

from typing import Dict, Any, List, Optional
from pathlib import Path
import subprocess
import json

try:
    from .base_toolbox import BaseToolbox
except ImportError:
    from base_toolbox import BaseToolbox


class RecoveryToolbox(BaseToolbox):
    """Exchange邮件恢复工具箱"""
    
    def __init__(self, data_access_layer=None):
        """
        初始化恢复工具箱
        
        Args:
            data_access_layer: 数据访问层实例（保留兼容性，但实际使用script/下的工具）
        """
        super().__init__(data_access_layer)
        self.toolbox_id = "recovery_toolbox"
        self.toolbox_name = "恢复工具箱"
        self.version = "2.0.0"
        self.script_dir = Path(__file__).parent.parent / 'script'
    
    def get_toolbox_info(self) -> Dict[str, Any]:
        """获取工具箱信息"""
        return {
            "toolbox_id": self.toolbox_id,
            "toolbox_name": self.toolbox_name,
            "version": self.version,
            "description": "提供Exchange邮件恢复的完整功能，包括数据域管理、服务器管理、备份查找、邮件浏览、恢复任务执行、可用性验证和报告生成",
            "available_tools": self.get_available_tools()
        }
    
    def get_available_tools(self) -> List[str]:
        """获取可用工具列表"""
        return [
            "list_data_domains",
            "list_exchange_servers",
            "find_backup_timepoints",
            "browse_backup_emails",
            "execute_recovery_task",
            "verify_exchange_availability",
            "generate_recovery_report"
        ]
    
    def _run_script(self, script_name: str, args: List[str]) -> Dict[str, Any]:
        """
        运行script目录下的脚本
        
        Args:
            script_name: 脚本文件名
            args: 命令行参数列表
            
        Returns:
            脚本执行结果
        """
        try:
            script_path = self.script_dir / script_name
            
            if not script_path.exists():
                return {
                    "success": False,
                    "error": f"脚本不存在: {script_path}"
                }
            
            result = subprocess.run(
                ['python', str(script_path)] + args,
                capture_output=True,
                text=True,
                encoding='utf-8',
                errors='ignore'
            )
            
            if result.returncode != 0:
                return {
                    "success": False,
                    "error": f"脚本执行失败: {result.stderr}",
                    "returncode": result.returncode
                }
            
            output = result.stdout.strip()
            
            if not output:
                return {
                    "success": False,
                    "error": "脚本无输出"
                }
            
            try:
                data = json.loads(output)
                return {
                    "success": True,
                    "data": data
                }
            except json.JSONDecodeError:
                return {
                    "success": True,
                    "data": output
                }
        except Exception as e:
            return {
                "success": False,
                "error": f"运行脚本异常: {str(e)}",
                "exception": str(e)
            }
    
    def list_data_domains(self, **kwargs) -> Dict[str, Any]:
        """
        列出数据域列表
        
        Returns:
            数据域列表
        """
        result = self._run_script('list_data_domains.py', [])
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
    
    def list_exchange_servers(self, data_domain_id: int, **kwargs) -> Dict[str, Any]:
        """
        列出Exchange服务器列表
        
        Args:
            data_domain_id: 数据域ID
            
        Returns:
            Exchange服务器列表
        """
        result = self._run_script('list_exchange_servers.py', [str(data_domain_id)])
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
    
    def find_backup_timepoints(self, server_name: str, server_address: str, **kwargs) -> Dict[str, Any]:
        """
        查找备份时间点副本
        
        Args:
            server_name: Exchange服务器名称
            server_address: Exchange服务器地址
            
        Returns:
            备份时间点列表
        """
        result = self._run_script('find_backup_timepoints.py', [server_name, server_address])
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
    
    def browse_backup_emails(self, backup_timepoint_id: int, 
                            email_subject_filter: Optional[str] = None,
                            email_sender_filter: Optional[str] = None,
                            email_date_filter: Optional[str] = None,
                            **kwargs) -> Dict[str, Any]:
        """
        浏览备份邮件
        
        Args:
            backup_timepoint_id: 备份时间点ID
            email_subject_filter: 邮件主题过滤（可选）
            email_sender_filter: 邮件发件人过滤（可选）
            email_date_filter: 邮件日期过滤（可选）
            
        Returns:
            邮件列表
        """
        args = [str(backup_timepoint_id)]
        
        if email_subject_filter:
            args.append(email_subject_filter)
        else:
            args.append("")
        
        if email_sender_filter:
            args.append(email_sender_filter)
        else:
            args.append("")
        
        if email_date_filter:
            args.append(email_date_filter)
        else:
            args.append("")
        
        result = self._run_script('browse_backup_emails.py', args)
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
    
    def execute_recovery_task(self, task_id: int, user_id: str, **kwargs) -> Dict[str, Any]:
        """
        执行恢复任务
        
        Args:
            task_id: 恢复任务ID
            user_id: 执行恢复操作的用户ID
            
        Returns:
            恢复作业信息
        """
        result = self._run_script('execute_recovery_task.py', [str(task_id), user_id])
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
    
    def verify_exchange_availability(self, recovery_job_id: int, **kwargs) -> Dict[str, Any]:
        """
        验证Exchange可用性
        
        Args:
            recovery_job_id: 恢复作业ID
            
        Returns:
            验证结果
        """
        result = self._run_script('verify_exchange_availability.py', [str(recovery_job_id)])
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
    
    def generate_recovery_report(self, recovery_job_id: int, **kwargs) -> Dict[str, Any]:
        """
        生成恢复报告
        
        Args:
            recovery_job_id: 恢复作业ID
            
        Returns:
            恢复报告
        """
        result = self._run_script('generate_recovery_report.py', [str(recovery_job_id)])
        
        if result['success']:
            return result['data']
        else:
            return {
                "success": False,
                "error": result['error']
            }
