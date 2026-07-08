# Exchange邮件恢复工具箱使用指南

## 概述

本工具箱系统提供了完整的Exchange邮件恢复功能，通过统一的工具箱接口调用script/目录下的已开发工具，实现了业务逻辑与数据访问的分离。

## 架构设计

```
toolboxes/
├── __init__.py                    # 工具箱包初始化
├── base_toolbox.py                 # 基础工具箱抽象类
├── recovery_toolbox.py             # 恢复工具箱（统一工具箱）
├── toolbox_manager.py              # 工具箱管理器
└── README.md                      # 本文档

script/
├── list_data_domains.py            # 列出数据域
├── list_exchange_servers.py        # 列出Exchange服务器
├── find_backup_timepoints.py       # 查找备份时间点
├── browse_backup_emails.py         # 浏览备份邮件
├── execute_recovery_task.py       # 执行恢复任务
├── verify_exchange_availability.py # 验证Exchange可用性
└── generate_recovery_report.py     # 生成恢复报告
```

## 核心组件

### 1. 基础工具箱 (BaseToolbox)

**文件**: `base_toolbox.py`

**功能**: 定义所有工具箱的通用接口和功能

**主要方法**:
```python
# 工具箱信息
get_toolbox_info() -> Dict
get_available_tools() -> List[str]

# 工具执行
execute_tool(tool_id: str, **kwargs) -> Dict

# 辅助方法
validate_required_params(tool_id: str, params: Dict, required_params: List) -> Dict
log_execution(tool_id: str, params: Dict, result: Dict) -> Dict
```

### 2. 恢复工具箱 (RecoveryToolbox)

**文件**: `recovery_toolbox.py`

**功能**: 提供Exchange邮件恢复的完整功能，通过调用script/目录下的工具实现

**版本**: 2.0.0

**可用工具**:
```python
[
    "list_data_domains",           # 列出数据域列表
    "list_exchange_servers",       # 列出Exchange服务器列表
    "find_backup_timepoints",      # 查找备份时间点副本
    "browse_backup_emails",        # 浏览备份邮件
    "execute_recovery_task",      # 执行恢复任务
    "verify_exchange_availability", # 验证Exchange可用性
    "generate_recovery_report"     # 生成恢复报告
]
```

**主要方法**:
```python
# 数据域管理
list_data_domains(**kwargs) -> Dict

# Exchange服务器管理
list_exchange_servers(data_domain_id: int, **kwargs) -> Dict

# 备份时间点管理
find_backup_timepoints(server_name: str, server_address: str, **kwargs) -> Dict

# 邮件浏览
browse_backup_emails(backup_timepoint_id: int, 
                   email_subject_filter: Optional[str] = None,
                   email_sender_filter: Optional[str] = None,
                   email_date_filter: Optional[str] = None,
                   **kwargs) -> Dict

# 恢复任务执行
execute_recovery_task(task_id: int, user_id: str, **kwargs) -> Dict

# 可用性验证
verify_exchange_availability(recovery_job_id: int, **kwargs) -> Dict

# 报告生成
generate_recovery_report(recovery_job_id: int, **kwargs) -> Dict
```

### 3. 工具箱管理器 (ToolboxManager)

**文件**: `toolbox_manager.py`

**功能**: 统一管理恢复工具箱，提供统一的调用接口

**主要方法**:
```python
# 工具箱管理
get_toolbox(toolbox_id: str) -> BaseToolbox
list_toolboxes() -> List[Dict]

# 工具执行
execute_tool(toolbox_id: str, tool_id: str, **kwargs) -> Dict

# 工具信息
get_available_tools(toolbox_id: Optional[str] = None) -> Dict[str, List[str]]
get_tool_info(toolbox_id: str, tool_id: str) -> Dict[str, Any]
list_all_tools() -> List[Dict[str, Any]]

# 初始化
initialize_all() -> Dict[str, Any]
```

## 使用示例

### 1. 创建工具箱管理器

```python
from toolboxes import create_toolbox_manager

# 创建工具箱管理器
manager = create_toolbox_manager()

# 初始化所有工具箱
init_result = manager.initialize_all()
print(f"初始化结果: {init_result}")
```

### 2. 列出数据域

```python
# 执行工具
result = manager.execute_tool("recovery_toolbox", "list_data_domains")

if result.get("success"):
    data_domains = result.get("data", {}).get("data_domains", [])
    for domain in data_domains:
        print(f"数据域: {domain['domain_name']} ({domain['domain_ip']})")
else:
    print(f"错误: {result.get('error')}")
```

### 3. 列出Exchange服务器

```python
# 指定数据域ID
data_domain_id = 1

# 执行工具
result = manager.execute_tool("recovery_toolbox", "list_exchange_servers", 
                           data_domain_id=data_domain_id)

if result.get("success"):
    servers = result.get("data", {}).get("exchange_servers", [])
    for server in servers:
        print(f"服务器: {server['name']} ({server['ip']}) - {server['status']}")
else:
    print(f"错误: {result.get('error')}")
```

