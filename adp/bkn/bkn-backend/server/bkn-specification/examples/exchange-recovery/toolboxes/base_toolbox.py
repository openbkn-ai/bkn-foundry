#!/usr/bin/env python3
"""
基础工具箱抽象类
定义所有工具箱的通用接口和功能
"""

from abc import ABC, abstractmethod
from typing import Dict, Any, Optional, List
from datetime import datetime
import json


class BaseToolbox(ABC):
    """基础工具箱抽象类"""
    
    def __init__(self, data_access_layer=None):
        """
        初始化工具箱
        
        Args:
            data_access_layer: 数据访问层实例
        """
        self.data_access = data_access_layer
        self.toolbox_name = self.__class__.__name__
    
    @abstractmethod
    def get_toolbox_info(self) -> Dict[str, Any]:
        """
        获取工具箱信息
        
        Returns:
            工具箱信息字典，包含名称、版本、描述等
        """
        pass
    
    @abstractmethod
    def get_available_tools(self) -> List[str]:
        """
        获取可用工具列表
        
        Returns:
            工具ID列表
        """
        pass
    
    def execute_tool(self, tool_id: str, **kwargs) -> Dict[str, Any]:
        """
        执行指定工具
        
        Args:
            tool_id: 工具ID
            **kwargs: 工具参数
            
        Returns:
            执行结果字典
        """
        if tool_id not in self.get_available_tools():
            return {
                "success": False,
                "error": f"工具 {tool_id} 不存在于工具箱 {self.toolbox_name} 中",
                "toolbox": self.toolbox_name,
                "available_tools": self.get_available_tools()
            }
        
        tool_method = getattr(self, tool_id, None)
        if tool_method is None or not callable(tool_method):
            return {
                "success": False,
                "error": f"工具 {tool_id} 未实现或不可调用",
                "toolbox": self.toolbox_name
            }
        
        try:
            result = tool_method(**kwargs)
            result["toolbox"] = self.toolbox_name
            result["tool"] = tool_id
            result["execution_time"] = datetime.now().isoformat()
            return result
        except Exception as e:
            return {
                "success": False,
                "error": f"工具执行异常: {str(e)}",
                "toolbox": self.toolbox_name,
                "tool": tool_id,
                "exception": str(e)
            }
    
    def log_execution(self, tool_id: str, params: Dict[str, Any], result: Dict[str, Any]):
        """
        记录工具执行日志
        
        Args:
            tool_id: 工具ID
            params: 执行参数
            result: 执行结果
        """
        log_entry = {
            "toolbox": self.toolbox_name,
            "tool": tool_id,
            "timestamp": datetime.now().isoformat(),
            "params": params,
            "result": result
        }
        
        print(f"[{self.toolbox_name}] 执行工具: {tool_id}")
        print(f"参数: {json.dumps(params, ensure_ascii=False, indent=2)}")
        print(f"结果: {json.dumps(result, ensure_ascii=False, indent=2)}")
        
        return log_entry
    
    def validate_required_params(self, tool_id: str, params: Dict[str, Any], required_params: List[str]) -> Dict[str, Any]:
        """
        验证必需参数
        
        Args:
            tool_id: 工具ID
            params: 提供的参数
            required_params: 必需参数列表
            
        Returns:
            验证结果字典
        """
        missing_params = [param for param in required_params if param not in params or params[param] is None]
        
        if missing_params:
            return {
                "valid": False,
                "error": f"缺少必需参数: {', '.join(missing_params)}",
                "missing_params": missing_params,
                "tool": tool_id
            }
        
        return {
            "valid": True,
            "tool": tool_id
        }