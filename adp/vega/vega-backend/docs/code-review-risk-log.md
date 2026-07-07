# 代码 Review 风险记录

## 说明

本文用于持续记录本轮 review 中发现的问题、风险、触发条件和候选处理策略。
每个问题独立成节，便于后续拆 issue / PR。

## R1：Build Task 同时承担执行记录与索引版本，导致配置和索引生命周期语义混乱

### 背景

本轮变更后，Build Task 的业务含义明显扩展：它不再只是一个后台执行记录，还开始承担资源本地索引配置、索引版本、索引健康展示和索引清理入口。

相关变化包括：

- 创建 build task 后立即入队执行
- build task 新增 `fulltext_fields` / `fulltext_analyzer`
- build task 返回 `index_health` / `failure_detail`
- 删除 build task 会 drop `BuildIndexName(resource_id, task_id)`
- `PUT /build-tasks/:id` 可以编辑索引配置并触发 full rebuild
- worker/reconciler 会自动重试或重新驱动同一 task

这些能力单独看都有业务诉求，但组合后暴露出一个核心模型问题：

```text
Build Task 同时承担“构建执行记录”和“索引配置版本”
```

### 典型表现：原地编辑索引配置

最直接的表现是本轮新增的 build task 配置编辑能力：

- `PUT /build-tasks/:id`
- 更新 `embedding_fields` / `build_key_fields` / `fulltext_fields` / `fulltext_analyzer`
- 然后触发 full rebuild

这个接口的实际语义不是单纯"编辑任务描述"，而是：

```text
修改索引配置 -> drop 原 task 对应索引 -> 按新配置重建同名索引
```

这说明 build task 当前同时承担了两种身份：

| 身份 | 字段/行为 |
|---|---|
| 构建执行记录 | `status` / `synced_count` / `vectorized_count` / `error_msg` / `create_time` |
| 索引配置版本 | `embedding_fields` / `build_key_fields` / `fulltext_fields` / `fulltext_analyzer` / `model_dimensions` |

这两种身份放在同一条可编辑 task 记录里，会让历史记录和索引版本语义混在一起。

R2/R3/R4/R5 都可以看作这个模型问题在不同路径上的具体表现：

| 风险项 | 具体表现 |
|---|---|
| R2 | 删除 task 时不清楚是在删除历史记录，还是删除当前生效索引 |
| R3 | full rebuild 原地 drop/recreate，同一个 task/index name 对应不同 mapping |
| R4 | `LocalIndexName` 切换和旧索引清理缺少版本化边界 |
| R5 | worker 自动重试/自愈会反复驱动同一 task，进一步放大原地 rebuild 风险 |

### 当前行为

task 的索引配置来源是 `t_build_task` 这条 task 记录本身。

创建 task 时，`CreateBuildTask` 把请求里的索引相关配置写入 `interfaces.BuildTask`：

```go
buildTask := &interfaces.BuildTask{
    ID:              xid.New().String(),
    ResourceID:      resourceID,
    EmbeddingFields:  req.EmbeddingFields,
    BuildKeyFields:   req.BuildKeyFields,
    EmbeddingModel:   req.EmbeddingModel,
    ModelDimensions:  req.ModelDimensions,
    FulltextFields:   req.FulltextFields,
    FulltextAnalyzer: req.FulltextAnalyzer,
}
```

worker 执行时再读取这条 task：

| 配置 | 使用位置 |
|---|---|
| `EmbeddingFields` | `createLocalIndex` 追加 `<field>_vector` mapping；embedding worker 生成向量 |
| `ModelDimensions` | vector mapping 的 dimension |
| `FulltextFields` / `FulltextAnalyzer` | `reconcileFulltextFeatures` 写入 resource schema，再由 OpenSearch mapping/query 使用 |
| `BuildKeyFields` | batch 同步游标、排序字段、文档 ID 生成 |
| `task.ID` | 参与生成 index name：`BuildIndexName(resource.ID, task.ID)` |

因此当前模型本质上是：

```text
task.id + task.index_config => 一个具体索引
```

但 `PUT /build-tasks/:id` 会在同一个 `task.id` 上改 `task.index_config`，然后复用同一个 index name 做 destructive rebuild。

