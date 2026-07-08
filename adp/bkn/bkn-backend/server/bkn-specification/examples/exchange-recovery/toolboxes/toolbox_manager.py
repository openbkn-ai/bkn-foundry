#!/usr/bin/env python3
"""
工具箱管理器
统一管理恢复工具箱，提供统一的调用接口
"""

from typing import Dict, Any, List, Optional
from pathlib import Path

try:
    from .base_toolbox import BaseToolbox
    from .recovery_toolbox import RecoveryToolbox
except ImportError:
    from base_toolbox import BaseToolbox
    from recovery_toolbox import RecoveryToolbox


class ToolboxManager:
    """工具箱管理器"""
    
    def __init__(self, data_dir: str = None):
        """
        初始化工具箱管理器
        
        Args:
            data_dir: 数据目录路径，默认为skill目录下的data文件夹
        """
        if data_dir is None:
            data_dir = Path(__file__).parent.parent / 'data'
        
        self.data_dir = str(data_dir)
        
        self.toolboxes = {
            "recovery_toolbox": RecoveryToolbox()
        }
    
    def get_toolbox(self, toolbox_id: str) -> Optional[BaseToolbox]:
        """
        获取指定的工具箱
        
        Args:
            toolbox_id: 工具箱ID
            
        Returns:
            工具箱实例或None
        """
        return self.toolboxes.get(toolbox_id)
    
    def list_toolboxes(self) -> List[Dict[str, Any]]:
        """
        列出所有工具箱
        
        Returns:
            工具箱信息列表
        """
        result = []
        for toolbox_id, toolbox in self.toolboxes.items():
            info = toolbox.get_toolbox_info()
            result.append(info)
        return result
    
    def execute_tool(self, toolbox_id: str, tool_id: str, **kwargs) -> Dict[str, Any]:
        """
        执行指定工具箱中的工具
        
        Args:
            toolbox_id: 工具箱ID
            tool_id: 工具ID
            **kwargs: 工具参数
            
        Returns:
            执行结果字典
        """
        toolbox = self.get_toolbox(toolbox_id)
        
        if toolbox is None:
            return {
                "success": False,
                "error": f"工具箱 {toolbox_id} 不存在",
                "available_toolboxes": list(self.toolboxes.keys())
            }
        
        return toolbox.execute_tool(tool_id, **kwargs)
    
    def get_available_tools(self, toolbox_id: Optional[str] = None) -> Dict[str, List[str]]:
        """
        获取可用工具列表
        
        Args:
            toolbox_id: 工具箱ID（可选），如果为None则返回所有工具箱的工具
            
        Returns:
            工具ID字典，key为工具箱ID，value为工具列表
        """
        if toolbox_id is not None:
            toolbox = self.get_toolbox(toolbox_id)
            if toolbox is not None:
                return {toolbox_id: toolbox.get_available_tools()}
            else:
                return {}
        
        result = {}
        for tb_id, toolbox in self.toolboxes.items():
            result[tb_id] = toolbox.get_available_tools()
        return result
    
    def get_tool_info(self, toolbox_id: str, tool_id: str) -> Dict[str, Any]:
        """
        获取工具信息
        
        Args:
            toolbox_id: 工具箱ID
            tool_id: 工具ID
            
        Returns:
            工具信息字典
        """
        toolbox = self.get_toolbox(toolbox_id)
        
        if toolbox is None:
            return {
                "exists": False,
                "error": f"工具箱 {toolbox_id} 不存在"
            }
        
        available_tools = toolbox.get_available_tools()
        
        return {
            "exists": True,
            "toolbox_id": toolbox_id,
            "toolbox_name": toolbox.toolbox_name,
            "tool_id": tool_id,
            "available": tool_id in available_tools,
            "toolbox_version": toolbox.version
        }
    
    def list_all_tools(self) -> List[Dict[str, Any]]:
        """
        列出所有工具箱中的所有工具
        
        Returns:
            所有工具的信息列表
        """
        result = []
        for toolbox_id, toolbox in self.toolboxes.items():
            available_tools = toolbox.get_available_tools()
            for tool_id in available_tools:
                result.append({
                    "toolbox_id": toolbox_id,
                    "toolbox_name": toolbox.toolbox_name,
                    "tool_id": tool_id,
                    "toolbox_version": toolbox.version
                })
        return result
    
    def initialize_all(self) -> Dict[str, Any]:
        """
        初始化所有工具箱
        
        Returns:
            初始化结果
        """
        results = {}
        for toolbox_id, toolbox in self.toolboxes.items():
            try:
                info = toolbox.get_toolbox_info()
                results[toolbox_id] = {
                    "status": "initialized",
                    "info": info
                }
            except Exception as e:
                results[toolbox_id] = {
                    "status": "failed",
                    "error": str(e)
                }
        
        return {
            "success": all(r["status"] == "initialized" for r in results.values()),
            "toolbox_results": results,
            "total_toolboxes": len(self.toolboxes),
            "initialized_count": sum(1 for r in results.values() if r["status"] == "initialized")
        }


def create_toolbox_manager(data_dir: str = None) -> ToolboxManager:
    """
    创建工具箱管理器实例
    
    Args:
        data_dir: 数据目录路径
        
    Returns:
        工具箱管理器实例
    """
    return ToolboxManager(data_dir)


if __name__ == '__main__':
    import json
    import sys
    from pathlib import Path
    
    sys.path.insert(0, str(Path(__file__).parent))
    
    from base_toolbox import BaseToolbox
    from recovery_toolbox import RecoveryToolbox
    
    print("工具箱管理器测试")
    print("=" * 50)
    
    manager = ToolboxManager()
    
    print("\n1. 初始化所有工具箱")
    init_result = manager.initialize_all()
    print(json.dumps(init_result, ensure_ascii=False, indent=2))
    
    print("\n2. 列出所有工具箱")
    toolboxes = manager.list_toolboxes()
    print(json.dumps(toolboxes, ensure_ascii=False, indent=2))
    
    print("\n3. 列出所有工具")
    all_tools = manager.list_all_tools()
    print(json.dumps(all_tools, ensure_ascii=False, indent=2))
    
    print("\n4. 测试工具调用")
    test_result = manager.execute_tool("recovery_toolbox", "list_data_domains")
    print(json.dumps(test_result, ensure_ascii=False, indent=2))
