# PR-A 执行计划：Resource.LastDiscoverStatus

## 背景

catalog 扫描（discover）后，`Resource` 上没有任何字段表达"本次扫描相对上次的差异"。
现有 `Status` 只表达生命周期（active/stale/disabled/deprecated），无法回答
"本次新增了哪些 / 变更了哪些 / 失踪了哪些"。

本 PR 引入单字段 `LastDiscoverStatus`，作为"最近一次扫描观察"的标签。
历史明细表（`t_discover_task_item`）走 PR-B，本 PR 不涉及。

## 目标

为 `Resource` 增加单字段 `LastDiscoverStatus`，让前端能区分扫描后资源的差异类型：
`new / unchanged / updated / restored / missing`。

## 五枚举语义

| 值 | 含义 | 写入时机 | 重写策略 |
|---|---|---|---|
| `new` | 本次扫描首次创建 | createResource 成功后 | 一次性事件，下次扫描自动让位给 unchanged/updated |
| `unchanged` | 命中且 schema/metadata 未变 | enrich 末尾哈希相等 | 每次扫描重写 |
| `updated` | 命中且 schema/metadata 有变 | enrich 末尾哈希不等 | 每次扫描重写 |
| `restored` | 原状态 stale，本次源端重新出现 | reactivate 分支 | 一次性事件，下次扫描自动让位给 unchanged/updated |
| `missing` | 源端缺席 | stale 分支（**守卫之外**）| 每次扫描重写，即便已是 stale |

## 状态对照表

| 库内 → 源端 | 源端存在 | 源端缺席 |
|---|---|---|
| 库内不存在 | `new` | —（不可能）|
| 库内 active | `unchanged` 或 `updated` | `missing`（同时 Status→stale）|
| 库内 stale | `restored`（同时 Status→active）| `missing`（Status 保持 stale）|
| 库内 disabled | `unchanged`（不动 Status）| `missing`（不动 Status）|

## 关键设计原则

- **Status 是状态机**：受幂等守卫保护，仅在生命周期翻转时写入。
- **LastDiscoverStatus 是"最近一次观察"**：每次扫描都覆盖，反映本次结果。
- **`markDiscover` 必须放在状态机守卫之外**——否则已 stale 的资源在持续缺席的扫描中会失去 missing 标记。
- **哈希语义**：`SchemaDefinition + SourceMetadata` 稳定 JSON 序列化后 sha1（或 fnv）。
  **不纳入** `Description / Tags / DisplayName / Database` 等用户可编辑字段——
  这些不是源端属性，纳入会污染 "源端变了" 的语义。

## 验收清单

- [ ] DB 多一列 `f_last_discover_status VARCHAR(32) NOT NULL DEFAULT ''`，历史行为空串
- [ ] `Resource` 结构体多一字段，五个枚举常量定义在 `interfaces`
- [ ] `ResourceService` / `ResourceAccess` 接口 + mock 新增 `UpdateDiscoverStatus(ctx, id, status)`
- [ ] `discover_handler.go` 三处 reconcile（table/index/fileset）+ 三处 enrich 全部接入 `markDiscover`
- [ ] `markDiscover` 写在状态机守卫**之外**，确保 `missing/unchanged/updated` 每次扫描重写
- [ ] `updated` 通过 enrich 前后哈希对比判定
- [ ] 单测覆盖下述全部边缘案例并通过
- [ ] `go vet ./... && go test ./...` 全绿

## 失败条件（任一触发即返工）

- 已 stale 资源在第二次扫描后 `LastDiscoverStatus` 未被刷新为 `missing`
- 同资源连扫两次，第二次仍是 `new`（应翻为 `unchanged`/`updated`）
- 用户已 `disable` 的资源被扫描后 `Status` 被改动
- 哈希纳入了 `Description / Tags / DisplayName` 等用户字段
- 任一现有 discover 测试因本 PR 而红
- migration 在已有数据的 t_resource 上跑失败

## 改动清单（6 文件，1 个原子 PR）

| # | 文件 | 改动 |
|---|---|---|
| 1 | `interfaces/resource.go` | + `LastDiscoverStatus` 字段 + 5 个 `DiscoverStatus*` 常量 |
| 2 | `interfaces/resource_service.go` + `interfaces/resource_access.go` | + `UpdateDiscoverStatus` 方法签名 |
| 3 | `interfaces/mock/mock_resource_service.go` + `mock_resource_access.go` | mockgen 重生成 |
| 4 | `drivenadapters/resource/resource_access.go` | DAO Select/Scan/Update 新列 |
| 5 | `logics/resource/resource_service.go` | service 方法 + 单测 |
| 6 | `worker/discover_handler.go` | + `markDiscover` helper + `schemaHash` helper + 6 处调用点；附设计注释 |
| 7 | `migrations/mariadb/NNNN_add_resource_last_discover_status.sql` | `ALTER TABLE t_resource ADD COLUMN ...` |