### 问题

如果索引配置变更后仍复用旧 task，或者把索引生命周期操作挂在旧 task 上，会导致同一个 task 代表多个不同含义：

| 维度 | 旧语义 | 编辑后的新语义 |
|---|---|---|
| task 记录 | 某次历史构建结果 | 最新索引配置 |
| index name | 旧配置生成的索引 | 新配置重建后的索引 |
| `synced_count` / `vectorized_count` | 旧构建进度 | 被重置或覆盖后的新构建进度 |
| `error_msg` | 旧失败原因 | 新 rebuild 失败原因 |
| audit / 排障 | 可追溯一次构建 | 历史被原地改写 |

这会引出几个连锁问题：

- 已完成 task 不是不可变历史记录，排障时无法还原当时的索引配置
- 配置变更后的 rebuild 会 drop 同一个 task index，无法保留旧版本兜底
- 如果 rebuild 失败，旧索引可能已经被删除或污染
- `resource.LocalIndexName` 仍指向同一个 index name，但这个 name 背后的 mapping 语义已经变了
- 删除 task、切换索引、计算 health 时都很难判断它到底是"任务"还是"当前索引版本"

### 更合理的语义

如果用户变更的是索引配置，建议驱动新的 build task：

```text
新的索引配置 -> 新 build task -> 新 index name -> 构建成功后切换 resource.LocalIndexName
```

这样每条 task 都是不可变的构建记录，同时也是一个明确的索引版本：

| 行为 | 建议语义 |
|---|---|
| 调整 embedding/fulltext/build key 配置 | 创建新 task |
| 新 task 构建中 | 旧 task / 旧索引继续服务查询 |
| 新 task 构建成功 | 更新 `resource.LocalIndexName` 指向新 index |
| 新 task 构建失败 | resource 继续指向旧 index |
| 清理旧版本 | 切换成功后再删除旧 index / 旧 task，或保留历史 |

这个模型下，配置变更不再需要 destructive in-place rebuild，也不需要把旧 task 的历史字段重置。

### 触发场景

典型触发流程：

1. task `t1` 已完成，生成 index `vega-build-r1-t1`
2. resource 的 `LocalIndexName` 指向 `vega-build-r1-t1`
3. 用户希望把全文字段从 `title` 改为 `title,body`
4. 当前实现会编辑 `t1`，drop `vega-build-r1-t1`，再用新配置重建同名 index
5. 这期间 `t1` 的历史配置被改写，旧索引也不再可回退

建议行为应该是：

```text
t1 保持不变
创建 t2(title,body)
t2 构建 vega-build-r1-t2
t2 成功后 resource.LocalIndexName 从 vega-build-r1-t1 切到 vega-build-r1-t2
```

### 次级风险：即使保留编辑接口，也缺少字段校验

如果短期仍保留 `PUT /build-tasks/:id`，还需要补齐字段校验。

创建 build task 时，handler 会基于 resource schema 做字段校验：

- `build_key_fields` 必须存在
- `embedding_fields` 必须存在，且字段类型只能是 `string` / `text`
- `fulltext_fields` 必须存在，且字段类型只能是 `string` / `text`

但编辑路径没有复用这组校验。它可以持久化不存在字段、非文本 embedding/fulltext 字段、无效 build key 字段，导致 task 配置、resource schema、OpenSearch mapping 和实际文档内容不一致。

### 候选处理策略

推荐把 `PUT /build-tasks/:id` 从当前语义中移除或收敛为非破坏性字段，例如只允许更新展示性 metadata。

索引配置变更走新建 task：

```text
POST /build-tasks
{
  "resource_id": "...",
  "embedding_fields": "...",
  "fulltext_fields": "...",
  "build_key_fields": "..."
}
```

或者提供更明确的 clone/rebuild API：

```text
POST /build-tasks/:id/revisions
```

但无论 API 形式如何，核心约束应是：

- 新索引配置产生新 task id
- 新 task id 产生新 index name
- 旧 task 记录不可原地改写
- 旧索引在新索引成功切换前不能被删除
- resource 的 `LocalIndexName` 只在新索引成功后切换

如果短期必须保留编辑接口，则至少补充：

