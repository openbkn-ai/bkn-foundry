# Resource 索引配置归属设计

状态：终稿
关联 Issue：[#221](https://github.com/openbkn-ai/bkn-foundry/issues/221)

## 最终决策

1. resource 是索引语义的唯一配置入口。
2. 字段级索引语义放在 `schema_definition[].features`。
3. 字段级配置覆盖放在 `feature.config`。
4. resource 级默认值和跨字段构建策略放在 `resource.index_config`。
5. build task 不接受客户端传入的索引配置；它只保存创建时由服务端从 resource 派生出的不可变构建快照。
6. `model_dimensions` 只由模型服务解析得到，不进入客户端可写配置。

本次 PR 内一次性完成最终模型，不保留旧 create task 入参兼容路径：

- `POST /build-tasks` 只接受 `resource_id` 和 `mode`。
- `CreateBuildTaskRequest` 移除索引配置字段。
- resource 侧补齐 `field.features`、`feature.config`、`index_config` 的完整配置模型。
- task 侧索引字段只作为服务端生成的不可变构建快照保留。
- 不做历史 task 回填或旧请求体兼容。

## 背景

当前 build task 同时承载了两类语义：

- 构建执行记录：`status`、`synced_count`、`vectorized_count`、`error_msg`、`create_time`
- 索引配置版本：`embedding_fields`、`build_key_fields`、`fulltext_fields`、`fulltext_analyzer`、`embedding_model`、`model_dimensions`

这会让 task 既像一次后台执行，又像一个可查询索引版本。随着 task 参与生成 `BuildIndexName(resource_id, task_id)`，这些索引配置字段会直接决定 OpenSearch mapping 和后续查询能力。

本设计确定的边界是：

```text
resource 描述数据和索引语义
build task 描述一次构建执行
```

也就是说，哪些字段要做全文、哪些字段要做向量、使用什么模型和维度，都属于 resource 的配置或 resource schema 的派生语义，不由 task 独立持有。

## 设计目标

1. build task 不再直接管理索引配置细节。
2. resource 成为索引语义的唯一来源。
3. 每次构建使用 resource 在创建 task 时的索引配置快照。
4. task 仍然可以作为索引版本标识参与 index name，但不再作为配置编辑入口。
5. resource 索引配置变化时，通过创建新 task 生成新 index version，成功后再切换 `resource.LocalIndexName`。

## 非目标

- 不在 task 上恢复任何配置编辑接口。
- 不允许通过 task 原地修改已完成索引的 mapping。
- 不把模型维度等派生参数开放给客户端直接设置。
- 不要求删除历史 task 时同时删除 resource 的索引配置。

## 收益

### 语义边界更清楚

resource 表达“这个资源如何被理解和检索”，build task 表达“某次构建执行”。两者分离后，task 不再同时承担执行记录、索引配置入口和索引版本配置三种身份。

### 配置一致性更强

索引配置归 resource 后，`schema_definition`、字段特征、全文配置、向量配置和当前查询索引都围绕 resource 收敛。创建 task 只读取 resource 的当前配置，不再允许请求体临时拼出一套和 resource 不一致的索引语义。

### 减少错误 mapping

`model_dimensions` 会直接进入 OpenSearch vector mapping。如果客户端可以传该值，就可能创建出和真实 embedding 模型不匹配的索引。归 resource 和模型服务派生后，维度只能来自模型服务解析结果。

### 更适合索引版本化

resource 索引配置变化时，创建新 task 生成新 index version。新索引构建成功后再切换 `LocalIndexName`，失败时旧索引仍可继续服务查询。这比在 task 上原地改配置或重建同名索引更容易审计、回滚和排障。

### API 更简单

`POST /build-tasks` 改为“对 resource 发起一次构建”，调用方不再需要理解全文字段、向量字段、模型维度等底层索引细节。索引配置修改走 resource API，构建触发走 build task API。

## 风险与缓解

### 迁移成本

当前 worker、DAO 和测试曾经读取 task 上的 `EmbeddingFields`、`BuildKeyFields`、`FulltextFields`、`FulltextAnalyzer`、`EmbeddingModel`、`ModelDimensions`。本次 PR 不保留这些分散字段，而是收敛为 task 的 `index_config` 快照。

缓解策略：

- task 表只保留 `f_index_config` 作为服务端生成的只读快照。
- 禁止 create task 请求直接设置索引配置字段。
- worker 读取 `BuildTask.IndexConfig` 快照，避免回读 resource 当前配置造成历史语义漂移。

### resource 索引配置模型需要补齐

resource 目前已有 `schema_definition.features`，应把它作为字段级索引语义的主入口。新增 `index_config` 只承载字段特征表达不了的 resource 级默认值和跨字段构建策略，避免出现两个都能配置全文/向量字段的入口。

缓解策略：

- 字段是否参与全文、向量、keyword 索引，统一放在 `schema_definition.features`。
- 单个 feature 的局部参数，放在 `feature.config`。
- 跨字段、全局默认和构建策略，放在 `index_config`。
- task 快照作为 worker 的稳定输入。

### 旧客户端破坏性变更

旧客户端如果仍在 `POST /build-tasks` 中传 `embedding_fields`、`fulltext_fields`、`build_key_fields` 等字段，本次变更后这些字段不再产生任何效果。

缓解策略：

- 不提供旧字段继续驱动 task 配置的兼容路径，避免形成第二个索引配置入口。
- API 文档和 Issue 明确该变更为本次 PR 的有意破坏性调整：旧字段会被忽略，不再参与 task 配置。
- 调用方必须先更新 resource 索引配置，再调用 `POST /build-tasks` 触发构建。

### resource update 语义变重

索引配置归 resource 后，更新 resource 不再只是改 metadata，可能意味着旧索引失效并需要重建。

缓解策略：

- 索引配置变更时，如果存在 active task，返回 409。
- 索引配置变更后明确标记旧 `LocalIndexName` stale 或清空引用。
- 新 task 成功前不把查询切换到新索引。

### 历史构建可追溯性

如果 task 不保存配置快照，而 worker 或查询侧总是回读 resource 当前配置，历史 task 就无法还原当时的构建语义。

缓解策略：

- 本次 PR 采用 task 持久化只读快照。
- 后续采用 `index_config_version` 后，task 保存版本引用。
- 不允许 task 创建后修改快照或切换版本引用。

## 配置归属

### 归 resource 所有

| 配置 | 归属 | 说明 |
|---|---|---|
| build key 字段 | `resource.index_config.build_key_fields` | 构建游标或文档 ID 规则是跨字段构建策略 |
| embedding 字段 | `schema_definition[].features` | 字段是否可向量化是字段级检索语义 |
| fulltext 字段 | `schema_definition[].features` | 字段是否可全文检索是字段级检索语义 |
| fulltext analyzer | `feature.config.analyzer` / `index_config.default_fulltext_analyzer` | 字段级配置优先，resource 级配置作为默认值 |
| embedding model | `feature.config.embedding_model` / `index_config.default_embedding_model` | 字段级配置优先，resource 级配置作为默认值 |
| model dimensions | 模型服务派生 | 由模型服务解析，不接受客户端直接设置 |

### 归 build task 所有

| 字段 | 说明 |
|---|---|
| `id` | 构建执行 ID，也可参与 index version name |
| `resource_id` | 被构建的 resource |
| `status` | 构建状态 |
| `mode` | 本次执行模式：batch / streaming |
| `synced_count` / `vectorized_count` | 执行结果统计 |
| `error_msg` / `failure_detail` | 执行失败或部分失败详情 |
| `create_time` / `update_time` | 执行记录时间 |

## 最终模型

resource 持有当前索引语义。字段级能力放在 `field.features`，resource 级默认值和构建策略放在 `index_config`：

```text
resource
  schema_definition
    field.features:
      - keyword
      - fulltext { analyzer? }
      - vector { embedding_model? }
  index_config:
    build_key_fields
    default_fulltext_analyzer
    default_embedding_model
```

`index_config` 不是新的字段索引配置中心。它只保存 `field.features` 表达不了或不适合逐字段重复表达的策略：

| 配置 | 是否放入 `index_config` | 原因 |
|---|---|---|
| 哪些字段全文索引 | 否 | 字段级语义，放 `field.features` |
| 哪些字段向量索引 | 否 | 字段级语义，放 `field.features` |
| 字段 analyzer 覆盖 | 否 | 单字段参数，放 `feature.config.analyzer` |
| 默认 analyzer | 是 | resource 级默认值 |
| 字段 embedding model 覆盖 | 否 | 单字段参数，放 `feature.config.embedding_model` |
| 默认 embedding model | 是 | resource 级默认值 |
| build key fields | 是 | 跨字段、有顺序的构建策略 |

创建 task 时，服务端从 resource 读取当前配置，并生成一份不可变构建快照：

```text
resource current index semantics
  -> create build task
  -> build task index config snapshot
  -> BuildIndexName(resource_id, task_id)
```

## Task 快照策略

本次 PR 采用 task 持久化配置快照。

task 表使用 `f_index_config` 保存索引配置快照。快照不来自 `POST /build-tasks` 请求，而是在创建 task 时由服务端从 resource 派生。

这样做的原因：

- worker 读取 `BuildTask.IndexConfig` 即可获得构建时配置。
- 历史 task 可完整还原当时构建使用的配置。
- task 不需要回读 resource 当前配置，避免 resource 后续修改影响历史构建语义。

约束：

- task 上的 `index_config` 是只读快照。
- 不提供 task 配置编辑接口。
- create task 请求不能直接设置索引配置。

后续如果索引配置继续扩展，可以演进为 task 引用 resource index config version：

新增 resource 索引配置版本表或版本字段，task 只保存 `index_config_version`。

该演进不是本次 PR 范围。引入前必须保证：

- 历史 task 仍可还原当时构建使用的配置。
- worker 可以按版本稳定读取构建配置。
- 老 task 的快照语义不被破坏。

## API 语义调整

### Create build task

`POST /build-tasks` 只表达“对某个 resource 发起一次构建”：

```json
{
  "resource_id": "...",
  "mode": "batch"
}
```

不再接受以下索引配置字段：

```text
build_key_fields
embedding_fields
fulltext_fields
fulltext_analyzer
embedding_model
model_dimensions
```

请求中出现上述字段时，服务端忽略这些旧字段，不把它们作为配置入口。调用方必须先更新 resource 配置，再创建 build task。

### Update resource index config

索引配置变化应走 resource 更新路径：

```text
update resource index config
  -> clear or mark old LocalIndexName stale
  -> create new build task
  -> build new index
  -> switch LocalIndexName after success
```

如果同一 resource 存在 active task，必须拒绝修改索引配置，避免构建过程中配置漂移。

## Worker 语义

worker 不应从请求语义理解索引配置，而应读取构建快照：

```text
task snapshot
  embedding fields
  fulltext fields
  analyzer
  embedding model id
  model dimensions
```

这些快照必须满足：

- 创建 task 时一次性生成。
- task 创建后不可变。
- 来源是 resource 当前配置和模型服务解析结果。
- `model_dimensions` 只由模型服务返回，不能由客户端传入。

## LocalIndexName 切换语义

resource 的 `LocalIndexName` 仍表示当前查询侧使用的本地索引。

```text
old task / old index remains serving
new task builds new index
new task completed successfully
resource.LocalIndexName -> BuildIndexName(resource_id, new_task_id)
```

如果新 task 失败：

- `LocalIndexName` 不切换到失败索引。
- 旧索引继续服务查询，除非 resource schema/index config 已被明确标记为 stale。

## PR 实施步骤与 Commit 拆分

本次 PR 不做历史兼容，按以下顺序拆分 commit。每个 commit 应保持可 review，最终 PR 合并前整体测试通过。

### Commit 1：文档终稿

- 新增并定稿 `adp/vega/vega-backend/docs/resource-index-config-ownership.md`。
- 明确 resource 是索引语义唯一入口。
- 明确 `field.features`、`feature.config`、`resource.index_config`、task 快照的职责边界。
- 关联 Issue #221。

建议提交信息：

```text
docs(vega-backend): define resource index config ownership
```

### Commit 2：Resource 索引配置模型

- 在 resource 接口模型中新增 `index_config`。
- `index_config` 只承载 resource 级默认值和跨字段构建策略：
  - `build_key_fields`
  - `default_fulltext_analyzer`
  - `default_embedding_model`
- 字段级索引能力继续由 `schema_definition[].features` 表达。
- 字段级配置覆盖继续由 `feature.config` 表达。
- 更新 resource 持久化、读取、更新和相关测试。
- 更新 resource update guard：索引配置变化时，如果存在 active build task，返回 409。

建议提交信息：

```text
feat(vega-backend): add resource index config model
```

### Commit 3：Create build task API 收口

- `POST /build-tasks` 的业务语义改为“对 resource 发起一次构建”。
- 请求体只保留：
  - `resource_id`
  - `mode`
- 从 `CreateBuildTaskRequest` 中移除：
  - `build_key_fields`
  - `embedding_fields`
  - `fulltext_fields`
  - `fulltext_analyzer`
  - `embedding_model`
  - `model_dimensions`
- 删除 handler 中基于 request 索引字段的校验逻辑。
- create task 请求携带旧字段时忽略旧字段，不把它们作为配置入口。
- 更新 handler 单测。

建议提交信息：

```text
fix(vega-backend): restrict build task create request
```

### Commit 4：Build task 快照从 resource 派生

- `CreateBuildTask` 不再从 request 读取索引配置。
- service 从 resource 当前状态派生 task 快照：
  - 从 resource schema/features 派生 `index_config.features[field].vector`
  - 从 resource schema/features 派生 `index_config.features[field].fulltext`
  - 从 `resource.index_config.build_key_fields` 派生 `index_config.build_key_fields`
  - 从 `fulltext` feature 的 `config.analyzer` 或 `index_config.default_fulltext_analyzer` 派生字段级 analyzer 快照
  - 从 `vector` feature 的 `config.embedding_model` 或 `index_config.default_embedding_model` 得到字段级 embedding model
  - 通过模型服务解析得到字段级 embedding dimensions
- 如果无法解析模型或维度，create task 失败。
- 如果 resource 当前没有完整索引配置，create task 失败，而不是回退读取 request 字段。

- task 表上的 `f_index_config` 不作为客户端配置入口。
- `f_index_config` 的语义是“创建时由服务端生成的只读快照”。
- task 创建后不允许修改快照。

- worker 读取 `BuildTask.IndexConfig` 快照。
- worker 不需要知道快照来自 request 还是 resource，只依赖“task 快照不可变”这个约束。

建议提交信息：

```text
fix(vega-backend): derive build task snapshot from resource
```

### Commit 5：索引切换与测试收口

- 补齐 `LocalIndexName` 切换相关回归测试。
- 补齐 resource index config 更新时 active task guard 的测试。
- 补齐 create task 从 `field.features` / `feature.config` / `resource.index_config` 派生快照的测试。
- 确认新 task 失败时不覆盖旧 `LocalIndexName`。
- 清理不再使用的 request 字段测试和旧校验代码。

建议提交信息：

```text
test(vega-backend): cover resource-owned build task config
```

### 完成后的系统语义

```text
client -> POST /build-tasks(resource_id, mode)
service -> read resource index config
service -> create immutable task snapshot
worker -> read task snapshot and build index
```

PR 完成后，客户端不能再通过 create task 直接决定全文字段、向量字段、模型和维度；resource 是唯一索引语义入口。

## Resource 配置模型

resource 需要明确两层配置：`field.features` 是主模型，`index_config` 是补充策略。

字段级配置放在 `schema_definition.features`：

| 配置 | 表达 |
|---|---|
| 字段是否参与全文索引 | field feature: `fulltext` |
| 字段是否参与向量索引 | field feature: `vector` |
| 字段是否作为 keyword 检索 | field feature: `keyword` |
| 字段级全文 analyzer 覆盖 | `fulltext` feature 的 `config.analyzer` |
| 字段级 embedding model 覆盖 | `vector` feature 的 `config.embedding_model` |

resource 级默认值和构建策略放在 `index_config`：

| 配置 | 说明 |
|---|---|
| `build_key_fields` | 构建游标、排序或文档 ID 规则 |
| `default_fulltext_analyzer` | 未在 feature 上指定 analyzer 时使用的默认全文 analyzer |
| `default_embedding_model` | 未在 feature 上指定 embedding model 时使用的默认模型引用 |

`model_dimensions` 不进入客户端可写配置，只能在创建 task 快照时由模型服务解析得到。

## Update resource index config

更新 resource 索引配置时必须走 resource 更新路径，而不是 build task API。

更新规则：

- 如果同一 resource 存在 active task，返回 409。
- 只允许更新明确属于索引配置的字段，不允许顺带修改源端字段。
- 更新后必须处理旧 `LocalIndexName`：
  - 如果旧索引配置与新 resource 配置不一致，清空或标记 stale。
  - 如果选择继续让旧索引服务查询，必须有显式状态表示“索引可用但配置已过期”。
- 新配置生效到查询侧之前，必须创建新 task 并成功构建新 index。

## Create build task

create task 完全依赖 resource 配置：

```text
resource schema/features + resource index_config + model service
  -> immutable task snapshot
```

如果 resource 配置不完整，create task 返回 400，提示先补齐 resource index config。

## 查询侧语义

查询侧继续使用 `resource.LocalIndexName`。

- 如果 `LocalIndexName` 为空，表示当前没有可用本地索引。
- 如果 resource 索引配置已更新但新 task 未成功，查询侧不能假装新配置已生效。
- 新 task 成功后，才将 `LocalIndexName` 切换到新 task 对应的 index。

### 后续演进：清理或版本化 task 模型

- task 配置字段可以作为只读快照长期保留。
- 如果迁移到 `index_config_version` 引用，task 表可逐步去除配置字段。
- 历史 task 的配置可追溯性必须在迁移后保持不变。

## 验收测试

- create task 请求携带 `embedding_fields` 时忽略该字段。
- create task 请求携带 `fulltext_fields` 时忽略该字段。
- create task 请求携带 `build_key_fields` 时忽略该字段。
- create task 请求携带 `embedding_model` 时忽略该字段。
- create task 请求携带 `model_dimensions` 时忽略该字段。
- create task 从 resource schema/features 派生 embedding/fulltext 字段。
- create task 从 `resource.index_config.build_key_fields` 派生 build key 快照。
- create task 从 `feature.config` 或 `index_config` 默认值派生 analyzer / embedding model。
- create task 通过模型服务派生 `model_dimensions`。
- task 创建后 resource 修改索引配置，不影响已有 task 快照。
- 新 task 成功后才切换 `resource.LocalIndexName`。
- 新 task 失败时不覆盖旧 `resource.LocalIndexName`。
- active task 存在时拒绝更新 resource 索引配置。

## 结论

build task 应该是构建执行记录，而不是索引配置入口。

索引配置属于 resource；task 只在创建时捕获 resource 配置快照，用于保证历史构建可追溯、worker 执行稳定、index version 语义清晰。
