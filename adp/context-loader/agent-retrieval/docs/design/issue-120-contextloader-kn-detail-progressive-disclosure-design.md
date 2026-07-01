<!--
Copyright openbkn.ai
Copyright The kweaver.ai Authors.

Licensed under the Apache License, Version 2.0.
See the LICENSE file in the project root for details.
-->

# Design: get_kn_detail 渐进式披露（progressive disclosure）

- Epic: [#120](https://github.com/openbkn-ai/bkn-foundry/issues/120)
- 本仓落地: [#125](https://github.com/openbkn-ai/bkn-foundry/issues/125)（agent-retrieval / context-loader）
- 跨面子任务: 后端 bkn-backend [#122]、SDK/openbkn-cli [#123]、Studio [#124]
- 分支 / PR: `feat/120-kn-detail-progressive-disclosure` · [#127](https://github.com/openbkn-ai/bkn-foundry/pull/127)

## 1. 背景与问题

`get_kn_detail` 一次性返回知识网络的全量 schema：概念组、对象类型（含 `data_source`、`data_properties`、`logic_properties`）、关系类型（含 `mapping_rules`）、行动类型。对 agent（LLM）消费者不友好：

- **单次响应过大**，直接吃掉大量 context。实测 worldcup KN（27 对象类 / 37 关系类）≈ **142,926 字节**。
- **多数场景只需先看结构**（有哪些对象/关系、主键、端点），再对少数目标对象下钻取字段映射；一把梭全量是浪费。
- 导出响应里 `concept_groups` **重复嵌套**了顶层已有的完整对象/关系/行动实例（现有 `concept_retrieval` 只消费顶层数组，嵌套副本无人用）。

## 2. 目标 / 非目标

**目标**
- `get_kn_detail` 默认只返「骨架 + 属性名」，按需下钻取完整定义。
- 契约在所有对外表面一致（agent-retrieval / 后端 / SDK / Studio）。
- agent-retrieval 侧零上游依赖变更即可落地（不改 bkn-backend / vega）。

**非目标（本仓本期）**
- 不在 bkn-backend 源头做 summary / ids 下钻（那是 [#122]，能真正省后端负载，本期先在 adp 层裁剪兜住）。
- 不改动 `search_schema` 等其它工具的既有行为。

## 3. 统一契约

### 3.1 `get_kn_detail` 加 `detail_level`

| 值 | 行为 |
|---|---|
| `summary`（默认） | 对象/关系/行动**骨架** + 每个属性仅 `name / display_name / type / comment`；**砍** `data_property.mapped_field`、`data_property.condition_operations`、`logic_property.data_source`、`logic_property.parameters`、`relation.mapping_rules`、`relation.source/target_object_type`；`concept_groups` 只留 `id / name / object_type_ids`（去重嵌套）。 |
| `full` | 全量返回；**仍去重** `concept_groups` 嵌套。 |

保留的导航锚点：对象 `primary_keys` / `data_source`，关系 `source_object_type_id` / `target_object_type_id`。

### 3.2 下钻工具

- `get_object_types(kn_id, ids[])` → 指定对象类的**完整**定义（含 `mapped_field` / `condition_operations` / 逻辑属性细节）。
- `get_relation_types(kn_id, ids[])` → 指定关系类的完整定义（含 `mapping_rules`、source/target 对象名）。
- `ids` 支持多个、一次批量取回；接受 id 或 name；未匹配的 id 在 `missing` 字段回传。

### 3.3 渐进式闭环（agent 视角）

```
get_kn_detail(kn_id)                       → 骨架 + 属性名（轻）
  ↓ 挑中相关对象/关系 id
get_object_types(kn_id, [ot_a, ot_b])      → 那几个对象的字段映射
get_relation_types(kn_id, [rt_x])          → 那几个关系的 mapping_rules
```

## 4. 实现（agent-retrieval）

裁剪 / 过滤逻辑挂在 `interfaces.KnowledgeNetworkDetail`，MCP 与 REST 共用一条路径：

- `Slim(level string)` —— 永远去重 `concept_groups` 嵌套；`level != full` 时砍每属性重货。
- `FilterObjectTypes(ids) (matched, missing)` / `FilterRelationTypes(ids)` —— 按 id/name 过滤、保序、去重、回报 missing。
- `mapped_field` / `condition_operations` / `mapping_rules` 补 `omitempty`，nil 才真正从 JSON 删键。

**三处对齐**（每个工具都过）：

| 面 | 位置 |
|---|---|
| MCP | `driveradapters/mcp/app.go`（toolKey + AddTool）、`tools.go`（handler）、`schemas/*.json`、`schemas/tools_meta.json`、`serverInstructions` |
| REST | `driveradapters/knquerytools/index.go`（handler + 接口）、`rest_private_handler.go` + `rest_public_handler.go`（路由） |
| Toolbox | `bootstrap/tool_dependencies/context_loader_toolset.adp`（`version == source_id`） |

REST 路由（private + public 各注册）：
```
POST /api/agent-retrieval/in/v1/kn/get_kn_detail        # + detail_level
POST /api/agent-retrieval/in/v1/kn/get_object_types
POST /api/agent-retrieval/in/v1/kn/get_relation_types
```

工具描述与 MCP `serverInstructions` 明确写出 summary→drill 流程，引导 agent 先拿骨架再按需展开。

## 5. 验收与测试

**单元测试**（`interfaces` 包）：`Slim`（summary/full/nil-safe）、`FilterObjectTypes` / `FilterRelationTypes`（by-id / by-name / 去重 / missing）。全模块 `go test ./...` 绿（20 包，含 `knsearch`、`tests/http`，验证 `omitempty` 未波及 `search_schema`）。

**VM 实测**（worldcup KN，headers `x-account-id`/`x-account-type`）：

| 场景 | 字节 | 对比旧默认 |
|---|---|---|
| 旧默认（full） | 142,926 | — |
| 新默认 summary | 106,649 | ↓25% |
| 新 full | 132,006 | ↓8%（去重） |
| get_object_types × 3 | 10,301 | 按需 |
| get_relation_types × 2 | 1,842 | 按需 |

结构 / 字段保留 / missing 全部验证通过。

## 6. 兼容性

- 新增 `detail_level` 缺省即 summary —— **默认行为变化**（不再一次吐全量）；显式传 `full` 保留旧行为。
- `omitempty` 只影响 wire JSON；内部消费者读 Go struct，不受影响。
- 下钻工具为纯增量；不动既有工具。

## 7. 后续规划

- [ ] PR #127 合并 → 部署 118（补丁通常两台都上）。
- [ ] **后端 bkn-backend [#122]**：导出/详情端点原生支持 `detail_level=summary` + `ids` 下钻，真正省后端负载与拉取时延；落地后 agent-retrieval 可去掉「fetch-full-then-filter」改为透传。
- [ ] **SDK/openbkn-cli [#123]**（独立仓）：暴露 `detail_level` + `get_object_types` / `get_relation_types`。
- [ ] **Studio [#124]**（独立仓）：schema 视图默认 summary，点开懒加载对象/关系详情。
- [ ] 调优待定：summary 是否再砍 `display_name`（留纯 name）；worldcup 为 CSV KN 重货少，metric/logic 重的 KN summary 省更多。
- [ ] TOON 传输层（MCP）单独验证 —— HTTP `/in` 恒 JSON；summary 让 `data_properties` 齐整后 TOON 表格化可再省一档。

## 8. 备注

- HTTP `/in` 端点需 `x-account-id` / `x-account-type` 头做授权（授权押下游 bkn-backend）；缺则 `Public.Forbidden`。
- `response_format`（json/toon）仅在 MCP 传输层生效，HTTP `/in` 恒返 JSON。
