# vega-backend/server UT 现状 Review 与补齐计划

> 日期：2026-07-09  
> 范围：`adp/vega/vega-backend/server`  
> 目标：用 Go 原生 `testing` + `testify` 风格补齐单元测试，优先覆盖纯逻辑、参数校验、状态流转、SQL/HTTP adapter 边界，不引入外部服务依赖。

## 1. 当前结论

`server` 目录已有一定 UT 基础，但覆盖集中在 `driveradapters`、`worker`、少量 `logics` 包；大量核心数据访问、配置、错误码、逻辑视图 DSL/SQL、discover schedule/task 仍为 0% 覆盖。

本次扫描命令：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：`go test ./...` 通过，整体 statement coverage 为 **7.3%**。执行时出现 `/etc/profile.d/ulimit.sh` 的 ulimit warning，但不影响测试通过。

## 2. 现有 UT 分布

静态统计排除 `interfaces/mock` 生成代码后：

| 指标 | 数量 |
| --- | ---: |
| Go 源码文件 | 213 |
| Go 测试文件 | 42 |
| 使用 goconvey 的测试文件 | 14 |
| 使用 testify 的测试文件 | 0 |
| 使用 gomock 的测试文件 | 22 |
| 使用 sqlmock 的测试文件 | 1 |

按顶层目录粗略分布：

| 目录 | 源码文件 | 测试文件 | 观察 |
| --- | ---: | ---: | --- |
| `driveradapters` | 18 | 13 | 参数校验和部分 handler 已有较多测试 |
| `worker` | 12 | 7 | build handler/reconciler 有基础，discover/schedule worker 薄 |
| `logics` | 103 | 19 | catalog/resource/build_task/rate 有基础，很多子包为 0% |
| `drivenadapters` | 16 | 2 | 只有 build_task order 与 model_factory 覆盖较好 |
| `common` | 5 | 0 | 配置、工具函数、visitor 未覆盖 |
| `errors` | 11 | 0 | 错误码和扩展错误未覆盖 |
| `interfaces` | 45 | 1 | 多数是接口/模型和 mock，低优先级 |

## 3. 包级覆盖现状

已覆盖相对较好的包：

| 包 | 覆盖率 | 备注 |
| --- | ---: | --- |
| `drivenadapters/model_factory` | 86.4% | `httptest` 覆盖外部 HTTP 成功/失败路径 |
| `logics/rate` | 65.5% | 并发限流核心逻辑已覆盖 |
| `logics/build_task` | 56.0% | 服务与 normalize 有基础 |
| `logics/catalog` | 47.2% | service 层已有主要路径 |
| `driveradapters` | 31.5% | list/filter 参数校验较多，动作 handler 仍不足 |
| `logics/resource` | 32.3% | service、status、logic view SQL 有基础 |
| `worker` | 23.4% | build 相关较多，discover/schedule 仍缺 |

0% 或接近 0% 的重点包：

| 包/区域 | 当前风险 |
| --- | --- |
| `drivenadapters/catalog`、`resource`、`connector_type`、`discover_task`、`discover_schedule` | SQL 构造、扫描、事务、分页/排序/过滤逻辑几乎无 UT，回归风险高 |
| `drivenadapters/permission`、`auth`、`user_mgmt`、`kafka`、`asynq` | 外部系统 adapter 缺少 HTTP/client 配置、错误处理、降级路径测试 |
| `logics/discover_task`、`discover_schedule`、`connector_type`、`dataset`、`auth`、`user_mgmt` | 服务编排和业务规则缺少 mock 驱动测试 |
| `logics/resource_data/logic_view/{dsl,sql,parsing}` | 查询转换链路缺少表达式组合、错误输入和边界条件测试 |
| `logics/connectors/local/fileset/anyshare`、`remote`、`factory`、`oracle` | connector 注册、发现、查询/条件转换缺少覆盖 |
| `common`、`errors`、`locale` | 小函数多，适合低成本补齐基础行为 |
| `worker/discover_*`、`schedule_worker`、`task_worker_manager` | 异步任务状态流转和调度边界缺少覆盖 |

## 4. 测试风格约定

后续新增 UT 统一采用 `testing` + `testify`：

```go
func TestFoo(t *testing.T) {
	t.Run("returns value when input is valid", func(t *testing.T) {
		got, err := Foo("bar")

		require.NoError(t, err)
		assert.Equal(t, "bar", got)
	})
}
```

约定：