### 4. 查找备份时间点

```python
# 指定服务器信息
server_name = "ExchangeServer01"
server_address = "192.168.1.100"

# 执行工具
result = manager.execute_tool("recovery_toolbox", "find_backup_timepoints",
                           server_name=server_name,
                           server_address=server_address)

if result.get("success"):
    timepoints = result.get("data", {}).get("backup_timepoints", [])
    for tp in timepoints:
        print(f"时间点: {tp['snapshot_name']} - {tp['snapshot_time']}")
else:
    print(f"错误: {result.get('error')}")
```

### 5. 浏览备份邮件

```python
# 指定备份时间点ID
backup_timepoint_id = 1

# 执行工具（可选过滤条件）
result = manager.execute_tool("recovery_toolbox", "browse_backup_emails",
                           backup_timepoint_id=backup_timepoint_id,
                           email_subject_filter="合同",
                           email_sender_filter="客户")

if result.get("success"):
    emails = result.get("data", {}).get("emails", [])
    for email in emails:
        print(f"邮件: {email['email_subject']} - {email['email_sender']}")
else:
    print(f"错误: {result.get('error')}")
```

### 6. 执行恢复任务

```python
# 指定任务ID和用户ID
task_id = 1
user_id = "admin"

# 执行工具
result = manager.execute_tool("recovery_toolbox", "execute_recovery_task",
                           task_id=task_id,
                           user_id=user_id)

if result.get("success"):
    job = result.get("data", {}).get("recovery_job")
    print(f"恢复作业: {job['job_name']} - {job['recovery_result']}")
else:
    print(f"错误: {result.get('error')}")
```

### 7. 验证Exchange可用性

```python
# 指定恢复作业ID
recovery_job_id = 1

# 执行工具
result = manager.execute_tool("recovery_toolbox", "verify_exchange_availability",
                           recovery_job_id=recovery_job_id)

if result.get("success"):
    verification = result.get("data", {}).get("verification")
    print(f"验证结果: {verification['verification_result']}")
    print(f"验证方法: {verification['verification_method']}")
else:
    print(f"错误: {result.get('error')}")
```

### 8. 生成恢复报告

```python
# 指定恢复作业ID
recovery_job_id = 1

# 执行工具
result = manager.execute_tool("recovery_toolbox", "generate_recovery_report",
                           recovery_job_id=recovery_job_id)

if result.get("success"):
    report = result.get("data", {}).get("report")
    print(f"报告ID: {report['report_id']}")
    print(f"恢复状态: {report['recovery_status']}")
    print(f"数据统计: {report['data_statistics']}")
else:
    print(f"错误: {result.get('error')}")
```

## 架构优势

### 1. 统一的工具箱接口
- 所有action使用同一个工具箱：`recovery_toolbox`
- 简化了配置和管理
- 降低了学习成本

### 2. 使用已开发的工具
- 直接调用script/目录下的Python脚本
- 避免重复开发
- 保持工具的一致性

### 3. 清晰的职责分离
- 工具箱：负责工具管理和调用
- 脚本：负责具体业务逻辑
- 数据：通过CSV文件存储

### 4. 易于扩展
- 新增工具只需在script/目录添加脚本
- 在recovery_toolbox中添加对应方法
- 无需修改其他代码

### 5. 完整的错误处理
- 脚本执行异常捕获
- 统一的错误返回格式
- 便于调试和维护

## Action配置

所有action_types/中的行动类型现在都使用`recovery_toolbox`：

| Action | 工具ID | 描述 |
|--------|--------|------|
| list_data_domains | list_data_domains | 列出数据域列表 |
| list_exchange_servers | list_exchange_servers | 列出Exchange服务器列表 |
| find_backup_timepoints | find_backup_timepoints | 查找备份时间点副本 |
| browse_backup_emails | browse_backup_emails | 浏览备份邮件 |
| execute_recovery_task | execute_recovery_task | 执行恢复任务 |
| verify_exchange_availability | verify_exchange_availability | 验证Exchange可用性 |
| generate_recovery_report | generate_recovery_report | 生成恢复报告 |

## 测试

运行工具箱管理器测试：

```bash
cd toolboxes
python toolbox_manager.py
```

测试输出包括：
1. 初始化所有工具箱
2. 列出所有工具箱
3. 列出所有工具
4. 测试工具调用

## 总结

通过使用统一的`recovery_toolbox`工具箱，我们实现了：

- ✅ 统一的工具调用接口
- ✅ 使用script/下已开发的工具
- ✅ 清晰的架构设计
- ✅ 完整的错误处理
- ✅ 易于测试和维护
- ✅ 支持未来扩展

这种设计完全替代了之前的多工具箱架构，提供了更简洁、更高效的Exchange邮件恢复解决方案。