| 字段 | 建议规则 |
|---|---|
| `build_key_fields` | 非空时必须全部存在 |
| `embedding_fields` | 非空时必须全部存在，且类型为 `string` / `text` |
| `fulltext_fields` | 非空时必须全部存在，且类型为 `string` / `text` |
| 逗号分隔字段 | trim 后忽略空项，必要时拒绝重复字段 |

测试建议：

- 配置变更创建新 task，不修改旧 task 配置
- 新 task 失败时，resource 仍指向旧 `LocalIndexName`
- 新 task 成功后，resource 切到新 index name
- 删除旧 task 不能影响当前生效 index
- 如果保留编辑路径，非法字段不能更新 task，不能触发 full rebuild
- `index_health.fulltext` 不应仅由 `FulltextFields` 推断成功，至少要和 task status / sync 结果绑定


## R2：删除 Build Task 可能误删使用中的索引

### 背景

近期删除语义从"有构建任务则拒绝删除资源"调整为"删除资源 / 目录时级联清理构建任务与 OpenSearch 索引"。
这个方向对资源和目录删除是合理的：上层资源即将消失，对应的任务行和索引也应一起清理，避免孤儿数据。

但同一套索引删除逻辑也被加到了单独删除 build task 的路径：

- `server/logics/build_task/build_task_service.go`
- `DeleteBuildTasks`
- 对每个待删 task 执行 `ds.Delete(BuildIndexName(resource_id, task_id))`
- 然后删除 `t_build_task` 行

这带来一个独立风险：**build task 对应的索引可能正是 resource 当前使用中的本地索引**。

### 当前行为

`DeleteBuildTasks` 当前只做这些保护：

| 条件 | 行为 |
|---|---|
| task 不存在 | 默认 404；`ignore_missing=true` 时跳过 |
| task 为 `running` / `stopping` | 409 `HasRunningExecution` |
| task 为 `init` / `stopped` / `failed` / `completed` | 允许删除 |

允许删除后，代码会无条件计算索引名：

```go
idx := interfaces.BuildIndexName(bt.ResourceID, bt.ID)
```

然后尝试删除该 OpenSearch index。删除 index 失败只记录日志，不阻断任务行删除。

### 使用中索引的判定

构建任务创建的索引名格式为：

```text
vega-build-<resource_id>-<build_task_id>
```

批量构建完成后，worker 会通过 `updateResourceIndexName` 把 `resource.LocalIndexName` 更新为当前任务索引：

```go
resource.LocalIndexName = indexName
```

资源数据查询路径会优先使用 `resource.LocalIndexName`：

- `server/logics/resource_data/resource_data_service.go`
- table resource 查询本地索引时调用 `ListDocuments(ctx, resource.LocalIndexName, ...)`

因此，只要满足：

```text
resource.local_index_name == BuildIndexName(task.resource_id, task.id)
```

该 task 的索引就是当前资源查询正在使用的索引。

### 问题

当前 `DeleteBuildTasks` 没有读取 resource，也没有比较 `resource.LocalIndexName`。

因此用户删除一个已经完成且当前生效的 build task 时，会发生：

1. task 不是 running/stopping，通过删除校验
2. 服务删除 `vega-build-<resource_id>-<task_id>` 索引
3. 服务删除 task 行
4. resource 行仍然保留原 `LocalIndexName`
5. 后续资源查询继续访问已删除索引

结果是 resource 仍显示存在，但本地索引查询失败。

### 风险

| 风险 | 影响 |
|---|---|
| 删除正在使用的本地索引 | table resource 的本地查询 / fulltext / embedding 检索失败 |
| resource 上残留悬空 `LocalIndexName` | 资源状态与真实索引状态不一致，排障困难 |
| 删除 task 后无法从 task 找回索引配置 | 任务行已删除，恢复只能重建 task 或手工修 resource/index |
| 与资源/目录级联删除语义混淆 | 上层资源删除时 drop index 合理；单独 task 删除时不一定合理 |
| 索引删除失败不阻断 task 删除 | 可能出现相反的不一致：任务行没了，但索引还留着 |

### 触发场景

典型触发流程：

