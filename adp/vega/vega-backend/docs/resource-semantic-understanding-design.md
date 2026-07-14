# Resource 语义理解设计

> 状态：终稿
> 范围：vega-backend resource 元数据增强
> 关联：bkn-agent Epic #202

## 1. 目标

Vega resource 元数据分为两类：

- 原始元数据：由 catalog discover 从源端扫描得到，表示源端事实。
- 语义元数据：由人工或 agent 基于原始元数据生成，表示面向业务使用者的解释和展示内容。

本设计引入 bkn-agent 对 resource 执行一次性语义理解任务，生成表级和字段级语义结果。Vega 负责保存当前已应用的展示结果，并用独立表管理 agent 任务、原始输出、置信度、warnings 和应用状态。

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

`Property` 不新增 `SemanticType`、`SemanticConfidence` 等语义分析字段。字段语义类型、字段级置信度、字段 warnings 和 agent 原始输出只保存在 `t_resource_semantic_profile`。

## 3. 语义结果表

新增 `t_resource_semantic_profile` 管理 agent 任务和语义产物。

```text
t_resource_semantic_profile
- f_id
- f_resource_id
- f_task_id
- f_agent_id
- f_input
- f_input_hash
- f_status
- f_apply_mode
- f_result_json
- f_warnings_json
- f_table_confidence
- f_field_confidence_json
- f_applied
- f_applied_time
- f_failure_detail
- f_create_time
- f_update_time
```

字段语义：

| 字段 | 说明 |
| --- | --- |
| `f_resource_id` | 关联 resource |
| `f_task_id` | bkn-agent 任务 ID |
| `f_agent_id` | 执行语义理解的 agent ID |
| `f_input` | 发送给 bkn-agent 的完整结构化输入，用于审计和重放 |
| `f_input_hash` | 基于 resource 原始元数据和 schema 生成，用于判断任务结果是否仍匹配当前 resource |
| `f_status` | `pending` / `running` / `succeeded` / `failed` |
| `f_apply_mode` | `dry_run` / `fill_empty` / `force` |
| `f_result_json` | agent 原始结构化输出，包含表级和字段级语义结果 |
| `f_warnings_json` | agent 输出的 warnings |
| `f_table_confidence` | 表级语义置信度 |
| `f_field_confidence_json` | 字段级置信度，按字段名索引 |
| `f_applied` | agent 结果是否已投影到 `t_resource` 和 `schema_definition` |
| `f_applied_time` | 应用时间 |
| `f_failure_detail` | 失败详情 |

`t_resource_semantic_profile` 是语义任务与历史产物的管理表；`Resource` 和 `schema_definition` 只保存当前已应用的展示结果。

## 4. Agent 接入

Resource 语义理解使用 bkn-agent 一次性任务模式。

- agent id：`resource-semantic-understanding`
- 调用方式：`POST /api/bkn-agent/v1/run`
- 查询方式：`GET /api/bkn-agent/v1/tasks/{task_id}`
- 模型调用：由 bkn-agent 经 mf-model-api 完成

Vega 不实现 agent loop、prompt 管理或模型调用，只负责构造输入、创建 profile 记录、查询任务状态、校验输出并应用结果。

