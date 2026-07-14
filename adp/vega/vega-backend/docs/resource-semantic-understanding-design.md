# Resource 语义理解设计

> 状态：终稿
> 范围：vega-backend resource 元数据增强
> 关联：bkn-agent Epic #202

## 1. 目标

Vega resource 元数据分为两类：

- 原始元数据：由 catalog discover 从源端扫描得到，表示源端事实。
- 语义元数据：由人工或 agent 基于原始元数据生成，表示面向业务使用者的解释和展示内容。

本设计引入 bkn-agent 对 resource 和 catalog 执行一次性语义理解任务，生成 resource 级、字段级和 catalog 级语义结果。Vega 负责保存当前已应用的展示结果，并用独立表管理 agent 任务、原始输出、置信度、warnings 和应用状态。

语义理解能力分两阶段：

1. Resource 级语义增强：理解单个 resource 的表名、表描述、字段展示名和字段业务描述，并将已应用结果写回 `Resource` 与 `schema_definition` 展示字段。
2. Catalog 级业务表发现：基于当前 catalog 下全部 resource 的原始元数据和 resource 级语义结果，识别面向业务使用的逻辑视图。逻辑视图可以来自单表拆分，也可以来自多表合并。

## 2. 字段边界

### 2.1 Resource 主体字段

`Resource` 只保存源端事实和当前已应用的展示结果。

| 字段 | 来源 | 说明 |
| --- | --- | --- |
| `ID` | create/discover | 平台 resource 主标识，API 定位、权限引用和构建任务使用 |
| `Name` | create/discover | 平台 resource 名称字段，用于列表展示、名称筛选/排序和同 catalog 下按名查找，不由 agent 修改 |
| `SourceIdentifier` | discover | 源端表名、路径或对象标识，不由 agent 修改 |
| `Database` | discover | 源端 database，不由 agent 修改 |
| `SourceDescription` | discover | 源端表注释，不由 agent 修改 |
| `DisplayName` | 人工/agent | 当前生效的业务展示名 |
| `Description` | 人工/agent | 当前生效的业务描述 |

新增字段：

```go
type Resource struct {
    SourceDescription string `json:"source_description,omitempty"`
    DisplayName       string `json:"display_name,omitempty"`
}
```

### 2.2 Schema 字段

`schema_definition` 只保存字段事实和当前已应用的字段展示结果。

| 字段 | 来源 | 说明 |
| --- | --- | --- |
| `Name` | discover/create | 平台字段稳定名称，用于数据查询、过滤、排序、逻辑视图和构建配置，不由 agent 修改 |
| `Type` | discover/规范化 | Vega 内部字段类型，不由 agent 修改 |
| `OriginalName` | discover | 源端字段名，不由 agent 修改 |
| `OriginalType` | discover | 源端字段类型，不由 agent 修改 |
| `OriginalDescription` | discover | 源端字段注释，不由 agent 修改 |
| `DisplayName` | 人工/agent | 当前生效的字段展示名 |
| `Description` | 人工/agent | 当前生效的字段业务描述 |

`Property` 不新增 `SemanticType`、`SemanticConfidence` 等语义分析字段。字段语义类型、细粒度置信分、warnings 和 agent 原始输出只保存在 `t_semantic_understanding_profile`。

## 3. 语义结果表

新增 `t_semantic_understanding_profile` 管理 agent 任务和语义产物。

```text
t_semantic_understanding_profile
- f_id
- f_scope
- f_catalog_id
- f_resource_id
- f_task_id
- f_agent_id
- f_input
- f_input_hash
- f_status
- f_apply_mode
- f_result_json
- f_confidence_threshold
- f_confidence
- f_confidence_detail_json
- f_catalog_apply_detail_json
- f_applied
- f_applied_time
- f_failure_detail
- f_create_time
- f_update_time
```

字段语义：