1. 对 table resource 创建 batch build task
2. build task 完成，生成索引 `vega-build-r1-t1`
3. resource 的 `LocalIndexName` 被更新为 `vega-build-r1-t1`
4. 用户调用 `DELETE /build-tasks/t1`
5. 后端删除 `vega-build-r1-t1`
6. resource 仍指向 `vega-build-r1-t1`

这个场景不需要并发，也不需要任务运行中；只要删除的是当前生效 task 即可。

### 与资源 / 目录删除的区别

资源删除或目录删除时，级联删除 build task + index 是合理的：

- resource/catalog 本身会被删除
- `LocalIndexName` 不再需要保持可用
- 清理 task/index 能避免孤儿数据

单独删除 build task 时不同：

- resource 仍然存在
- `LocalIndexName` 仍然参与查询
- task 只是索引构建记录，不一定代表索引可以一起被删除

因此单独 task 删除应该有额外保护，不能完全复用级联删除语义。

### 候选处理策略

#### 方案 A：禁止删除当前生效 task（推荐）

删除 task 前读取 resource：

```text
if resource.LocalIndexName == BuildIndexName(task.ResourceID, task.ID):
    return 409
```

优点：

- 最保守，不会破坏正在使用的索引
- 与用户心智一致：正在生效的构建结果不能直接删
- 不需要定义清空 `LocalIndexName` 后资源如何降级

缺点：

- 用户想删除当前索引时，需要先切换到另一个 build task 或删除 resource
- 需要新增错误语义或复用现有 conflict 错误

#### 方案 B：删除 task 时清空 resource.LocalIndexName

如果 task 是当前生效索引，则先把 resource 的 `LocalIndexName` 清空，再删除索引和任务。

优点：

- 删除操作可以完成
- 不留下悬空引用

缺点：

- 资源查询行为会突然变化：可能回源查询，或失去本地索引能力
- 对 fulltext / embedding 消费方是破坏性变化
- 需要明确 UI 和 API 对"索引被移除"的提示

#### 方案 C：单独删除 task 不删除 index

`DELETE /build-tasks` 只删除任务行，不 drop index；资源/目录删除继续通过 cascade drop index。

优点：

- 不会破坏使用中的索引
- 区分了"删除记录"和"删除资源数据"

缺点：

- 删除非生效 task 会留下孤儿索引
- 后续需要索引清理任务或管理 API
- 与本次"避免孤儿索引"的目标相反

#### 方案 D：增加显式参数控制

例如：

```text
DELETE /build-tasks/:id?delete_index=true
```

默认只删 task，不删 index；显式参数才 drop index，并在当前生效时拒绝或要求更强确认。

优点：

- 语义清楚，可兼容两类需求
- 适合后续 UI 做二次确认

缺点：

- API 行为变复杂
- 仍需定义当前生效索引的保护规则

### 建议

短期建议采用 **方案 A：禁止删除当前生效 task**。

理由：

- 当前风险是数据可用性风险，先保守止血最合适
- resource/catalog 删除路径仍保留 cascade，不影响清理上层资源时的完整性
- 不需要引入新的降级语义
- 后续如果产品确实需要"删除当前索引"，可以再加显式 API 或参数

推荐错误行为：

| 条件 | HTTP |
|---|---|
| task 对应 index 是 resource.LocalIndexName | 409 Conflict |

错误详情建议包含：

```json
{
  "resource_id": "r1",
  "build_task_id": "t1",
  "index_name": "vega-build-r1-t1",
  "reason": "build task index is currently used by resource"
}
```

## R3：Full rebuild 先写 resource schema 再 drop 索引，查询侧可能读到半生效状态

### 背景

全文检索这次引入了两个联动变化：

1. worker 在 build 前把 `fulltext_fields` 对账写入 `resource.SchemaDefinition`
2. OpenSearch 查询侧根据 `resource.SchemaDefinition` 上的 fulltext feature 决定查询字段名

这让 resource schema 成为查询 DSL 的关键输入。

### 当前行为

`server/worker/build_handler_batch.go` 的 `executeBuild` 大致顺序是：

```text
1. reconcileFulltextFeatures(resource, task.FulltextFields, task.FulltextAnalyzer)
2. 如果 schema 有变化，立即 ra.Update(resource)
3. full rebuild 时 drop 旧 task index
4. createLocalIndex，用新 schema 创建 mapping
5. 同步数据 / 发送 embedding
6. 完成后 updateResourceIndexName
```

