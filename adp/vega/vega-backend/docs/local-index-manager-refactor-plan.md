# Local Index Manager 拆分方案

> 已确认决策：
> 1. 新服务命名为 `LocalIndexManager`。
> 2. `LocalIndexManager` 定位为本地 index/document 存储能力入口，覆盖 dataset 本地存储和 `vega-build-*` 构建产物。
> 3. `DatasetService` 保留 dataset 对外业务语义，但底层不再直接依赖 `IndexConnector`，而是组合 `LocalIndexManager`。
> 4. 整体作为 **一个 issue** 推进，按 commit 拆分（见第 8 节）。
> 5. 经二次扫描，未发现遗漏的本地索引调用路径。

## 当前实施核对

- `LocalIndexManager` 已作为本地 index/document 存储入口落在 `interfaces` / `logics/local_index`，并直接封装本地 OpenSearch `IndexConnector`。
- `DatasetService` 已组合 `LocalIndexManager`，保留 dataset 对外语义，不再直接创建或持有 OpenSearch `IndexConnector`。
- worker、build task 删除、resource/catalog 级联删除、table 本地索引查询均已迁移到 `LocalIndexManager`。
- `main.go` 与 `driven_access.go` 不初始化 `LocalIndexManager`；各 service / worker 按自身构造边界初始化或在测试中注入 mock。
- 旧 `logics.DS` / `SetDatasetService` 已从本地索引级联路径中移除。
- `DatasetService` 仍保留 dataset 资源文档 API；`driveradapters/resource_data_handler.go` 等 dataset 资源路径不直接迁移调用方，只通过 `DatasetService` 间接复用本地存储能力。

## 1. 现状分析

### 1.1 代码位置

| 层级 | 文件 | 职责 |
|------|------|------|
| 接口层 | `server/interfaces/dataset_service.go` | 定义 `DatasetService` |
| 业务逻辑层 | `server/logics/dataset/dataset_service.go` | `DatasetService` 唯一实现 |
| 驱动层 | `server/logics/connectors/local/index/opensearch/opensearch.go` | `IndexConnector` 实现 |
| 端口层 | `server/logics/connectors/connector.go` | 定义 `IndexConnector` |
| 任务/级联 | `server/logics/build_task/build_task_service.go`<br>`server/logics/cascade.go`<br>`server/logics/resource/resource_service.go`<br>`server/logics/catalog/catalog_service.go` | 通过 `DatasetService` 删除/检查本地索引 |
| Worker | `server/worker/build_task_common.go`<br>`server/worker/build_handler_*.go` | 通过 `DatasetService` 创建/更新本地索引、读写文档 |

### 1.2 当前 `DatasetService` 承担的两类职责

`DatasetService` 目前混合了两层职责：

1. **Dataset 对外业务语义**（与 table/file/index 同级的资源表现）
   - 为 `ResourceCategoryDataset` 创建/更新/删除 OpenSearch 索引
   - 索引名称为 `resource.ID`
   - 对应接口：`Create(ctx, res *Resource)`、`Update(ctx, res *Resource)`、`Delete(ctx, id)`

2. **本地 index/document 存储能力**
   - 为 dataset 资源管理本地 OpenSearch index/document
   - 为 `BuildTask` 创建/删除/检查 OpenSearch 索引
   - 索引名称为 `vega-build-{resourceID}-{buildTaskID}`
   - 为 build task 写入/更新/删除/查询文档
   - 对应接口：`CreateDocuments`、`UpsertDocuments`、`DeleteDocuments`、`ListDocuments`、`GetDocument` 等

### 1.3 当前调用链

```text
Resource(table) ──► BuildTask ──► createLocalIndex() ──► DatasetService.Create()
                                         │
                                         ▼
                              OpenSearch Index: vega-build-{r}-{t}
                                         │
                                         ▼
                              DatasetService.{Upsert/Delete/List}Documents()
                                         │
                                         ▼
                              Resource.LocalIndexName = vega-build-{r}-{t}
```

### 1.4 关键发现

- `datasetService` 是 `IndexConnector` 的唯一业务包装，所有本地 index/document 操作都不得不经过 `DatasetService`。
- `build_task_service.DeleteBuildTasks` 和 `cascade.go` 用 `DatasetService.Delete` 来 drop `vega-build-*` 索引，语义上属于“删任务级联删索引”，但依赖的是 dataset 服务。
- `resource_data_service.go` 在查询 table 数据时，若 `resource.LocalIndexName != ""`，同样调用 `DatasetService.ListDocuments`。
- 这导致 `DatasetService` 成为所有 OpenSearch 索引操作的“万能入口”，把 dataset 对外语义和本地存储能力绑在一起。