| 字段 | 说明 |
| --- | --- |
| `f_scope` | `resource` / `catalog` |
| `f_catalog_id` | 关联 catalog；resource 级任务取 resource 所属 catalog，catalog 级任务取目标 catalog |
| `f_resource_id` | resource 级任务关联 resource；catalog 级任务为空 |
| `f_task_id` | bkn-agent 任务 ID |
| `f_agent_id` | 执行语义理解的 agent ID |
| `f_input` | 发送给 bkn-agent 的完整结构化输入，用于审计和重放 |
| `f_input_hash` | 基于 agent 输入生成，用于判断任务结果是否仍匹配当前 resource 或 catalog 快照 |
| `f_status` | `pending` / `running` / `succeeded` / `failed` |
| `f_apply_mode` | `dry_run` / `fill_empty` / `force` |
| `f_result_json` | agent 原始结构化输出，包含 resource 级、字段级或 catalog 级语义结果以及 warnings |
| `f_confidence_threshold` | 本次任务要求的最低置信分，低于阈值的结果不 apply |
| `f_confidence` | 任务级语义置信度 |
| `f_confidence_detail_json` | 细粒度置信分详情，包含字段、逻辑视图和 stale 建议等对象级置信分 |
| `f_catalog_apply_detail_json` | catalog 级应用明细，记录逻辑视图创建、更新、标记 stale 等实际变更对象；resource 级任务为空 |
| `f_applied` | agent 结果是否已应用；resource 级表示展示字段已投影，catalog 级表示逻辑视图变更已执行 |
| `f_applied_time` | 应用时间 |
| `f_failure_detail` | 失败详情 |

`t_semantic_understanding_profile` 是语义任务与历史产物的管理表；`Resource` 和 `schema_definition` 只保存当前已应用的展示结果。

Catalog 级 `f_catalog_apply_detail_json` 示例：

```json
{
  "created_resource_ids": ["view-3"],
  "updated_resource_ids": ["view-1"],
  "staled_resource_ids": ["view-2"]
}
```

## 4. Agent 接入

Resource 语义理解使用 bkn-agent 一次性任务模式。

- Resource 级 agent id：`resource-semantic-understanding`
- Catalog 级 agent id：`catalog-semantic-understanding`
- 调用方式：`POST /api/bkn-agent/v1/run`
- 查询方式：`GET /api/bkn-agent/v1/tasks/{task_id}`
- 模型调用：由 bkn-agent 经 mf-model-api 完成

Vega 不实现 agent loop、prompt 管理或模型调用，只负责构造输入、创建 profile 记录、查询任务状态、校验输出并应用结果。

Resource 级 Agent 输入：

```json
{
  "resource": {
    "id": "res-1",
    "name": "orders",
    "category": "table",
    "database": "sales",
    "source_identifier": "orders",
    "source_description": "订单主表",
    "schema_definition": [
      {
        "name": "order_id",
        "type": "string",
        "original_name": "order_id",
        "original_type": "varchar",
        "original_description": "订单ID"
      }
    ]
  },
  "sample_rows": [
    {
      "order_id": "O202607140001"
    }
  ],
  "options": {
    "language": "zh-CN",
    "apply_mode": "fill_empty",
    "confidence_threshold": 0.75,
    "include_sample_rows": true,
    "sample_policy": {
      "masked": true,
      "max_rows": 20
    }
  }
}
```

Resource 级 Agent 输出：

```json
{
  "confidence": 0.86,
  "table": {
    "display_name": "订单表",
    "description": "记录用户订单主数据，包括订单标识、状态和时间等信息。",
    "confidence": 0.86
  },
  "fields": [
    {
      "name": "order_id",
      "display_name": "订单 ID",
      "description": "订单唯一标识。",
      "semantic_type": "identifier",
      "confidence": 0.94
    }
  ],
  "warnings": [
    "样本行数量较少，部分字段语义仍需人工确认。"
  ]
}
```

`fields[].name` 必须匹配当前 `Property.Name`。

`include_sample_rows=true` 时，Vega 在执行语义理解任务时临时查询样本数据，并在权限校验和脱敏后写入 agent 输入的 `sample_rows`。`sample_rows` 字段名必须匹配当前 `Property.Name`，且只允许包含当前 resource 的字段。Vega 不将样本数据写入 `Resource`、`schema_definition` 或逻辑视图；实际发送给 agent 的结构化输入保存在 `f_input` 中用于审计。

Catalog 级 Agent 输入：