- 断言使用 `github.com/stretchr/testify/assert` 和 `github.com/stretchr/testify/require`。
- 对依赖接口使用现有 `interfaces/mock` 里的 gomock mock；新增接口后同步 `go generate ./...`。
- DB access 层优先用 `sqlmock`，只验证 SQL 关键结构、参数、扫描和错误分支，不连真实 DB。
- HTTP 外部 adapter 用 `httptest.Server`，覆盖状态码、超时/请求错误、JSON 解析失败。
- 不新增真实 Redis/Kafka/OpenSearch/MariaDB 等外部依赖；需要真实中间件的放到 IT，不放 UT。
- `server` 范围内已有 goconvey 测试已迁移完成；后续新增和大改文件统一使用 `testing + testify`。

注意：仓库根 `rules/TESTING.md` 旧规范仍写 Go assertions 使用 goconvey；本计划按本次需求改为新增 UT 使用 testify。后续如要统一全仓库规范，应单独更新测试规范文档。

## 5. 大批次补齐计划

后续不再按单个小文件拆 Step，改为按模块域组织 review/commit。每个批次可以包含多个相关包，批内仍保持不改生产逻辑优先；如发现真实 bug，再单独拆修复 commit。

### Batch 1：Connectors 剩余覆盖

目标：把 connector 注册、基础 proxy、纯转换和本地 connector 边界放在一个批次完成。

范围：

- 已纳入本批次：`logics/connectors/factory`、`logics/connectors/remote`。
- `logics/connectors/local/fileset/anyshare`：配置校验、元数据、条件/查询构造、错误输入。
- `logics/connectors/local/index/opensearch`：type mapping、query/dsl/raw query 边界、已有 fulltext/groupby 测试风格统一。
- `logics/connectors/local/table/{postgresql,mariadb,oracle}`：type mapping 已补；后续补 condition/discover/query 中不依赖真实 DB 的分支。

建议验收：

- `go test ./logics/connectors/...` 通过。
- `factory`、`remote`、`anyshare` 从 0% 拉起；table/index connector 覆盖继续提升。
- 可作为一个 commit：`test(vega-backend): add connector unit coverage`。

### Batch 2：Logics Service 业务规则

目标：覆盖核心 use case 编排，不碰真实外部服务。

范围：

- `logics/connector_type`：create/update/list/delete、enabled 状态、重复/不存在错误。
- `logics/discover_task`：创建任务、状态更新、进度/结果更新、已存在任务校验。
- `logics/discover_schedule`：enable/disable、cron next run、调度参数校验。
- `logics/dataset`：dataset schema、写入/查询委托、错误透传。
- `logics/auth`、`user_mgmt`：noop 与外部 adapter 选择、错误降级。

建议验收：

- 每个 service 的成功路径、依赖错误、业务拒绝路径至少各 1 组 case。
- service 层通过 gomock/fake 验证关键调用参数。
- 可按复杂度拆 1-2 个 commit；优先一个 commit，过大再拆。

### Batch 3：Logic View / Query 转换链路

目标：覆盖查询 DSL/SQL 转换和表达式边界。

范围：

- `logics/resource_data/logic_view/{dsl,sql}`：条件组合、非法表达式、空条件、字段映射。
- `logics/resource_data/logic_view/sql/parsing`：解析器输入边界、错误 SQL、别名/函数/字段提取。
- `logics/query`、`filter_condition` 现有覆盖补充与 testify 风格收敛。

建议验收：

- 重点覆盖稳定输入输出，避免绑定 parser 生成代码内部细节。
- `go test ./logics/resource_data/... ./logics/query ./logics/filter_condition` 通过。
- 可作为一个 commit：`test(vega-backend): cover logic view query conversion`。

### Batch 4：Driven Adapters 数据访问边界

目标：覆盖 SQL access 层最容易回归的 query 构造、扫描和错误分支。

范围：

- `drivenadapters/catalog`：Create/Get/List/Update/Delete、extension join、enabled/health 状态更新。
- `drivenadapters/resource`：Create/Get/List/Update/Delete、category/status 过滤、auth resource list、discover status。
- `drivenadapters/connector_type`：扫描、list filter、enabled 更新。
- `drivenadapters/discover_task`、`discover_schedule`：分页、排序、状态更新、cron next run。
- `drivenadapters/entityextension`：Replace/Get/Delete/ApplyJoins/FilterKeys。

建议验收：

- access 层使用 `sqlmock`，覆盖 `sql.ErrNoRows`、扫描错误、exec/query 失败。
- 对动态 SQL 只断言关键片段和参数顺序，避免测试过脆。
- 预计拆 2 个 commit：小包先行，`catalog/resource` 单独一组。

### Batch 5：外部 Adapter 与 Worker

目标：覆盖异步和外部系统边界，不引入真实服务。

范围：

