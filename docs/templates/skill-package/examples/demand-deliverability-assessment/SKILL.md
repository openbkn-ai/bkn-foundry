---
name: demand-deliverability-assessment
description: >
  Use when a user asks whether a product demand (an existing forecast or a new order) can be
  delivered on knowledge network supply_942_verify: locate the forecast, read the inventory
  metric, run the kitting / net-demand function, and, only after explicit user confirmation,
  raise the simulated replenishment action.
metadata:
  bkn_scope: supply_942_verify
  uses:
    - type: metric
      name: 库存可用量
      purpose: 可用库存总口径，作为齐套判断的库存背景
    - type: function
      name: BOM清单
      purpose: 展开产品一级主料，解释缺料从何而来
    - type: function
      name: 要X套净需求与齐套
      purpose: 判断指定套数是否齐套并给出缺料与建议补货量
    - type: function
      name: l1_kitting_check
      purpose: 只看一级主料的快速齐套检查（bkn-osdk 自建函数样例）
    - type: action
      name: 模拟确认补货计划
      purpose: 缺料时生成模拟补货计划回执
      confirm: true
---

# 需求可交付性评估

这是一个**业务流程编排 Skill**。它不自带算法、不写 URL、不带 Token；所有取数与计算都通过当前知识网络已挂载的指标、函数和行动完成。Agent 读完本文后，按「能力依赖」表用平台工具逐步调用即可。

## Skill Card

| 字段 | 值 |
|------|-----|
| `bkn_scope` | `supply_942_verify` |
| `trigger` | 「这单能不能按期交」「预测单 X 现在能交多少」「新增 N 套 Y 产品能否承接」 |
| `优先指标` | **库存可用量** |
| `优先函数` | **要X套净需求与齐套**；解释缺料结构时补 **BOM清单** |
| `可选行动` | **模拟确认补货计划**（须用户确认） |
| `open_parameters` | `product`（产品编码）、`qty`（需求套数）、`substitute_enabled`（替代料策略）、`warehouse_scope?` |

## 能力依赖

Agent 用 `search_capabilities` 按**名称**在本网络内找到函数，用 `get_kn_detail` / `get_object_types` 找到指标与行动类型。名称与执行工厂、知识网络中的登记完全一致，不要改写。

| 名称 | 类型 | 用途 | 调用方式 | 结果怎么读 | 需确认 |
|------|------|------|----------|------------|--------|
| 库存可用量 | 指标 | 可用库存总口径 | `query_metric`，`time.instant=true`；需按物料拆分时加 `analysis_dimensions=["material_code"]` | `datas[0].values[0]` 为合计可用量 | 否 |
| BOM清单 | 函数 | 展开产品一级主料 | `execute_tool`，参数 `product`（必填）、`depth=1` | `l1_lines[]` 为一级主料，`line_count` 为 BOM 行数 | 否 |
| 要X套净需求与齐套 | 函数 | 齐套判断与缺料 | `execute_tool`，参数 `product`、`qty`、`substitute_enabled`（必须明确 true/false） | `kitting_ok` 为齐套结论；`gaps[]` 为缺料，取 `net_requirement` 与 `recommended_replenishment_qty` | 否 |
| l1_kitting_check | 函数 | 一级主料快速齐套检查（不含在途与替代料） | `execute_tool`，参数 `kn_id`、`product`、`qty` | `kitting_ok`、`gap_count`、`gaps[]`（`net_requirement`） | 否 |
| 模拟确认补货计划 | 行动 | 生成模拟补货计划回执 | 先 `get_action_info` 取参数 schema，再 `execute_action`；`dynamic_params.request_json` 为 JSON 字符串，`dynamic_params.simulation=true` | 回执带 `simulation=true`、`business_write=false`，不代表真实业务写入 | **是** |

不在依赖表里的能力不要调用；表里的能力若在本网络找不到，如实告知用户「该能力未挂载」，不要换用其他网络或平台级目录里的同名能力。

## Agent 执行步骤

1. **解析需求**：把用户给的产品解析为唯一编码。若给的是预测单号，用 `query_object_instance` 在「产品需求预测单」上按 `billno` 或 `material_number` 用 `match` 定位（这两个字段是全文索引字段，不要用 `==`），取出 `material_number` 与 `qty`。名称命中多个编码时先澄清。
2. **确认替代料策略**：`substitute_enabled` 未给出时先问用户，不得默认。
3. **读库存背景**：调用指标 **库存可用量**（instant），作为报告的库存基础数字。
4. **齐套判断**：调用函数 **要X套净需求与齐套**，传 `product`、`qty`、`substitute_enabled`。`kitting_ok=true` 直接给「可交付」结论并结束。
5. **解释缺料**：`kitting_ok=false` 时，按 `gaps[]` 的 `net_requirement` 从大到小列出前 5 项缺料；需要说明缺料来自哪一级主料时再调 **BOM清单**（`depth=1`）。
6. **询问是否补货**：向用户复述缺料清单与建议补货量，明确询问是否生成模拟补货计划。**未得到肯定答复不得执行行动。**
7. **执行行动**：用户确认后，先 `get_action_info` 取「模拟确认补货计划」的参数，再 `execute_action`。参数映射：取 `gaps[]` 中 `net_requirement` 最大的一项作为瓶颈料，`request_json` 为
   `{"request_id":"DEMO-DLV-<产品编码>","demo_approved":true,"eligible_on_hand":<该项 available_qty>,"reserved":0,"net_demand":<该项 recommended_replenishment_qty>,"safety_stock":0,"qualified_in_transit":<该项 in_transit_qty>,"moq":1,"pack_size":1}`，`simulation=true`。
8. **报告**：按 `references/report-spec.md` 输出。行动回执必须标注「模拟」，不得表述为已下达采购。

## 完成门槛

1. 产品已解析为唯一编码，`substitute_enabled` 已明确。
2. 指标与函数结果都来自本网络的调用，不在本地重算。
3. 行动只在用户明确确认后执行，且报告中标注为模拟。
4. 任一能力不可用时如实记录 `unavailable`，不得用估算值替代。

## 参考

- `references/report-spec.md`