```json
{
  "catalog": {
    "id": "catalog-1",
    "name": "sales"
  },
  "resources": [
    {
      "id": "res-1",
      "name": "orders",
      "display_name": "订单表",
      "description": "记录用户订单主数据。",
      "category": "table",
      "database": "sales",
      "source_identifier": "orders",
      "source_description": "订单主表",
      "schema_definition": [
        {
          "name": "order_id",
          "type": "string",
          "original_name": "order_id",
          "original_type": "varchar",
          "display_name": "订单 ID",
          "description": "订单唯一标识。"
        }
      ]
    }
  ],
  "existing_logic_views": [
    {
      "id": "view-1",
      "name": "customer_order_summary",
      "display_name": "客户订单汇总",
      "description": "按客户聚合订单数量、订单金额和最近下单时间。",
      "source_resources": ["res-1", "res-2"],
      "logic_definition": {
        "type": "sql",
        "query": "select ..."
      }
    }
  ],
  "options": {
    "language": "zh-CN",
    "apply_mode": "fill_empty",
    "confidence_threshold": 0.75
  }
}
```

Catalog 级 Agent 输出：

```json
{
  "confidence": 0.84,
  "logic_views": [
    {
      "action": "update",
      "target_resource_id": "view-1",
      "name": "customer_order_summary",
      "display_name": "客户订单汇总",
      "description": "按客户聚合订单数量、订单金额和最近下单时间。",
      "source_resources": ["res-1", "res-2"],
      "logic_definition": {
        "type": "sql",
        "query": "select ..."
      },
      "confidence": 0.82
    }
  ],
  "obsolete_logic_views": [
    {
      "target_resource_id": "view-2",
      "reason": "该逻辑视图依赖的源表已不存在，且没有可替代字段。",
      "confidence": 0.91
    }
  ],
  "warnings": [
    "部分逻辑视图基于字段名推断，建议人工确认关联条件。"
  ]
}
```

Catalog 级输出不直接修改物理表 resource，只用于创建、更新或标记 stale `category=logicview` 的逻辑视图 resource。`logic_views[].action` 取值为 `create` / `update`；`update` 必须携带 `target_resource_id`。一次 catalog 级任务可以同时返回 `create` 和 `update` 结果；不需要处理的既有逻辑视图不出现在输出中。需要废弃的既有逻辑视图进入 `obsolete_logic_views`，Vega 达到应用条件后只将对应 resource 标记为 stale，不物理删除。

## 5. Vega 流程

Resource 级内部触发接口：

```http
POST /api/vega-backend/in/v1/resources/{id}/semantic-understanding
```

请求：

```json
{
  "apply_mode": "fill_empty",
  "language": "zh-CN",
  "confidence_threshold": 0.75,
  "include_sample_rows": true,
  "sample_policy": {
    "masked": true,
    "max_rows": 20
  }
}
```

Catalog 级内部触发接口：

```http
POST /api/vega-backend/in/v1/catalogs/{id}/semantic-understanding
```

请求：

```json
{
  "apply_mode": "fill_empty",
  "language": "zh-CN",
  "confidence_threshold": 0.75
}
```

执行流程：