第 3、7 算附属：mock 是生成产物、migration 是单行 DDL。
核心 review 集中在 1/2/4/5/6。

## 实施顺序（每步可独立编译通过）

1. **Step 1 — 数据模型骨架**
   - 改 #1 #2 #3 #7
   - 跑 `go build` 确认接口贯通
2. **Step 2 — 存储层落地**
   - 改 #4：Select 增加列读取、`UpdateDiscoverStatus` 实现
   - 加 DAO 单测
3. **Step 3 — Service 透传**
   - 改 #5：service 方法 + 单测
4. **Step 4 — 写入接入**
   - 改 #6：
     - 抽 `markDiscover(ctx, resourceID, status string)`
     - 抽 `schemaHash(*Resource) string`（稳定 JSON）
     - 3×reconcile 接入 `new / restored / missing`
     - 3×enrich 末尾算哈希接入 `unchanged / updated`
     - **关键**：`missing` 写在 `if existing.Status != stale` 守卫**之外**
5. **Step 5 — 测试覆盖**
   - 补 `worker/discover_handler_test.go`，覆盖下述用例
6. **Step 6 — 静态检查 + 全量测试**
   - `go vet ./...` + `go test ./...`

## 测试用例

| # | 场景 | 期望 |
|---|---|---|
| T1 | 新资源首次扫描 | `Status=active`, `LastDiscoverStatus=new` |
| T2 | 同资源连扫两次，源端无变 | 第二次：`LastDiscoverStatus=unchanged` |
| T3 | 同资源连扫两次，源端 schema 变 | 第二次：`LastDiscoverStatus=updated` |
| T4 | 老资源首次被扫到（`LastDiscoverStatus=""`）| 写 `unchanged`（不是 new） |
| T5 | 用户 disable 的资源源端仍在 | `Status` 保持 `disabled`，`LastDiscoverStatus=unchanged` |
| T6 | 源端首次缺席 | `Status: active→stale`, `LastDiscoverStatus=missing` |
| T7 | 源端持续缺席 | `Status` 保持 `stale`（守卫跳过），`LastDiscoverStatus` 每次仍刷为 `missing` |
| T8 | 源端删除后又出现 | `Status: stale→active`, `LastDiscoverStatus=restored` |
| T9 | 任务策略排除 `delete` | 源端没了的资源**不写** `missing`，原值保持 |
| T10 | 任务策略排除 `update` | 命中的资源**不写** `unchanged/updated`，原值保持 |
| T11 | enrich 失败 | 资源已写入的 `LastDiscoverStatus` 不回滚 |
| T12 | 用户编辑了 `Description` | 哈希不包含 description，`LastDiscoverStatus=unchanged` |
| T13 | 哈希稳定性 | 同一 schema 反复序列化哈希恒等 |

## 代码注释（写入 discover_handler.go 顶部）

```go
// LastDiscoverStatus 写入策略：
//   - Status 是状态机：受幂等守卫保护，仅在生命周期翻转时写入。
//   - LastDiscoverStatus 是"最近一次观察"：每次扫描都覆盖，反映本次结果。
//   - 因此 markDiscover 必须放在状态机守卫之外，避免已 stale 的资源
//     在持续缺席的扫描中失去 missing 标记。
//   - new/restored 是一次性事件标签，下次扫描自动让位给 unchanged/updated；
//     unchanged/updated/missing 是持续观察，每次重写。
//   - 未来若再加同类"观察"字段，应考虑下沉到独立事件表（PR-B 方向），
//     而非继续往 Resource 实体塞。
```

## PR 描述需说明的事

- 五枚举语义对照表
- 已知未解决问题：fileset `SourceIdentifier` fallback 切换可能误判 new+missing 对（既有问题，本 PR 不解决）
- 已知妥协：观察日志塞实体是便利妥协，PR-B 明细表是干净方案
- 不含 API/前端改动；前端消费在后续 PR

## 风险与依赖

- **零外部依赖**：纯 backend，无 frontend 改动、无新增 endpoint
- **migration 风险低**：只加列、有默认值、无回填
- **回滚策略**：rollback migration `DROP COLUMN f_last_discover_status` + revert 代码

## 工作量估算

- 实施：约 200~300 行新增 + 30~50 行修改
- 测试：约 300~400 行
- 评审重点：#6 的写入位置和哈希语义

## 不在本 PR 范围

- `t_discover_task_item` 明细表（PR-B）
- 前端消费 `LastDiscoverStatus` 的 UI 改动
- fileset `SourceIdentifier` fallback 切换的误判问题
- enrich 覆写式更新导致用户编辑字段丢失的问题