问题在于：步骤 2 已经让查询侧看到新 schema，但步骤 4/5/6 还没有保证成功。

### 问题

如果步骤 2 之后任一阶段失败，就可能留下半生效状态：

| 失败点 | 可能状态 |
|---|---|
| schema 已更新，drop 前失败 | 查询侧按新 schema 生成 DSL，但当前索引还是旧 mapping |
| drop 已完成，createLocalIndex 失败 | `LocalIndexName` 可能仍指向刚被删除的旧索引 |
| create 成功，数据同步失败 | 索引存在但数据不完整，resource schema 已切到新全文配置 |
| embedding 任务失败 | 文本同步可能完成，但向量可用性和 `LocalIndexName` 切换时机依赖另一个 worker |

其中最危险的是 full rebuild 针对当前生效 task 时：索引名与 task 绑定，drop 的正是 resource 当前 `LocalIndexName` 指向的索引。

### 查询侧影响

`server/logics/resource_data/resource_data_service.go` 对 table resource 的查询逻辑是：

```go
if resource.LocalIndexName != "" {
    return rds.ds.ListDocuments(ctx, resource.LocalIndexName, resource, params)
}
```

也就是说，只要 `LocalIndexName` 非空，查询就会走本地 OpenSearch，并用当前 resource schema 解析 filter。

当 resource schema 已经写入新的 fulltext feature，但本地索引仍是旧 mapping 或已经被 drop 时，会出现：

- `match` / `match_phrase` / `multi_match` 查询访问不存在的 `字段.fulltext`
- 本地索引不存在导致查询直接 500
- API 上 resource 看起来存在，task 也可能只是 failed，但查询链路不可用
- fulltext health 可能仍基于 `FulltextFields` 显示 `ok`，误导排障

### 触发场景

典型触发流程：

1. task `t1` 已完成，resource 的 `LocalIndexName` 是 `vega-build-r1-t1`
2. 用户编辑 `t1` 的全文字段或 analyzer
3. worker 先把新 fulltext feature 写入 resource schema
4. worker drop `vega-build-r1-t1`
5. 后续 create index / sync data / embedding 任一阶段失败
6. resource 仍存在，但查询本地索引失败或按不匹配的 schema 查询

这个问题不依赖并发；单次 rebuild 失败即可触发。

### 候选处理策略

更稳的方向是把 full rebuild 做成两阶段切换：

1. 使用 staging / generation index 构建新索引，例如 `vega-build-<resource>-<task>-<generation>`
2. 使用新 schema 创建 mapping 并完成数据同步
3. 成功后一次性更新 resource：
   - `SchemaDefinition`
   - `LocalIndexName`
   - 可选的 index generation / version
4. resource 更新成功后，再异步或 best-effort 删除旧索引

短期如果不引入 generation index，也建议至少：

- rebuild 当前生效 index 前显式阻断查询，返回清晰的 rebuilding 状态，而不是让查询打到被 drop 的 index
- schema 更新和 `LocalIndexName` 切换尽量放到索引创建、同步成功之后
- `index_health.fulltext` 不要只看 `FulltextFields`，应和 task status / index 可用性绑定

## R4：切换 LocalIndexName 前先删除旧索引，DB 更新失败会留下悬空引用

### 背景

batch build 完成后，worker 会通过 `updateResourceIndexName` 把 resource 指向新索引。
如果 resource 之前已经有本地索引，这个函数会先删旧索引，再更新 resource。

### 当前行为

`server/worker/build_task_common.go`：

```go
if resource.LocalIndexName != indexName {
    err := ds.Delete(ctx, resource.LocalIndexName)
    if err != nil {
        return fmt.Errorf("delete local index failed: %w", err)
    }
    resource.LocalIndexName = indexName
    return ra.Update(ctx, resource)
}
```

### 问题

这个顺序的失败语义不安全：

1. 新索引已经构建成功
2. worker 删除旧索引
3. `ra.Update(ctx, resource)` 失败
4. DB 中的 resource 仍然指向旧 `LocalIndexName`
5. 但旧索引已经被删除