```text
Resource 级任务：
1. 读取 resource 详情与 schema_definition。
2. 校验 `include_sample_rows` 和 `sample_policy`。
3. `include_sample_rows=true` 时，Vega 按 `sample_policy` 查询、权限校验并脱敏样本数据。
4. 基于原始元数据、样本配置和样本数据构造 agent 输入并计算 input_hash。
5. 查询是否存在相同 f_input_hash 的 pending/running profile。
6. 若存在，直接返回该 profile，不重复创建 bkn-agent task。
7. 若不存在，调用 bkn-agent /run 创建任务。
8. 创建 t_semantic_understanding_profile 记录，保存 f_input、f_input_hash，状态为 pending。
9. 查询 bkn-agent task 状态。
10. 任务完成后保存 agent 原始输出、置信度和最低置信分阈值；warnings 保留在 `f_result_json` 中。
11. 校验 agent 输出字段集合。
12. 判断置信分是否达到最低阈值。
13. 达到阈值时，按 apply_mode 将展示结果投影到 Resource 与 schema_definition。
14. 未达到阈值时，不 apply，profile 状态保持 succeeded，applied 为 false。
15. 更新 profile applied/applied_time/status。

Catalog 级任务：
1. 读取 catalog 下全部可参与理解的 resource 详情。
2. 读取 catalog 下当前已存在的逻辑视图 resource。
3. 基于 catalog、resource 原始元数据、当前展示字段和已存在逻辑视图构造 agent 输入并计算 input_hash。
4. 查询是否存在相同 f_input_hash 的 pending/running catalog profile。
5. 若存在，直接返回该 profile，不重复创建 bkn-agent task。
6. 若不存在，调用 bkn-agent /run 创建任务。
7. 创建 t_semantic_understanding_profile 记录，scope 为 catalog，保存 f_input、f_input_hash，状态为 pending。
8. 查询 bkn-agent task 状态。
9. 任务完成后保存 agent 原始输出、置信度和最低置信分阈值；warnings 保留在 `f_result_json` 中。
10. 校验 logic view 输出、obsolete logic view 输出、action、target_resource_id、source_resources 和 logic_definition。
11. 判断置信分是否达到最低阈值。
12. 达到阈值时，按 action 和 apply_mode 创建或更新逻辑视图 resource，并将 obsolete logic view 标记为 stale。
13. 未达到阈值时，不 apply，profile 状态保持 succeeded，applied 为 false。
14. 更新 profile catalog_apply_detail_json/applied/applied_time/status；resource 级任务不写 catalog_apply_detail_json。
```

查询接口：

```http
GET /api/vega-backend/in/v1/resources/{id}/semantic-understanding/{profile_id}
GET /api/vega-backend/in/v1/catalogs/{id}/semantic-understanding/{profile_id}
```

响应：

```json
{
  "id": "profile-1",
  "task_id": "agent-task-1",
  "scope": "resource",
  "resource_id": "res-1",
  "status": "succeeded",
  "apply_mode": "fill_empty",
  "confidence_threshold": 0.75,
  "applied": true,
  "warnings": []
}
```

## 6. 应用规则

`apply_mode`：

| 值 | 规则 |
| --- | --- |
| `dry_run` | 只保存 profile，不应用展示字段或逻辑视图变更 |
| `fill_empty` | 只填充空的 `DisplayName` / `Description` |
| `force` | 覆盖 `DisplayName` / `Description` |

置信分阈值：

- `confidence_threshold` 取值范围为 `[0, 1]`。
- Resource 级和 Catalog 级任务均以 agent 顶层 `confidence` 作为 `f_confidence`。
- `f_confidence` 低于阈值时，整次任务结果不 apply。
- 字段、逻辑视图和废弃建议的细粒度 `confidence` 保存到 `f_confidence_detail_json`，并随 agent 原始输出保留在 `f_result_json`，用于审计和人工确认，不作为 profile 主置信分。
- 当 `f_confidence` 低于阈值时，profile 状态为 `succeeded`，`f_applied=false`。
- `dry_run` 模式仍保存 profile 和置信分，但不执行 apply。

Resource 级应用字段：

- `Resource.DisplayName`
- `Resource.Description`

字段级应用字段：

- `Property.DisplayName`
- `Property.Description`

Catalog 级应用对象：

- 只创建、更新或标记 stale `Resource.Category=logicview` 的逻辑视图 resource。
- Catalog 级 agent 输入必须包含当前 catalog 下已存在的逻辑视图 resource，用于判断逻辑视图应新建、修改或无需处理。
- Catalog 级 agent 输出只包含需要新建或修改的逻辑视图；无需处理的既有逻辑视图不输出。
- 同一次 catalog 级任务允许同时创建新的逻辑视图和更新既有逻辑视图。
- `action=create` 时创建新的逻辑视图 resource。
- `action=update` 时只更新 `target_resource_id` 指向的既有逻辑视图 resource。
- `obsolete_logic_views` 表示废弃建议；达到置信分阈值且 `apply_mode` 不是 `dry_run` 时，Vega 将对应逻辑视图 resource 标记为 stale。
- stale 标记只作用于 `existing_logic_views` 中的逻辑视图 resource，不物理删除 resource。
- 逻辑视图的 `LogicDefinition` 必须引用当前 catalog 下存在且允许被引用的 resource。
- 逻辑视图可以基于单张物理表拆分，也可以基于多张表合并。
- Catalog 级任务不修改物理表的 `Name`、`DisplayName`、`Description` 或 `schema_definition`。

