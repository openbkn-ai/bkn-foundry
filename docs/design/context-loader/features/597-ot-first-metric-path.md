# OT-first 指标路径：对象类看得见指标，选定后按口径取数

- Issue：[#597](https://github.com/openbkn-ai/bkn-foundry/issues/597)
- 分支：`feature/597-ot-first-metric-path`
- 模块：context-loader（agent-retrieval）、bkn-backend
- 状态：implementing

## 1. 背景与问题

第三方 Agent 取指标的目标路径是 **OT-first**：

```text
召回对象类 → 在该对象类下判断用哪个指标 → 按 MetricDefinition 口径计算
```

供应链 POC（`supplychain_hd0202`，8 个 atomic 指标）实测下来，第 1 步基本可用，
第 2、3 步断链：

| 步骤 | 现状 | 后果 |
|---|---|---|
| 2. 选指标 | 对象类只能通过**已绑定的逻辑属性**间接暴露指标；未绑逻辑属性的 scoped 指标在 `get_object_types` / `get_kn_detail` 里完全不可见 | Agent 看不到可用指标 |
| 3. 计算 | 实例级 + 已绑逻辑属性有 `get_logic_properties_values`；类级 / 未绑的只有 ontology-query 的 `POST .../metrics/{id}/data`，MCP 无对应工具 | Agent 退化成 `run_sql` 自己重写口径 |

「用 SQL 重写已建模指标」是这条链路真正的代价：口径写在 MetricDefinition 里，
SQL 重写等于把治理过的口径复制一份到 Agent 的临时推理里。

## 2. 方案

### 2.1 第 2 步：对象类带出 `related_metrics`

`get_object_types` 在返回对象类完整定义时，附带 `related_metrics[]` ——
`scope_type=object_type` 且 `scope_ref=<对象类 ID>` 的全部指标，**含未绑逻辑属性的**。

数据来源选择：**bkn-backend 指标注册表**（`GET /in/v1/knowledge-networks/{kn}/metrics`，
DB 直查、带 KN `view_detail` 鉴权），不是 `SearchMetricTypes`。后者是概念索引上的语义召回，
既是排序结果、又只有索引那么新（见 #535）；而「这个对象类有哪些指标」必须全量且与库一致。

批量：bkn-backend 的 `scope_ref` 扩成支持逗号多值（`sq.Eq` 遇切片自然落成 `IN`），
于是一次 `get_object_types(ids=[a,b,c])` 只多打一次后端，不按对象类扇出。

降级：取指标失败只记 warn 并返回没有 `related_metrics` 的对象类。对象类定义本身已经拿到了，
让整个 `get_object_types` 失败会连 schema 都读不到。

`get_kn_detail` 只挂 `related_metric_count`（计数，不含明细），维持渐进式下钻的体积约束：
`>0` 才值得为取指标下钻。

### 2.2 第 3 步：新增 `query_metric`

MCP 工具 + REST 端点 `POST /kn/query_metric`，转发 ontology-query 的
`POST /in/v1/knowledge-networks/{kn_id}/metrics/{metric_id}/data`。

- 请求体与 ontology-query 的 `MetricQueryRequestBody` 同构（`time` / `condition` /
  `analysis_dimensions` / `order_by` / `having` / `limit`），`fill_null` 走 URL query。
- 响应只留结果序列（`datas` + `overall_ms`），**不回带 `model`（指标定义）**：
  那份定义调用方刚从 `related_metrics` 拿过，每次取数再抄一遍是这套工具面一直在削的体积。
- 走内部路由 `/in`，账户随 header 透传、授权由下游判定，与 `get_logic_properties_values`
  同一姿势——因此 issue 里担心的「AppKey/OAuth 打不通 metric data 会 401」不成立。
- 时间窗就地校验返回 400，规则**逐条对齐 ontology-query 的 `validateMetricQueryRequest`**：
  省略 `instant` 等同于序列查询（必须给 `step`）、`step` 限日历粒度且大小写不敏感、
  `start`/`end` 必须成对、`start <= end`、`fill_null` 仅对带 `start`/`end` 的序列查询有效。
  本地比下游严会把合法调用误拒，比下游松只是把同一个错误延后——所以是复刻，不是另立一套。
- `kn_id` / `metric_id` 进 URL 前 `url.PathEscape`，`fill_null` 走 `url.Values`：`metric_id`
  是 Agent 自由填写的字符串，不转义的话一个带 `?` 的值就能给下游塞进 `branch` 等查询参数。
- 指标列表**分页取全**（单页 1000，硬上限 10000 只是失控保护）：截断后的答案与「这个对象类
  没有指标」在 Agent 眼里完全一样，而那正是把它推回 `run_sql` 的状态；真触到上限会打 warn。

### 2.3 口径写进工具描述

三分流写进 MCP `serverInstructions`（中英）与 `tools_meta`：

- 实例级 + 已绑逻辑属性 → `get_logic_properties_values`
- 类级 / 未绑逻辑属性 → `query_metric`
- 压根没建模成指标 → 才用 `run_sql`

`search_schema` 的描述同时降级 `metric_types` 为辅助线索——指标的权威清单是
`get_object_types` 的 `related_metrics`。

## 3. 改动清单

**bkn-backend**

- `driveradapters/metric_handler.go`：`scope_ref` 支持逗号多值（`splitScopeRefs`）
- `interfaces/metric.go`：`MetricsListQueryParams.ScopeRefs []string`（保留单值 `ScopeRef`）
- `drivenadapters/metric/metric_access.go`：多值走 `IN`，单值仍等值

**context-loader（agent-retrieval）**

- `interfaces/driven_bkn_backend.go`：`RelatedMetric`；`ObjectType.RelatedMetrics` /
  `RelatedMetricCount`；`BknBackendAccess.ListMetricsByObjectTypes`
- `drivenadapters/bkn_backend.go`：`ListMetricsByObjectTypes`（含「后端忽略过滤时本地再筛一遍」）
- `interfaces/kn_metric_query.go`、`interfaces/driven_ontology_query.go`、
  `drivenadapters/ontology_query.go`：`query_metric` 的请求 / 响应与下游调用
- `logics/knmetrics/`：挂指标、挂计数、指标取数与参数校验（MCP 与 REST 共用）
- `driveradapters/mcp/`：`query_metric` 工具（`app.go` / `tools.go` /
  `schemas/query_metric.json` / `tools_meta.json` / `locales/en-US/*`），
  `get_object_types`、`get_kn_detail` 接线，社区工具面基线 +1
- `driveradapters/knquerytools/index.go` + `rest_public_handler.go` /
  `rest_private_handler.go`：REST 侧同源接线与路由
- `bootstrap/tool_dependencies/context_loader_toolset.adp`：新增 `query_metric` 工具条目
  （`metadata.version == source_id`）

**文档**

- `docs/api/context-loader/logic-property.yaml`：新增 `/kn/query_metric`
- `docs/api/context-loader/kn-explore.yaml`：`related_metrics` / `related_metric_count` / `RelatedMetric`
- `docs/api/context-loader/README.md`：文件索引、指标链路、巡检覆盖
- `docs/api/bkn/bkn-metrics.yaml`：`scope_ref` 多值语义

## 4. 兼容性

全部是**新增字段与新增端点**，没有改动既有响应字段的含义：

- `related_metrics` / `related_metric_count` 都是 `omitempty`，老调用方不受影响
- `scope_ref` 单值行为不变，多值是新增能力
- 新 MCP 工具属 core（社区档位可见），不进企业门控

老版本 bkn-backend 配新版 context-loader：多值 `scope_ref` 会被老后端当作单个字符串匹配不到，
返回空列表——表现为「没有 related_metrics」，不报错、不串对象类（适配器还会本地再筛一遍）。

## 5. 验收

- 单测：适配器（查询参数、scope 复筛、错误透传）、服务（分组、降级、时间窗校验、透传）、
  MCP 工具（`related_metrics` 出现在结果里、`query_metric` happy path 与缺参）、
  bkn-backend（`IN` 过滤、`splitScopeRefs`）、社区工具面基线
- 实机（测试服 14.103.77.23，KN `ecommerce_ops_bkn_public`，14 个 atomic 指标）：
  - `order` 的 5 个指标全部可见（此前只有绑了 `lp_gmv_metric` 的 GMV 一个）；
    `shipment` 无任何逻辑属性，其 2 个指标此前完全不可见，现可见
  - `get_kn_detail` 的 20 个对象类计数与指标注册表逐一吻合
  - `query_metric` 取 `m_shipment_cnt` = 12190；`m_gmv` 带 `condition` 排除
    `cancelled` 得 436,367,809.20，与 `run_sql` 分组对账（469,497,191.25 −
    33,129,382.05）完全一致

## 6. 不在范围内

- Studio Context Loader 调试台的 `query_metric` op 与示例（转交前端同事，见 Issue 评论）
- 指标写操作、uniquery 路径变更（#151）
- ConceptSyncer / KN detail 质量（#535）