Agent 输入：

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
  "options": {
    "language": "zh-CN",
    "apply_mode": "fill_empty",
    "include_sample_rows": false
  }
}
```

Agent 输出：

```json
{
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
    "未提供样本数据，部分字段语义仅基于名称和注释推断。"
  ]
}
```

`fields[].name` 必须匹配当前 `Property.Name`。

## 5. Vega 流程

内部触发接口：

```http
POST /api/vega-backend/in/v1/resources/{id}/semantic-understanding
```

请求：

```json
{
  "apply_mode": "fill_empty",
  "language": "zh-CN",
  "include_sample_rows": false
}
```

执行流程：

```text
1. 读取 resource 详情与 schema_definition。
2. 基于原始元数据构造 agent 输入并计算 input_hash。
3. 查询是否存在相同 f_input_hash 的 pending/running profile。
4. 若存在，直接返回该 profile，不重复创建 bkn-agent task。
5. 若不存在，调用 bkn-agent /run 创建任务。
6. 创建 t_resource_semantic_profile 记录，保存 f_input、f_input_hash，状态为 pending。
7. 查询 bkn-agent task 状态。
8. 任务完成后保存 agent 原始输出、置信度和 warnings。
9. 校验 agent 输出字段集合。
10. 按 apply_mode 将展示结果投影到 Resource 与 schema_definition。
11. 更新 profile applied/applied_time/status。
```

查询接口：

```http
GET /api/vega-backend/in/v1/resources/{id}/semantic-understanding/{profile_id}
```

响应：

```json
{
  "id": "profile-1",
  "task_id": "agent-task-1",
  "resource_id": "res-1",
  "status": "succeeded",
  "apply_mode": "fill_empty",
  "applied": true,
  "warnings": []
}
```

## 6. 应用规则

`apply_mode`：

| 值 | 规则 |
| --- | --- |
| `dry_run` | 只保存 profile，不写回 `Resource` 或 `schema_definition` |
| `fill_empty` | 只填充空的 `DisplayName` / `Description` |
| `force` | 覆盖 `DisplayName` / `Description` |

表级应用字段：

- `Resource.DisplayName`
- `Resource.Description`

字段级应用字段：

- `Property.DisplayName`
- `Property.Description`

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

Discover 不更新 semantic profile 状态。应用或查询 profile 时，通过 `f_input_hash` 与当前 resource 原始元数据 hash 比对判断结果是否仍可用。

未完成的语义理解任务不阻塞 resource 更新、schema 修改或 discover 任务。语义理解是派生任务，不能反向锁住源端事实同步流程。

并发规则：

- 存在 `pending` / `running` semantic profile 时，允许 schema 修改和 discover 继续执行。
- schema 修改或 discover 改变原始元数据后，旧 profile 的 `f_input_hash` 与当前 hash 不匹配，旧任务结果不可应用。
- 旧任务完成后仍保存 `f_result_json`、warnings 和置信度，但 `f_applied` 保持 false。
- 触发新的语义理解时，如果已存在相同 `f_input_hash` 的 `pending` / `running` profile，直接返回该 profile；如果 hash 不同，则创建新的 profile。
- bkn-agent 支持取消任务时，Vega 可对 hash 已失效的旧任务发起取消；取消失败或不支持取消时，旧任务自然完成后按 hash 校验拒绝应用。

## 8. 校验规则

应用 agent 输出前必须校验：

1. `fields[].name` 必须存在于当前 `schema_definition`。
2. `fields[].name` 不得重复。
3. agent 输出不得要求新增、删除、重命名字段。
4. agent 输出不得修改字段类型、原始字段名或原始注释。
5. `display_name` 长度不得超过 `MaxLength_PropertyDisplayName`。
6. `description` 长度不得超过 `MaxLength_PropertyDescription`。
7. `confidence` 必须在 `[0, 1]`。
8. `f_input_hash` 必须匹配当前 resource 原始元数据和 schema。

校验失败时不写回 `Resource` 或 `schema_definition`，profile 状态置为 `failed` 并记录 `f_failure_detail`。

## 9. 权限与安全

1. 语义理解接口只放在 `/in` 内部路由下。
2. Vega 调用 bkn-agent 使用平台服务身份。
3. 终端用户流量不得直接访问 bkn-agent。
4. 默认不向 agent 发送 sample rows。
5. Agent 输出只作为语义结果，不作为权限判断依据。

## 10. 验收清单

- [ ] Discover 后原始表名、原始表注释、原始字段名、原始字段类型、原始字段注释可完整保留。
- [ ] Agent 结果不会修改任何原始元数据字段。
- [ ] Agent 任务状态、原始输出、置信度、warnings 保存在 `t_resource_semantic_profile`。
- [ ] `dry_run` 不写回 resource 主体和 schema。
- [ ] `fill_empty` 不覆盖已有人工展示名和描述。
- [ ] `force` 只覆盖展示名和描述。
- [ ] Agent 输出未知字段、重复字段或 schema 过期时不会写回。
- [ ] Discover 重扫不会覆盖 agent/人工展示字段。
- [ ] Agent 失败不影响 resource 查询、构建和数据访问。

## 11. 失败条件

- Agent 结果覆盖源端事实字段。
- Agent 修改 `Resource.Name` 或 `Property.Name`。
- 字段语义类型、置信度、warnings 写入 `schema_definition`。
- Agent 任务状态或原始输出写入 `Resource` 或 `Extensions`。
- 终端用户流量可直接访问 bkn-agent。
- 未经脱敏和权限校验向 agent 发送样本数据。