> 边界说明：`DatasetService` 仍然负责 `ResourceCategoryDataset` 的文档 API，例如 `server/driveradapters/resource_data_handler.go` 中对 `resource.ID` 的 `CreateDocuments` / `UpsertDocuments` / `DeleteDocuments` 调用。这些调用方不需要直接改成 `LocalIndexManager`；由 `DatasetService` 在内部复用本地存储能力。

---

## 2. 问题

1. **概念层级混乱**：dataset 是本地存储的一种对外表现，`vega-build-*` 是构建任务产物的对外表现；二者底层都是本地 index/document 存储，不应由 `DatasetService` 直接承担底层存储适配。
2. **职责单一原则违反**：`DatasetService` 既承担 dataset 对外语义，又直接封装所有本地 index/document CRUD，未来扩展其他索引类型（如向量库、全文引擎）会进一步膨胀。
3. **测试与替换成本高**：所有 build/task 相关测试都需要 mock `DatasetService`，即使只测索引生命周期。
4. **隐式依赖全局单例**：改造前 `logics.DS` 被同时用于 dataset 资源和本地索引，启动顺序和注入逻辑耦合；cleanup 阶段应移除这个本地索引用途消失后的全局入口。

---

## 3. 目标

- 将本地 index/document 存储能力从 `DatasetService` 中拆分出来。
- `LocalIndexManager` 负责本地 OpenSearch index/document 生命周期，覆盖 dataset 本地存储和 `vega-build-*` 构建产物。
- `DatasetService` 仅保留 `ResourceCategoryDataset` 的对外业务语义，底层组合 `LocalIndexManager`。
- 上层服务（build_task、resource、catalog）和 worker 统一通过 `LocalIndexManager` 操作本地索引产物。

---

## 4. 方案设计

### 4.1 新增接口

在 `server/interfaces/local_index_manager.go` 定义：

```go
// LocalIndexManager 管理本地 search engine 上的 index/document 存储能力。
// BuildTask 本地索引命名规则由 interfaces.BuildIndexName 统一生成：vega-build-{resourceID}-{buildTaskID}
type LocalIndexManager interface {
    // 索引生命周期
    CreateIndex(ctx context.Context, indexName string, schema []*Property) error
    UpdateIndex(ctx context.Context, indexName string, schema []*Property) error
    DeleteIndex(ctx context.Context, indexName string) error
    CheckExist(ctx context.Context, indexName string) (bool, error)

    // 文档操作
    ListDocuments(ctx context.Context, indexName string, res *Resource, params *ResourceDataQueryParams) ([]map[string]any, int64, error)
    GetDocument(ctx context.Context, indexName, docID string) (map[string]any, error)
    CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error)
    UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error)
    DeleteDocument(ctx context.Context, indexName, docID string) error
    DeleteDocuments(ctx context.Context, indexName string, docIDs string) error
    DeleteDocumentsByQuery(ctx context.Context, indexName string, res *Resource, params *ResourceDataQueryParams) error
}
```

### 4.2 新增实现

新增包 `server/logics/local_index/local_index_manager.go`：

```go
type localIndexManager struct {
    c connectors.IndexConnector
}

func NewLocalIndexManager(appSetting *common.AppSetting) interfaces.LocalIndexManager { ... }
```

实现细节：
- 复用现有的 `opensearchConnector.NewOpenSearchConnector()` 作为底层 `IndexConnector`。
- `LocalIndexManager` 直接调用 `IndexConnector`，不再经过 dataset 层。

### 4.3 `DatasetService` 复用 `LocalIndexManager`

`DatasetService` 保留现有接口不变（对外 API 无影响），但底层改为组合 `LocalIndexManager`。这样 dataset API 仍由 `DatasetService` 暴露，实际本地 index/document 操作统一由 `LocalIndexManager` 执行：

```go
type datasetService struct {
    lim interfaces.LocalIndexManager
}

func (ds *datasetService) Create(ctx context.Context, res *Resource) error {
    return ds.lim.CreateIndex(ctx, res.ID, res.SchemaDefinition)
}

func (ds *datasetService) Update(ctx context.Context, res *Resource) error {
    return ds.lim.UpdateIndex(ctx, fmt.Sprintf("%s-%s", res.SourceIdentifier, res.ID), res.SchemaDefinition)
}

func (ds *datasetService) Delete(ctx context.Context, id string) error {
    return ds.lim.DeleteIndex(ctx, id)
}

func (ds *datasetService) CheckExist(ctx context.Context, id string) (bool, error) {
    return ds.lim.CheckExist(ctx, id)
}

// dataset 文档 API 仍使用 dataset 资源 ID；只是复用 LocalIndexManager 的本地存储能力。
func (ds *datasetService) ListDocuments(...) (...) {
    return ds.lim.ListDocuments(...)
}
// ...
```

