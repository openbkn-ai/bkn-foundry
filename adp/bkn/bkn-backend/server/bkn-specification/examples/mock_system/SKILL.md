# 员工入职系统-resource版本 - Agent 使用指南

> **网络ID**: yzm_mock_system2  
> **版本**:   

## 网络概览

### 核心对象

| 对象 | 文件路径 | 说明 |
|------|----------|------|
| 部门表 | `object_types/department.bkn` |  |
| 部门表2 | `object_types/department2.bkn` |  |
| 员工表 | `object_types/employee.bkn` | d7blk86fmoj81ili0q3g |
| 员工表2 | `object_types/employee2.bkn` | d7blk86fmoj81ili0q3g |
| 技能类 | `object_types/skill_object_type.bkn` |  |
| 技能类1 | `object_types/skill_object_type1.bkn` |  |

### 核心关系

| 关系 | 文件路径 | 说明 |
|------|----------|------|
| resource关联 | `relation_types/d7bmni0lr8vft7go3smg.bkn` |  |
| 员工-skill | `relation_types/emp_skill.bkn` |  |

### 可用行动

| 行动 | 文件路径 | 说明 |
|------|----------|------|
| a | `action_types/d7c6r1orj0k4ejrg82kg.bkn` |  |
| cc | `action_types/d7c9288rj0k4ejrg82lg.bkn` |  |

## 目录结构

```
.
├── network.bkn
├── SKILL.md
├── CHECKSUM
├── object_types/
├── relation_types/
└── action_types/
```

## 使用建议

### 查询场景

1. **获取所有对象定义**
   - 查看 `object_types/` 目录下的文件

2. **查找关系定义**
   - 查看 `relation_types/` 目录下的文件

### 运维场景

1. **执行运维操作**
   - 查看 `action_types/` 目录下的行动定义
   - 了解触发条件和参数绑定

## 索引表

### 按类型索引

- **对象定义**: `object_types/`
- **关系定义**: `relation_types/`
- **行动定义**: `action_types/`

## 注意事项

1. 本网络由 BKN SDK 自动生成 SKILL.md
2. 所有定义遵循 BKN 规范
3. 使用 CHECKSUM 文件验证网络完整性