- `drivenadapters/permission`：BKN Safe HTTP 成功/拒绝/错误、shadow mode、filter resources。
- `drivenadapters/auth`、`user_mgmt`：token 校验、账号查询、外部错误处理。
- `drivenadapters/kafka`、`asynq`：配置转换、client option 生成、空配置和非法配置。
- `worker/discover_*`：table/fileset/index 发现、reconcile、enrich 状态。
- `worker/schedule_worker`：start/stop/reload、schedule/unschedule/update、执行失败回写。
- `worker/task_worker_manager`：任务路由、handler 错误、panic 防护。

建议验收：

- HTTP 外部 adapter 用 `httptest.Server`；worker 使用 fake access/service + gomock，不依赖真实 queue。
- 状态流转覆盖成功、部分失败、全失败、重复执行。
- 预计 1 个 commit；如 worker 体量过大，可单独拆出。

## 6. 推荐执行顺序

1. 完成 Batch 1，把当前未提交的 `factory/remote` 与 connectors 剩余 UT 合并为一个 connector commit。
2. 做 Batch 2 service 层；如果 mock/generate 变更过多，按 service 复杂度拆成两个 commit。
3. 做 Batch 3 logic view/query 转换链路。
4. 做 Batch 4 driven adapters；先小 access 包，再 `catalog/resource` 大包。
5. 做 Batch 5 外部 adapter 与 worker，最后统一跑覆盖率和剩余 0% 包清单。

## 7. 每轮补测检查清单

- `go test ./...` 必须通过。
- 新增测试不依赖外部服务、环境变量或固定机器配置。
- 新增和大改测试文件统一使用 `testing` + `testify`。
- 对错误分支使用 `require.Error` + `assert.ErrorContains` 或具体错误码断言。
- 对 mock 调用只校验业务关键参数，避免把实现细节锁死。
- 每轮结束更新本文档的覆盖率快照和已完成范围。

## 8. 执行记录

### 2026-07-09：Step 1 基础纯逻辑样板

范围：

- 新增 `common/utils_test.go`，覆盖 `GiBToBytes`、`GetQueryOrDefault`、`EscapeLikePattern`。
- 新增 `common/visitor/visitor_test.go`，覆盖 visitor 从 Gin request/header 生成的基础行为。
- 新增 `errors/error_code_test.go`，覆盖错误码列表非空、无重复，以及 extensions 错误码注册列表完整性。
- 将 `github.com/stretchr/testify` 提为 `go.mod` 显式依赖，后续新增 UT 统一使用 `testing + testify`。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./common ./common/visitor ./errors
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./...` 通过。
- overall statement coverage：**7.4%**。
- 包覆盖率变化：`common/visitor` 到 100.0%，`errors` 到 100.0%，`common` 到 6.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 2 Catalog / Connector Type Validator

范围：

- 新增 `driveradapters/validate_catalog_test.go`，覆盖 catalog request 的 ID、name、tags、description、connector config duplicate、extensions，以及 list query 的 type、health status、extension filter pair 校验。
- 新增 `driveradapters/validate_connector_type_test.go`，覆盖 connector type request/list query 的 mode、category、remote endpoint，以及 optional mode/category helper。
- 本步骤新增测试全部使用 `testing + testify`。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率变化：`driveradapters` 从 31.5% 到 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 3 Discover Validator 风格迁移

范围：

- 将 `driveradapters/validate_discover_task_test.go` 从 goconvey 迁移到 `testing + testify`。
- 将 `driveradapters/validate_discover_schedule_test.go` 从 goconvey 迁移到 `testing + testify`。
- 仅调整测试组织和断言风格，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 4 Build Task Validator 风格迁移

范围：

- 将 `driveradapters/validate_build_task_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：`parseBuildTaskStatuses`、`isValidBuildTaskOrderBy`、`parseBuildTaskListParams`。
- 仅调整测试组织和断言风格，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 5 基础 Validator 风格迁移

范围：

- 将 `driveradapters/validate_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：name/id/tag/description/pagination 校验、catalog/resource request ID 校验、dataset request 与字段级校验、create resource category 校验。
- 仅调整测试组织和断言风格，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 6 Resource Handler 风格迁移

范围：

- 将 `driveradapters/resource_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：`ListResources` 的 invalid category、invalid status、成功解析 name/category/status 并调用 service。
- 仅调整测试组织和断言风格，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 7 小型 Handler 风格迁移

范围：