这样：
- `DatasetService` 仍然是 dataset 资源的业务入口；
- `LocalIndexManager` 是本地 index/document 存储能力入口；
- 调用方不直接把 dataset API 改成 `LocalIndexManager`，但 `DatasetService` 内部复用该能力，避免重复封装 `IndexConnector`。

### 4.4 调用方迁移

| 原调用 | 新调用 |
|--------|--------|
| `build_task_service.DeleteBuildTasks` → `ds.Delete(idx)` | `lim.DeleteIndex(ctx, idx)` |
| `cascade.CascadeDeleteBuildTasks` → `ds.Delete(idx)` | `lim.DeleteIndex(ctx, idx)` |
| `worker.createLocalIndex` → `ds.CheckExist` / `ds.Create` | `lim.CheckExist` / `lim.CreateIndex` |
| `worker.build_handler_*` → `ds.{Upsert/Delete/List/Get}Documents` | `lim.{Upsert/Delete/List/Get}Documents` |
| `resource_data_service.ListDocuments` → `ds.ListDocuments` (table local index) | `lim.ListDocuments` |
| `datasetService` → `IndexConnector` | `datasetService` → `LocalIndexManager` |

不迁移的调用方：

| 调用 | 原因 |
|------|------|
| `driveradapters/resource_data_handler.go` → `ds.{Create/Upsert/Delete/Get}Documents(resource.ID, ...)` | dataset 资源文档 API，继续通过 `DatasetService` 暴露；底层由 `DatasetService` 复用 `LocalIndexManager` |
| `resource_data_service.Query` → `ds.ListDocuments(ctx, resource.ID, ...)` (`ResourceCategoryDataset`) | dataset 资源查询路径，继续保留 `DatasetService` 对外语义 |

### 4.5 注入方式调整

- `buildTaskService` 新增 `lim interfaces.LocalIndexManager` 字段（测试可注入 mock）。
- `catalogService` / `resourceService` 在级联删除时通过结构字段或构造参数使用 `LocalIndexManager`。
- `worker` 中的 handler 将 `ds` 替换为 `lim`。
- Commit 1 不新增全局 `logics.LIM`，也不在 `main.go` 初始化。后续迁移具体调用方时，再按对应 service / worker 的构造边界完成注入。

---

## 5. 依赖关系

### 5.1 改造前（问题）

```text
logics/dataset ──► connectors/local/index/opensearch
logics/build_task ──► logics/dataset (通过 logics.DS)
logics/resource ──► logics/dataset
logics/catalog ──► logics/dataset
server/worker ──► logics/dataset
```

### 5.2 改造后（目标）

```text
logics/dataset ──► logics/local_index ──► connectors/local/index/opensearch
logics/local_index ──► connectors/local/index/opensearch
logics/build_task ──► logics/local_index
logics/resource ──► logics/local_index (级联删索引)
logics/catalog ──► logics/local_index (级联删索引)
server/worker ──► logics/local_index
```

cleanup 后 `logics.DS` / `SetDatasetService` 不再保留；dataset 资源路径由各自 service / handler 的 `DatasetService` 字段负责，`DatasetService` 内部与 build task / worker 共同复用 `LocalIndexManager`。

---

## 6. 风险与注意事项

1. **DatasetService 的 `Update` 行为差异**：当前 `DatasetService.Create` 使用 `res.ID` 建索引，但 `DatasetService.Update` 使用 `fmt.Sprintf("%s-%s", res.SourceIdentifier, res.ID)` 更新 mapping。拆分前必须确认这是历史兼容行为还是 bug；迁移时不能在没有测试保护的情况下改变实际索引名。
2. **LocalIndexManager 的语义边界**：它负责本地 index/document 存储能力，不直接承载 dataset 对外 API，也不负责远程 index 查询。dataset 资源的对外文档 API 继续通过 `DatasetService` 暴露。
3. **避免过度包装**：当前先让 `LocalIndexManager` 直接封装 `IndexConnector`，`DatasetService` 组合它。若未来本地存储后端或生命周期策略变复杂，再考虑继续拆更底层 store。
4. **索引生命周期原则**：根据项目约束，索引删除只能由任务删除流程（`build_task_service.go`、`cascade.go`）负责，`LocalIndexManager` 不应在资源更新时主动删除旧索引。该原则在拆分后仍需保持。
5. **注入边界**：不要在 `driven_access.go` 或 `main.go` 中提前引入全局 `LocalIndexManager`。后续迁移调用方时，应按对应 service / worker 的构造边界显式注入，避免形成新的全局耦合或初始化环。
6. **Worker embedding 直接依赖**：`server/worker/build_handler_embedding.go` 当前除了 `DatasetService` 字段外还有 `connectors.IndexConnector` 字段，拆分时需确认该直接 connector 是否仍有必要；若只用于本地索引读写，应一并收口到 `LocalIndexManager`。
7. **测试影响**：`mock_dataset_service.go` 中大量 mock 调用与本地索引相关，需要同步新增 `mock_local_index_manager.go`。