结果是新索引存在但未被引用，resource 仍指向不存在的旧索引。

### 风险

| 风险 | 影响 |
|---|---|
| `LocalIndexName` 悬空 | table resource 查询本地索引失败 |
| 新索引孤儿化 | 后续无法从 resource 找到新索引，只能从 task/index name 推断 |
| rebuild 失败恢复困难 | 旧索引已删，无法自动回退到上一个可用版本 |

### 候选处理策略

更安全的顺序是：

```text
1. 保留旧索引
2. 构建新索引
3. 更新 resource.LocalIndexName 指向新索引
4. resource 更新成功后，再删除旧索引
```

如果第 3 步失败，旧索引仍可继续服务查询；如果第 4 步失败，只会留下可清理的旧索引，不会破坏当前查询。

测试建议：

- 模拟 `ra.Update` 失败时，旧索引不能被提前删除
- 模拟旧索引删除失败时，resource 应仍能指向新索引，删除失败进入日志/异步清理

### 建议测试

应补充 build task service 单测：

| 用例 | 期望 |
|---|---|
| 删除非生效 completed task | drop 对应 index，然后删 task 行 |
| 删除当前生效 completed task | 返回 409，不调用 `ds.Delete`，不删 task 行 |
| 删除当前生效 failed/stopped task | 同样返回 409 |
| 删除 running/stopping task | 仍返回原有 `HasRunningExecution` |
| resource 不存在但 task 存在 | 可按孤儿 task 处理，允许 drop index + 删 task |
| 查询 resource 失败 | 返回 500，不执行破坏性删除 |

资源 / 目录级联删除测试也应保持：

| 用例 | 期望 |
|---|---|
| 删除 resource 时存在非运行 task | 仍级联 drop index + 删 task |
| 删除 catalog 时存在非运行 task | 仍级联 drop index + 删 task |
| 删除 resource/catalog 时存在 running/stopping task | 仍整体拒绝 |

### 待确认问题

- 是否允许一个 resource 同时存在多个 build task，但只有一个 `LocalIndexName` 生效？
- 如果当前生效 task 被禁止删除，UI 是否需要提示"请先切换索引或删除资源"？
- 是否需要新增"切换当前索引"或"清空当前索引" API？
- streaming build task 的索引是否也可能写入 `LocalIndexName`，是否需要同样保护？
- 如果 `resource.LocalIndexName` 指向不存在的索引，是否需要独立健康检查或修复接口？

## R5：构建 worker 自愈增强后的并发与最终状态需要确认

### 背景

本轮 worker 稳定性有明显增强，主要解决这些问题：

- 创建 build task 后立即入队，避免 task 长期停留在 `init`
- 新增 build task reconciler，周期扫描 `init` 且队列无消息的 task 并重新入队
- worker 出队时跳过 `stopped` / `stopping`，避免排队期间被 stop 的任务复活
- batch build 修复游标不推进导致重复读同一区间的问题
- embedding worker 对 Kafka read / commit 失败、ctx cancel、空闲假死、单文档向量化失败等场景做了有界重试和恢复

这些方向整体是合理的，确实是在修复"假排队"、"构建中冻结"、"向量缺失无痕迹"等稳定性问题。

但 worker 变得更自动化以后，有几个边界行为需要确认。

### 待确认 1：reconciler 重新入队是否可能产生重复 worker

`server/worker/build_task_reconciler.go` 的逻辑是：

1. 查询 DB 中 `status = init` 的 task
2. 扫描 asynq 队列中的 build task message
3. 找出 `init` 超时且队列中没有消息的 task
4. 重新 enqueue

这个逻辑能修复"DB 里有 init task，但入队消息丢失"的问题。

需要确认的是：扫描队列和重新入队之间不是原子操作。

可能存在竞态：

```text
reconciler 扫描队列：没看到 task t1
另一个请求 / reconciler / start 操作刚好 enqueue t1
当前 reconciler 继续 enqueue t1
```

如果 asynq 没有用 task id 去重，同一个 build task 可能有多个消息并发执行。

### 影响

重复 worker 对不同模式的影响不一样：