任何模式下都不修改：

- `Resource.ID`
- `Resource.Name`
- `Resource.SourceIdentifier`
- `Resource.Database`
- `Resource.SourceDescription`
- `Property.Name`
- `Property.Type`
- `Property.OriginalName`
- `Property.OriginalType`
- `Property.OriginalDescription`

## 7. Discover 规则

Discover 只更新源端事实字段：

- `Resource.SourceIdentifier`
- `Resource.Database`
- `Resource.SourceDescription`
- `Property.OriginalName`
- `Property.OriginalType`
- `Property.OriginalDescription`

Discover 不覆盖以下语义展示字段：

- `Resource.DisplayName`
- `Resource.Description`
- `Property.DisplayName`
- `Property.Description`

Discover 不更新 semantic profile 状态。应用或查询 profile 时，通过 `f_input_hash` 与当前 resource 或 catalog 输入 hash 比对判断结果是否仍可用。

未完成的语义理解任务不阻塞 resource 更新、schema 修改或 discover 任务。语义理解是派生任务，不能反向锁住源端事实同步流程。

并发规则：

- 存在 `pending` / `running` semantic profile 时，允许 schema 修改和 discover 继续执行。
- schema 修改或 discover 改变原始元数据后，旧 profile 的 `f_input_hash` 与当前 resource 或 catalog 输入 hash 不匹配，旧任务结果不可应用。
- 旧任务完成后仍保存 `f_result_json` 和置信度，但 `f_applied` 保持 false。
- 触发新的语义理解时，如果已存在相同 `f_input_hash` 的 `pending` / `running` profile，直接返回该 profile；如果 hash 不同，则创建新的 profile。
- bkn-agent 支持取消任务时，Vega 可对 hash 已失效的旧任务发起取消；取消失败或不支持取消时，旧任务自然完成后按 hash 校验拒绝应用。

## 8. 扫描任务配置

Discover 请求和 discover schedule 支持配置扫描完成后的语义理解任务。

```json
{
  "semantic_understanding": {
    "resource": {
      "enabled": true,
      "apply_mode": "fill_empty",
      "confidence_threshold": 0.75
    },
    "catalog": {
      "enabled": true,
      "apply_mode": "dry_run",
      "confidence_threshold": 0.75
    }
  }
}
```

配置规则：

- `resource.enabled=true` 时，discover 完成后为本次新增或原始元数据发生变化的 resource 发起 resource 级语义理解。
- `catalog.enabled=true` 时，discover 完成后为当前 catalog 发起 catalog 级语义理解。
- 同一次 discover 同时启用 resource 级和 catalog 级语义理解时，Vega 固定先执行 resource 级任务；resource 级任务完成或失败后，再触发 catalog 级任务。
- `confidence_threshold` 未设置时使用系统默认值。
- 语义理解任务失败不改变 discover 任务结果。
- discover 任务状态不等待语义理解任务完成；语义理解任务通过 `t_semantic_understanding_profile` 独立记录执行状态。

## 9. 校验规则

应用 agent 输出前必须校验：

1. `fields[].name` 必须存在于当前 `schema_definition`。
2. `fields[].name` 不得重复。
3. agent 输出不得要求新增、删除、重命名字段。
4. agent 输出不得修改字段类型、原始字段名或原始注释。
5. `display_name` 长度不得超过 `MaxLength_PropertyDisplayName`。
6. `description` 长度不得超过 `MaxLength_PropertyDescription`。
7. Agent 输出的顶层 `confidence` 和细粒度 `confidence` 必须在 `[0, 1]`。
8. `f_input_hash` 必须匹配当前 resource 或 catalog 输入。
9. `confidence_threshold` 必须在 `[0, 1]`。
10. Agent 输出的顶层 `confidence` 低于 `confidence_threshold` 时不得 apply。
11. `include_sample_rows=true` 时必须配置 `sample_policy`。
12. `options.sample_policy.masked` 必须为 true。
13. Vega 查询到的 `sample_rows` 字段名必须存在于当前 `schema_definition`。
14. Catalog 级输出的 `action` 必须是 `create` / `update`。
15. `action=update` 时，`target_resource_id` 必须存在于输入的 `existing_logic_views`。
16. `action=create` 时不得携带既有逻辑视图的 `target_resource_id`。
17. Catalog 级输出的 `source_resources` 必须存在于当前 catalog。
18. Catalog 级输出的 `logic_definition` 必须通过现有逻辑视图校验。
19. `obsolete_logic_views[].target_resource_id` 必须存在于输入的 `existing_logic_views`。
20. Catalog 级任务不得创建物理类 resource。
21. Catalog 级任务不得物理删除 resource；废弃逻辑视图只能标记为 stale。