---

## 7. 遗漏路径确认

经二次全量扫描，确认以下 `IndexConnector` 使用场景**不属于**本地索引管理范围，无需迁移：

| 文件 | 用途 | 结论 |
|------|------|------|
| `server/worker/discover_index.go` | discover 远程 OpenSearch index 资源 | 远程 connector，不涉及 `vega-build-*` |
| `server/logics/resource_data/resource_data_service.go:280` | `ResourceCategoryIndex` 查询远程索引 | 远程 connector，与本地索引无关 |
| `server/logics/resource_data/logic_view/logic_view.go:430` | logic view 查询远程索引 | 远程 connector，与本地索引无关 |
| `server/logics/query/raw_query_service.go:652` | raw query 查询远程索引 | 远程 connector，与本地索引无关 |

所有**本地索引**相关调用均已覆盖在第 1 节表格中。

---

## 8. 实施计划：一个 Issue，按 Commit 拆分

### Issue 标题

`refactor(vega): extract LocalIndexManager from DatasetService`

### Commit 拆分（按依赖顺序）

#### Commit 1: `feat(local_index): define LocalIndexManager`

- 新增 `server/interfaces/local_index_manager.go`
- 新增 `server/logics/local_index/local_index_manager.go`
- `LocalIndexManager` 直接封装 OpenSearch `IndexConnector`，提供本地索引语义入口
- 新增 `server/interfaces/mock/mock_local_index_manager.go`
- 不在 `server/main.go` 初始化 `LocalIndexManager`；具体注入点随后续调用方迁移确定
- **不修改任何调用方**

#### Commit 2: `refactor(worker): use LocalIndexManager for build task indexes`

- `server/worker/build_task_common.go`：`createLocalIndex` 使用 `LocalIndexManager`
- `server/worker/build_handler_batch.go` / `build_handler_streaming.go` / `build_handler_embedding.go`：
  - handler 的 `ds` 字段改为 `lim interfaces.LocalIndexManager`
  - 本地索引创建、full rebuild 检查/删除、文档 get/upsert/delete 改为 `LocalIndexManager`
  - 删除 `build_handler_embedding.go` 中未使用的直接 `connectors.IndexConnector` 字段
- 同步更新 worker 相关测试

#### Commit 3: `refactor(build_task): use LocalIndexManager for task deletion and cascade`

- `server/logics/build_task/build_task_service.go`：`DeleteBuildTasks` 使用 `LocalIndexManager.DeleteIndex`
- `server/logics/cascade.go`：`CascadeDeleteBuildTasks` 签名改为接收 `interfaces.LocalIndexManager`
- `server/logics/resource/resource_service.go` / `catalog/catalog_service.go`：级联删除时注入/使用 `LocalIndexManager`
- 同步更新相关测试

#### Commit 4: `refactor(resource_data): use LocalIndexManager for table local index queries`

- `server/logics/resource_data/resource_data_service.go`：table 本地索引查询改为 `LocalIndexManager.ListDocuments`
- 同步更新测试

#### Commit 5: `chore(cleanup): remove obsolete DatasetService index references`

- `server/logics/dataset/dataset_service.go`：改为组合 `LocalIndexManager`，移除直接 OpenSearch connector 初始化
- 不清理 `server/driveradapters/router.go`、`server/driveradapters/driveradapters_test.go` 中 dataset 资源文档 API 所需的 `DatasetService` 字段；dataset 对外 API 保持不变
- 修正 `server/logics/resource_data/resource_data_service.go` 错误的包注释 `// Package dataset provides Dataset management business logic.`
- 移除不再使用的 `logics.DS` / `SetDatasetService` 以及 `main.go` 中旧的 DatasetService cascade 注入
- 更新文档与架构说明

---

## 9. 后续讨论点（已收敛的不再列出）

1. `LocalIndexManager` 是否需要支持多后端（如未来接入 Elasticsearch）？当前基于 `IndexConnector` 端口，天然可切换。
2. `DatasetService` 后续是否应进一步拆分为“dataset 业务编排”+“dataset 数据访问”两层？本次 refactor 不涉及，保持最小改动。
