# vega-backend drivenadapters UT 补齐计划

> 日期：2026-07-09  
> 范围：`adp/vega/vega-backend/server/drivenadapters`  
> 目标：聚焦 driven adapter 层，用 Go 原生 `testing` + `testify` 补齐 UT，并统一既有 UT 风格。

## 1. 本轮范围

本轮只处理 `server/drivenadapters`，不继续扩大到 `logics`、`driveradapters`、`worker`、`common`、`errors` 等目录。

当前 drivenadapters 文件清单：

| 文件 | 说明 |
| --- | --- |
| `asynq/asynq_access.go` | Asynq/Redis client option 构造 |
| `auth/hydra_auth_access.go` | Hydra token 校验 |
| `kafka/kafka_access.go` | Kafka reader/writer/admin 配置与操作 |
| `permission/permission_access.go` | 权限访问入口 |
| `permission/shadow.go` | BKN Safe safe/shadow 权限客户端 |
| `user_mgmt/user_mgmt_access.go` | 用户账号查询 |
| `model_factory/model_factory_access.go` | 模型工厂 HTTP adapter |
| `connector_type/connector_type_access.go` | connector type SQL access |
| `build_task/build_task_access.go` | build task SQL access |
| `discover_task/discover_task_access.go` | discover task SQL access |
| `discover_schedule/discover_schedule_access.go` | discover schedule SQL access |
| `catalog/catalog_access.go` | catalog SQL access |
| `catalog/catalog_extension.go` | catalog extension join/attach helper |
| `resource/resource_access.go` | resource SQL access |
| `resource/resource_extension.go` | resource extension join/attach helper |
| `entityextension/store.go` | extension store SQL access/helper |

## 2. 测试组织规则

- 一个生产函数对应一个顶层测试函数。
- 顶层测试函数内部所有场景都用 `t.Run` 包裹。
- 方法测试命名优先使用 `Test<Type><Method>`，例如 `TestAsynqAccessCreateClient`、`TestPermissionAccessCheckPermission`。
- 包级函数/helper 测试命名使用 `Test<Function>`，例如 `TestGetRedisClientOpt`、`TestCatalogExtCol`。
- 如果旧 UT 已经为同一个生产函数拆出多个顶层测试函数，本轮触达时一起合并或调整。
- 断言使用 `require` 做前置条件和错误 gating，使用 `assert` 做结果校验。
- SQL access 使用 `sqlmock`；HTTP adapter 使用 fake client 或 `httptest`。
- 不连接真实 Redis、Kafka、DB、BKN Safe、Hydra。
- 缺少注入点、必须依赖真实中间件或容易阻塞的函数进入 defer/IT 清单，不为覆盖率写脆弱 UT。

示例：

```go
func TestAsynqAccessCreateClient(t *testing.T) {
	t.Run("creates client with standalone redis", func(t *testing.T) {
		// arrange / act / assert
	})

	t.Run("creates client with redis cluster", func(t *testing.T) {
		// arrange / act / assert
	})
}
```

## 3. 当前覆盖快照