- 将 `driveradapters/auth_resource_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 将 `driveradapters/discover_task_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 将 `driveradapters/discover_schedule_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：列表参数校验、状态码断言、service mock 参数透传断言。
- 仅调整测试组织和断言风格，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 8 Build Task / Connector Type Handler 风格迁移

范围：

- 将 `driveradapters/build_task_create_validate_test.go` 从 goconvey 迁移到 `testing + testify`。
- 将 `driveradapters/build_task_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 将 `driveradapters/connector_type_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：build task 列表/删除、embedding field type 校验、connector type 更新/列表参数校验。
- 仅调整测试组织和断言风格，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 9 Catalog Handler 风格迁移

范围：

- 将 `driveradapters/catalog_handler_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：catalog 列表参数校验、enabled/disabled 动作、update enabled 拒绝、database 配置更新、discover 禁用/逻辑 catalog 拦截。
- 仅调整测试组织和断言风格，不改生产逻辑。
- 至此 `driveradapters` 包内 `_test.go` 已无 goconvey / Convey / So 残留。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
rg -l 'smartystreets/goconvey|Convey\(|So\(' driveradapters --glob '*_test.go'
```

结果：

- `go test ./driveradapters` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- 包覆盖率保持：`driveradapters` 32.5%。
- `driveradapters` goconvey 扫描无残留。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 10 Driven Adapters goconvey 清理

范围：

- 将 `drivenadapters/build_task/build_task_order_test.go` 从 goconvey 迁移到 `testing + testify`。
- 将 `drivenadapters/model_factory/model_factory_access_test.go` 从 goconvey 迁移到 `testing + testify`。
- 保留原有覆盖语义：build task order clause/status bucket、model factory model lookup/vector API 成功和失败路径。
- 至此 `adp/vega/vega-backend/server` 范围内 `_test.go` 已无 goconvey / Convey / So 残留。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./drivenadapters/build_task ./drivenadapters/model_factory
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
rg -l 'smartystreets/goconvey|Convey\(|So\(' adp/vega/vega-backend/server --glob '*_test.go'
```

结果：

- `go test ./drivenadapters/build_task ./drivenadapters/model_factory` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.5%**。
- server goconvey 扫描无残留。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 11 Table Connector Type Mapping / Oracle 基础覆盖

范围：

- 新增 `logics/connectors/local/table/oracle/oracle_test.go`，覆盖 Oracle connector 元数据、enabled setter/getter、敏感字段、字段配置、`New` 成功/配置不完整/非法端口/schema 过长、`MapType` 基础映射。
- 新增 `logics/connectors/local/table/mariadb/type_mapping_test.go`，覆盖 MariaDB type mapping、unsigned、长度后缀裁剪、大小写/空白归一、unknown/empty 类型。
- 将 `logics/connectors/local/table/postgresql/type_mapping_test.go` 迁移到 `testing + testify` 断言风格，保留原有语义。
- 不涉及真实数据库连接，不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./logics/connectors/local/table/...
env GOCACHE=/tmp/go-build-cache go test ./logics/connectors/local/table/... -cover
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./logics/connectors/local/table/...` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.6%**。
- 包覆盖率：`mariadb` 17.9%，`oracle` 8.6%，`postgresql` 9.3%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Step 12 Connector Factory / Remote 基础覆盖

范围：

- 新增 `logics/connectors/factory/factory_test.go`，使用 fake connector 覆盖本地 connector 初始化、已有本地 connector 注册启停、remote connector 注册、未实现本地 connector 拒绝、remote 删除、本地删除拒绝、missing 删除/启停/创建错误、disabled 创建拒绝、enabled 创建成功、敏感字段读取。
- 新增 `logics/connectors/remote/remote_connector_test.go`，覆盖 remote connector 元数据、字段配置、enabled setter/getter、`New` 配置传递、生命周期空实现与 metadata stub。
- 避开 `Init` 单例和全局 `logics.CTA`，不依赖数据库或外部服务。
- 不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./logics/connectors/factory ./logics/connectors/remote
env GOCACHE=/tmp/go-build-cache go test ./logics/connectors/... -cover
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./logics/connectors/factory ./logics/connectors/remote` 通过。
- `go test ./logics/connectors/... -cover` 通过。
- `go test ./...` 通过。
- overall statement coverage：**7.8%**。
- 包覆盖率：`logics/connectors/factory` 74.3%，`logics/connectors/remote` 34.1%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Batch 1 Connectors 覆盖补齐

范围：