| 模式 | 可能影响 |
|---|---|
| batch incremental | 两个 worker 可能同时读源表、写同一个 OpenSearch index、更新同一个 `synced_count` / `synced_mark` |
| batch full | 如果消息 execute type 是 full，可能重复 drop / recreate 同一个 task index |
| embedding | 同一批 doc id 可能被多个 consumer 处理，当前代码有 docID 去重和 count 封顶，但跨 worker 内存去重不共享 |
| streaming | 多个 streaming worker 可能同时消费/写入同一 task index |

当前 reconciler 重新入队使用的是 incremental：

```text
从未跑过的任务游标为空，增量等效全量；跑过一半的任务沿游标续跑
```

这个设计可以降低 destructive full rebuild 风险，但仍需要确认同一 task 并发执行是否被其他机制排除。

### 建议确认

- asynq enqueue build task 时是否应使用稳定 `TaskID` 去重，例如 `build:<taskID>:<executeType>`
- worker 开始执行前是否需要 CAS 状态转换，例如仅允许 `init -> running` 成功的 worker 继续执行
- `running` 状态下是否可能仍存在第二个同 task worker，尤其是 reconciler 和手动 start 并发时
- streaming task 是否需要额外单实例保护

### 待确认 2：embedding 硬失败重试耗尽后，task 是否会永久停在 running

`server/worker/build_handler_embedding.go` 在 `executeEmbedding` 返回错误时：

```go
err = eh.taskAccess.UpdateStatus(ctx, taskID, map[string]interface{}{"errorMsg": embed_err.Error()})
return embed_err
```

这里会写 `errorMsg`，但没有把 `status` 改为 `failed`。

这个行为可能是有意的：返回 error 交给 asynq 重试，任务在重试期间继续保持 `running`，避免中间失败被用户误认为终态失败。

但需要确认 asynq 重试耗尽后的最终状态：

| 场景 | 需要确认 |
|---|---|
| Kafka read/commit 持续失败 | 重试耗尽后 task 是否仍是 `running + errorMsg` |
| ctx cancel / pod 重启反复发生 | 是否可能长时间显示 running |
| 模型服务持续不可用 | 单文档失败会进入 `failure_detail`，但整任务级失败是否会落 failed |
| resource/index 更新失败 | `updateResourceIndexName` 失败会返回 error，重试耗尽后状态如何 |

如果没有 asynq 失败回调或外部 reconciler 把最终失败落库，用户可能看到：

```text
status = running
error_msg = last execution error
asynq 已不再重试
```

这会和"构建中" UI 语义冲突。

### 建议确认

- asynq 最大重试耗尽时是否有全局 error handler 更新 task `status = failed`
- 如果没有，embedding worker 是否应在某类不可恢复错误上直接落 `failed`
- `running + errorMsg` 是否是产品允许的中间状态，UI 是否会提示"正在重试"
- `failure_detail` 和 `error_msg` 的边界是否清晰：
  - `failure_detail`：completed 但部分文档向量缺失
  - `error_msg`：整任务未完成的硬失败

### 待确认 3：自动重试与 full rebuild/drop index 语义存在耦合

worker 稳定性增强后，失败任务更容易被自动重试、自愈或手动重新 start。
这对可恢复性是好事，但如果当前执行类型是 full rebuild，仍会触发同名 index 的 drop/recreate。

需要确认这些链路是否会反复破坏当前生效索引：

- `PUT /build-tasks/:id` 更新配置后触发 full rebuild
- full rebuild 会 drop `BuildIndexName(resource.ID, task.ID)`
- 如果这个 index 正是 `resource.LocalIndexName`，查询会受影响
- worker / asynq 重试可能再次执行 drop/recreate 流程

这与 R2/R3 的语义问题相关：如果配置变更改为创建新 task / 新 index version，那么自动重试只会影响新索引，不会破坏旧的当前生效索引。

### 建议确认

- full rebuild 的 asynq 重试是否可能重复 drop 当前生效 index
- full rebuild 失败后再次 start 的默认 execute type 是 incremental 还是 full
- 对当前生效 task 进行 full rebuild 时，是否应该显式进入 rebuilding 状态并阻断查询
- 是否应先解决 R2 的"配置变更创建新 task"语义，再继续强化自动重试