校验失败时不应用任何变更，profile 状态置为 `failed` 并记录 `f_failure_detail`。

## 10. 权限与安全

1. 语义理解接口只放在 `/in` 内部路由下。
2. Vega 调用 bkn-agent 使用平台服务身份。
3. 终端用户流量不得直接访问 bkn-agent。
4. 默认不向 agent 发送 sample rows。
5. `include_sample_rows=true` 时，Vega 临时查询样本数据，并且必须完成权限校验和脱敏后才能进入 agent 输入。
6. Vega 不将样本数据写入 `Resource`、`schema_definition` 或逻辑视图；实际发送给 agent 的结构化输入保存在 `f_input` 中用于审计。
7. Agent 输出只作为语义结果，不作为权限判断依据。

## 11. 验收清单

- [ ] Discover 后原始表名、原始表注释、原始字段名、原始字段类型、原始字段注释可完整保留。
- [ ] Agent 结果不会修改任何原始元数据字段。
- [ ] Agent 任务状态、原始输出和置信度保存在 `t_semantic_understanding_profile`，warnings 保留在 `f_result_json`。
- [ ] 语义理解任务可设置最低置信分阈值。
- [ ] 低于最低置信分阈值的 agent 结果不会 apply。
- [ ] `include_sample_rows=true` 时，Vega 会临时查询样本数据，并在权限校验和脱敏后发送给 agent。
- [ ] Resource 级语义理解可生成并应用表展示名、表描述、字段展示名和字段描述。
- [ ] Catalog 级语义理解可基于当前 catalog 全部 resource 生成逻辑视图建议。
- [ ] Catalog 级语义理解会带入当前已存在的逻辑视图参与判断。
- [ ] Catalog 级语义理解可创建 `logicview` resource，支持单表拆分和多表合并。
- [ ] Catalog 级语义理解可在一次任务中同时输出逻辑视图的新建和修改动作。
- [ ] Catalog 级语义理解可输出既有逻辑视图的废弃建议，并将符合条件的逻辑视图标记为 stale。
- [ ] 扫描任务可配置 resource 级和 catalog 级语义理解任务。
- [ ] `dry_run` 不写回 resource 主体、schema 或逻辑视图变更。
- [ ] `fill_empty` 不覆盖已有人工展示名和描述。
- [ ] `force` 只覆盖展示名和描述。
- [ ] Agent 输出未知字段、重复字段或 schema 过期时不会写回。
- [ ] Catalog 级输出引用不存在的 resource 或生成无效逻辑视图时不会应用。
- [ ] Discover 重扫不会覆盖 agent/人工展示字段。
- [ ] Agent 失败不影响 resource 查询、构建和数据访问。

## 12. 失败条件

- Agent 结果覆盖源端事实字段。
- Agent 修改 `Resource.Name` 或 `Property.Name`。
- Catalog 级任务修改物理表 resource。
- Catalog 级任务创建非 `logicview` resource。
- Catalog 级任务未带入既有逻辑视图，导致重复创建已有业务视图。
- Catalog 级 `update` 输出引用不存在的逻辑视图。
- Catalog 级任务物理删除 resource。
- Catalog 级任务将非逻辑视图 resource 标记为 stale。
- 字段语义类型、置信度、warnings 写入 `schema_definition`。
- Agent 任务状态或原始输出写入 `Resource` 或 `Extensions`。
- 终端用户流量可直接访问 bkn-agent。
- 未经脱敏和权限校验向 agent 发送样本数据。
