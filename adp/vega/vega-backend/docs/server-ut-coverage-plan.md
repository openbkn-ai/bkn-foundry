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
- 保留已有 goconvey 测试不强制迁移；新增和大改文件用 testify，逐步收敛。

注意：仓库根 `rules/TESTING.md` 旧规范仍写 Go assertions 使用 goconvey；本计划按本次需求改为新增 UT 使用 testify。后续如要统一全仓库规范，应单独更新测试规范文档。

## 5. 分阶段补齐计划

### Phase 1：低成本高收益基础覆盖

目标：快速拉起基础覆盖，优先纯函数和稳定规则。

- `common/utils.go`：`GiBToBytes`、`GetQueryOrDefault`、`EscapeLikePattern`。
- `common/visitor`：visitor 生成格式、空值和稳定字段。
- `errors/*`：错误码、错误对象构造、扩展错误码映射。
- `driveradapters/validate_*.go`：把缺口补到主要 create/update/list 参数，特别是 resource data、catalog、connector type。
- `logics/connectors/local/table/{postgresql,mariadb,oracle}`：字段类型映射、条件 SQL、discover schema 解析边界。

建议验收：

- 新增测试全部为 testify。
- `go test ./...` 通过。
- 纯逻辑包覆盖率显著提升，整体 coverage 目标先到 12%+。

### Phase 2：service 层业务规则

目标：覆盖核心 use case 编排，不碰真实外部服务。

- `logics/connector_type`：create/update/list/delete、enabled 状态、重复/不存在错误。
- `logics/discover_task`：创建任务、状态更新、进度/结果更新、已存在任务校验。
- `logics/discover_schedule`：enable/disable、cron next run、调度参数校验。
- `logics/dataset`：dataset schema、写入/查询委托、错误透传。
- `logics/auth`、`user_mgmt`：noop 与外部 adapter 选择、错误降级。
- `logics/resource_data/logic_view`：DSL/SQL 条件组合、非法表达式、空条件、字段映射。

建议验收：

- 每个 service 的成功路径、依赖错误、业务拒绝路径至少各 1 组 case。
- service 层通过 gomock 验证关键调用参数。
- `logics/*` 重点包覆盖率目标 45%+。

### Phase 3：driven adapters 数据访问边界

目标：覆盖 SQL access 层最容易回归的 query 构造和扫描。

- `drivenadapters/catalog`：Create/Get/List/Update/Delete、extension join、enabled/health 状态更新。
- `drivenadapters/resource`：Create/Get/List/Update/Delete、category/status 过滤、auth resource list、discover status。
- `drivenadapters/connector_type`：扫描、list filter、enabled 更新。
- `drivenadapters/discover_task`、`discover_schedule`：分页、排序、状态更新、cron next run。
- `drivenadapters/entityextension`：Replace/Get/Delete/ApplyJoins/FilterKeys。

建议验收：

- access 层使用 `sqlmock`，覆盖 `rows.Close`、`sql.ErrNoRows`、扫描错误、exec 失败。
- 对动态 SQL 只断言关键片段和参数顺序，避免测试过脆。
- `drivenadapters` 重点包覆盖率目标 35%+。

### Phase 4：外部 adapter 与 worker

目标：覆盖异步和外部系统边界，不引入真实服务。

- `drivenadapters/permission`：BKN Safe HTTP 成功/拒绝/错误、shadow mode、filter resources。
- `drivenadapters/auth`、`user_mgmt`：token 校验、账号查询、外部错误处理。
- `drivenadapters/kafka`、`asynq`：配置转换、client option 生成、空配置和非法配置。
- `worker/discover_*`：table/fileset/index 发现、reconcile、enrich 状态。
- `worker/schedule_worker`：start/stop/reload、schedule/unschedule/update、执行失败回写。
- `worker/task_worker_manager`：任务路由、handler 错误、panic 防护。

建议验收：

- worker 使用 fake access/service + gomock，不依赖真实 queue。
- 状态流转覆盖成功、部分失败、全失败、重复执行。
- 整体 coverage 目标 25%+，高风险包覆盖 50%+。

## 6. 推荐执行顺序

1. 先补 `common`、`errors`、`driveradapters/validate_*`、connector condition/type mapping，建立 testify 写法样板。
2. 再补 service 层：`connector_type`、`discover_task`、`discover_schedule`、`dataset`。
3. 然后补 access 层：从 `connector_type`、`discover_task`、`discover_schedule` 开始，小包验证 sqlmock 模式，再扩展到 `catalog/resource` 大包。
4. 最后补 worker 和 permission/auth/user_mgmt/kafka/asynq 外部边界。

## 7. 每轮补测检查清单

- `go test ./...` 必须通过。
- 新增测试不依赖外部服务、环境变量或固定机器配置。
- 新增测试文件使用 `testing` + `testify`，除非只是在局部维护旧 goconvey 文件。
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
