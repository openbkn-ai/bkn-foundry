# HD 供应链业务知识网络 - Agent 使用指南

> **网络ID**: supplychain  
> **版本**: 1.0.0  
> **标签**: 供应链, 采购, 库存, MRP

## 网络概览

本知识网络描述产品供应分析完整路径：需求预测→产品→BOM→库存→生产计划/采购申请→采购订单→供应商。

### 业务逻辑

1. 基于需求预测，结合 BOM 结构展开物料需求
2. 核查成品及物料库存，进行 MRP 运算生成建议订单
3. 根据物料属性（自制/外购），生成生产计划或采购申请
4. 采购申请转化为采购订单并由供应商执行，生产计划指导工厂生产
5. 最终输出缺口数量及补货建议，支撑供应链决策

### 核心对象

| 对象类别 | 对象 | 文件路径 |
|----------|------|----------|
| 需求侧 | 需求预测 | `object_types/forecast.bkn` |
| 需求侧 | 产品需求计划 | `object_types/pp.bkn` |
| 产品侧 | 产品 | `object_types/product.bkn` |
| 产品侧 | 产品BOM | `object_types/bom.bkn` |
| 物料侧 | 物料 | `object_types/material.bkn` |
| 物料侧 | 库存 | `object_types/inventory.bkn` |
| 物料侧 | 物料需求计划 | `object_types/mrp.bkn` |
| 计划侧 | 工厂生产计划 | `object_types/mps.bkn` |
| 采购侧 | 采购申请单 | `object_types/pr.bkn` |
| 采购侧 | 采购订单 | `object_types/po.bkn` |
| 采购侧 | 供应商 | `object_types/supplier.bkn` |
| 销售侧 | 销售订单 | `object_types/salesorder.bkn` |

### 核心关系

| 关系 | 文件路径 | 说明 |
|------|----------|------|
| product2bom | `relation_types/product2bom.bkn` | 产品→BOM |
| po2supplier | `relation_types/po2supplier.bkn` | 采购订单→供应商 |
| pr2po | `relation_types/pr2po.bkn` | 采购申请→采购订单 |
| material2inventory | `relation_types/material2inventory.bkn` | 物料→库存 |
| bom2material | `relation_types/bom2material.bkn` | BOM→物料 |
| product2inventory | `relation_types/product2inventory.bkn` | 产品→库存 |
| demand2pp | `relation_types/demand2pp.bkn` | 需求→产品需求计划 |
| mrp2material | `relation_types/mrp2material.bkn` | MRP→物料 |
| material2pr | `relation_types/material2pr.bkn` | 物料→采购申请 |
| po2material | `relation_types/po2material.bkn` | 采购订单→物料 |
| pp2mrp | `relation_types/pp2mrp.bkn` | 产品需求计划→MRP |
| pp2product | `relation_types/pp2product.bkn` | 产品需求计划→产品 |
| salesorder2product | `relation_types/salesorder2product.bkn` | 销售订单→产品 |
| pp2mps | `relation_types/pp2mps.bkn` | 产品需求计划→生产计划 |

## 拓扑结构

```
需求预测 → 产品 → BOM → 物料 → 库存 → MRP运算
                                      ↓
                          ┌──────────┴──────────┐
                          ↓                     ↓
                    工厂生产计划            采购申请单
                          ↓                     ↓
                    生产执行               采购订单
                                                ↓
                                           供应商履约
```

## 使用建议

### 查询场景

1. **获取所有对象定义**
   - 读取 `object_types/*.bkn` 文件
   - 包含 12 个供应链相关对象

2. **查找关系定义**
   - 读取 `relation_types/*.bkn` 文件
   - 包含 14 个业务关系

### 分析场景

1. **供应缺口分析**
   - 结合需求预测、库存、在途订单
   - 计算净需求并生成补货建议

2. **供应商绩效评估**
   - 分析采购订单履约情况
   - 评估交付及时率和质量

## 索引表

### 按类型索引

- **对象定义**: `object_types/*.bkn`
- **关系定义**: `relation_types/*.bkn`

### 按业务流程索引

- **需求管理**: forecast, pp
- **产品管理**: product, bom
- **物料管理**: material, inventory, mrp
- **生产管理**: mps
- **采购管理**: pr, po, supplier
- **销售管理**: salesorder

## 注意事项

1. 本示例采用单文件单定义的组织方式
2. 每个对象和关系分别存储在独立文件中
3. 适合复杂业务领域的知识建模
4. 所有对象和关系遵循供应链标准业务流程