- 延续 Step 12：`logics/connectors/factory`、`logics/connectors/remote` 已纳入本批次。
- 新增 `logics/connectors/local/fileset/anyshare/anyshare_test.go`，覆盖 AnyShare connector 元数据、字段配置、`New` 成功与多类配置错误、token/app secret 鉴权、metadata、entry doc lib discovery、file-search 请求组装、sort/output helper、时间/数值 helper、doc lib type 校验。
- 新增 `logics/connectors/local/index/opensearch/type_mapping_test.go`，覆盖 OpenSearch connector 元数据、字段配置、`New` 配置传递、`MapType`、基础字段 mapping、unsupported feature 错误。
- 将 `opensearch_fulltext_test.go`、`opensearch_groupby_test.go` 断言风格收敛到 `testing + testify`。
- AnyShare HTTP 覆盖使用自定义 `RoundTripper`，不启动本地监听、不访问真实服务。
- 不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./logics/connectors/local/fileset/anyshare ./logics/connectors/local/index/opensearch
env GOCACHE=/tmp/go-build-cache go test ./logics/connectors/... -cover
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./logics/connectors/local/fileset/anyshare ./logics/connectors/local/index/opensearch` 通过。
- `go test ./logics/connectors/... -cover` 通过。
- `go test ./...` 通过。
- overall statement coverage：**8.4%**。
- 包覆盖率：`factory` 74.3%，`remote` 34.1%，`anyshare` 24.2%，`opensearch` 8.7%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Batch 2 Service 业务规则首批

范围：

- 新增 `logics/connector_type/connector_type_service_test.go`，覆盖 connector type 详情权限过滤、列表权限过滤与分页、auth resource 授权过滤与分页、存在性检查、enabled 更新成功与错误包装。
- 新增 `logics/discover_task/discover_task_service_test.go`，覆盖 task get/list 账号名填充、账号服务错误包装、status/result/existence 委托、delete 去重、running/pending 拒绝、missing 处理、ignore missing、access 错误包装。
- 新增 `logics/discover_schedule/discover_schedule_service_test.go`，覆盖 schedule create cron 校验与持久化字段、update 字段变更、get/list 账号名填充、enable/disable/delete/update last run 委托、ExecuteSchedule 的缺少 task service、已有 running 跳过、创建 scheduled task 并更新 last run、list 错误透传。
- 本批先覆盖不依赖真实 asynq/worker/factory 的 service 分支；涉及 factory 注册和真实队列的 create/register/update/delete 深路径后续单独处理。
- 不改生产逻辑。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./logics/connector_type ./logics/discover_task ./logics/discover_schedule
env GOCACHE=/tmp/go-build-cache go test ./logics/connector_type ./logics/discover_task ./logics/discover_schedule -cover
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./logics/connector_type ./logics/discover_task ./logics/discover_schedule` 通过。
- `go test ./...` 通过。
- overall statement coverage：**9.1%**。
- 包覆盖率：`logics/connector_type` 52.6%，`logics/discover_task` 62.2%，`logics/discover_schedule` 75.2%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。

### 2026-07-09：Batch 3 Logic View / Query 转换首批

范围：

- 新增 `logics/resource_data/logic_view/dsl/dsl_test.go`，覆盖 DSL 生成的分页、track total、排序补全/去重、text keyword sort、binary sort 拒绝、单 resource filter、多 resource should、unsupported union type、nil logic definition、filter condition 到 DSL 的 equal/range/and 和 text keyword 缺失错误。
- 新增 `logics/resource_data/logic_view/sql/sql_test.go`，覆盖 SQL resource/output 节点投影、缺少 output/output input 错误、join/union/sql template 节点构造、参数插值、limit helper、SQLBuilder where/order/limit 插入、sort 构造、SQL filter condition equal/like 转换。
- 发现现有 resource node filter + dollar placeholder 与 `interpolate(?)` 的兼容限制，本批不改生产逻辑，测试中避免把该路径作为期望成功路径；filter 转换逻辑已单独覆盖。
- 不触碰 antlr/parser 生成代码。

验证：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./logics/resource_data/logic_view/dsl ./logics/resource_data/logic_view/sql
env GOCACHE=/tmp/go-build-cache go test ./logics/resource_data/logic_view/... -cover
env GOCACHE=/tmp/go-build-cache go test ./...
env GOCACHE=/tmp/go-build-cache go test ./... -coverprofile=/tmp/vega-backend-server-cover.out
env GOCACHE=/tmp/go-build-cache go tool cover -func=/tmp/vega-backend-server-cover.out
```

结果：

- `go test ./logics/resource_data/logic_view/dsl ./logics/resource_data/logic_view/sql` 通过。
- `go test ./logics/resource_data/logic_view/... -cover` 通过。
- `go test ./...` 通过。
- overall statement coverage：**10.4%**。
- 包覆盖率：`logic_view/dsl` 27.2%，`logic_view/sql` 43.6%。
- 仍有 `/etc/profile.d/ulimit.sh` warning，不影响测试结果。