最近一次专项命令：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./drivenadapters/... -cover
```

结果：

| 包 | 覆盖率 |
| --- | ---: |
| `drivenadapters/auth` | 100.0% |
| `drivenadapters/kafka` | 45.7% |
| `drivenadapters/asynq` | 93.8% |
| `drivenadapters/catalog` | 45.3% |
| `drivenadapters/permission` | 90.3% |
| `drivenadapters/resource` | 53.7% |
| `drivenadapters/discover_schedule` | 70.4% |
| `drivenadapters/discover_task` | 72.0% |
| `drivenadapters/connector_type` | 75.9% |
| `drivenadapters/build_task` | 78.1% |
| `drivenadapters/entityextension` | 82.4% |
| `drivenadapters/model_factory` | 100.0% |
| `drivenadapters/user_mgmt` | 96.1% |

## 4. 文件批次计划

### D1：外部系统 Adapter 文件

文件：

- `drivenadapters/asynq/asynq_access.go`
- `drivenadapters/kafka/kafka_access.go`
- `drivenadapters/auth/hydra_auth_access.go`
- `drivenadapters/permission/permission_access.go`
- `drivenadapters/permission/shadow.go`
- `drivenadapters/user_mgmt/user_mgmt_access.go`
- `drivenadapters/model_factory/model_factory_access.go`

目标：

- 按“一函数一测试函数”整理已有测试。
- 覆盖配置转换、client option 构造、外部请求错误、非法响应。
- `asynq` 覆盖 standalone、cluster、sentinel、master-slave、未知 Redis 模式。
- `kafka` 覆盖 broker 地址、SASL mechanism/dialer、reader/writer 构造、close/write/create topic 错误路径。
- `auth` 覆盖 token 校验成功、非 2xx、请求失败、响应解析失败。
- `permission_access.go` 覆盖权限检查、资源创建/删除、资源过滤的成功和错误透传。
- `shadow.go` 覆盖 safe client、shadow permission、fallback 行为、shadow 不影响主结果。
- `user_mgmt_access.go` 和 `model_factory_access.go` 已有覆盖较高，重点做风格统一和遗漏分支补齐。
- `kafka.ReadMessage`、`kafka.CommitMessages` 如缺少 fake 注入点，先进入 defer/IT 清单。

建议 commit：`test(vega-backend): cover driven external adapters`。

状态：

- 已完成：`asynq/asynq_access.go`、`auth/hydra_auth_access.go`、`kafka/kafka_access.go`、`permission/permission_access.go`、`permission/shadow.go`、`user_mgmt/user_mgmt_access.go`、`model_factory/model_factory_access.go` 的测试风格整理和主要分支补齐。
- 暂缓/IT：`kafka.ReadMessage`、`kafka.CommitMessages` 仍依赖真实 `kafka.Reader` 行为，当前不为覆盖率增加脆弱 UT。

### D2：SQL Access 主体文件

文件：

- `drivenadapters/connector_type/connector_type_access.go`
- `drivenadapters/build_task/build_task_access.go`
- `drivenadapters/discover_task/discover_task_access.go`
- `drivenadapters/discover_schedule/discover_schedule_access.go`
- `drivenadapters/catalog/catalog_access.go`
- `drivenadapters/resource/resource_access.go`

目标：

- 每个文件内按生产函数整理为对应顶层测试函数。
- 使用 `sqlmock` 覆盖 create/get/list/update/delete/status/enable/disable 等 SQL access 行为。
- 覆盖成功、`sql.ErrNoRows`、query error、exec error、scan error、分页/排序/filter 参数。
- 覆盖 catalog/resource 的 auth resource、health/enabled/status、discover status 等主路径。
- 动态 SQL 只断言关键结构和参数顺序，避免测试过脆。

建议 commit：`test(vega-backend): cover driven sql access adapters`。

状态：

- 已完成：`connector_type/connector_type_access.go`、`build_task/build_task_access.go`、`discover_task/discover_task_access.go`、`discover_schedule/discover_schedule_access.go`、`catalog/catalog_access.go`、`resource/resource_access.go` 的现有测试按“一函数一测试函数 + t.Run”整理。
- 已补充：`connector_type` create/update 错误分支，`discover_task` strategy/not found/status/count/delete 分支，`discover_schedule` get/list/create/update 分支。
- 后续留给 D3/收口：`catalog/resource` 的 extension join/attach helper 与更细的动态 SQL 边界。

### D3：Extension、Store 与收口文件

文件：

- `drivenadapters/catalog/catalog_extension.go`
- `drivenadapters/resource/resource_extension.go`
- `drivenadapters/entityextension/store.go`

目标：

- 覆盖 extension join、extension attach、extension sort/filter helper。
- 覆盖 store create、replace、delete、get、batch get、join helper、filter keys。
- 整理 extension/store 旧测试，统一成“一函数一测试函数 + t.Run”。
- 跑 driven 专项覆盖率，输出剩余未覆盖函数。
- 更新 defer/IT 清单，明确哪些函数因为真实中间件、阻塞读或缺少注入点暂不放 UT。

建议 commit：`test(vega-backend): complete driven adapter test sweep`。

## 5. 执行顺序

1. D1：外部系统 adapter，从当前打开的 `asynq` 继续，合并 `kafka`、`auth`、`permission`、`user_mgmt`、`model_factory`。
2. D2：SQL access 主体，覆盖 `connector_type`、`build_task`、`discover_task`、`discover_schedule`、`catalog`、`resource`。
3. D3：extension/store 与收口，覆盖 catalog/resource extension、`entityextension/store.go`，并产出 driven defer/IT 清单。

## 6. 每批检查清单

- 新增测试前先确认是否已有对应 `Test<Function>`。
- 如果已有对应测试函数，只新增 `t.Run` case，不新增同函数的 sibling 顶层测试。
- 如果旧 UT 对同一生产函数拆得过散，本轮触达时顺手合并。
- 开发中优先跑 `go test ./drivenadapters/<pkg> -run Test<Function> -count=1`。
- 批次结束跑 `go test ./drivenadapters/... -cover`。
- 必要时再跑 `go test ./...` 做全仓验证。
- 每批结束更新本文档的覆盖率快照、已完成文件和 defer/IT 清单。
