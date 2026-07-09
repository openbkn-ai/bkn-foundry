# vega-backend server adapter UT 补齐计划

> 日期：2026-07-09  
> 当前阶段：`adp/vega/vega-backend/server/driveradapters`  
> 目标：聚焦 driver adapter 层，用 Go 原生 `testing` + `testify` 补齐 UT，并统一既有 UT 风格。

## 1. 当前阶段范围

当前阶段只处理 `server/driveradapters`，不继续扩大到 `logics`、`drivenadapters`、`worker`、`common`、`errors` 等目录。

当前 driveradapters 文件清单：

| 文件 | 说明 |
| --- | --- |
| `router.go` | Gin 路由注册、中间件、健康检查、鉴权入口 |
| `catalog_handler.go` | catalog HTTP handler |
| `resource_handler.go` | resource HTTP handler |
| `resource_data_handler.go` | resource data HTTP handler |
| `query_handler.go` | raw query HTTP handler |
| `connector_type_handler.go` | connector type HTTP handler |
| `build_task_handler.go` | build task HTTP handler |
| `discover_task_handler.go` | discover task HTTP handler |
| `discover_schedule_handler.go` | discover schedule HTTP handler |
| `auth_resource_handler.go` | auth resource HTTP handler |
| `validate.go` | 通用参数校验 |
| `validate_catalog.go` | catalog 参数校验 |
| `validate_resource.go` | resource 参数校验 |
| `validate_resource_data.go` | resource data 参数校验 |
| `validate_connector_type.go` | connector type 参数校验 |
| `validate_build_task.go` | build task 参数解析/校验 |
| `validate_discover_task.go` | discover task 参数校验 |
| `validate_discover_schedule.go` | discover schedule 参数校验 |

## 2. 通用测试组织规则

- 一个需要覆盖的非构造生产函数对应一个顶层测试函数。
- 顶层测试函数内部所有场景都用 `t.Run` 包裹。
- 测试文件名必须和原始业务文件名一一对应：`foo.go` 的测试写在 `foo_test.go`；不要新增按场景、bug、函数类型或包级 helper 拆分的测试文件。公共 test helper 放到最贴近的业务测试文件中。
- 方法测试命名优先使用 `Test<Type><Method>`，例如 `TestRestHandlerListCatalogs`、`TestRestHandlerCreateResource`。
- 包级函数/helper 测试命名使用 `Test<Function>`，例如 `TestValidateResourceRequest`、`TestParseBuildTaskListParams`。
- 如果旧 UT 已经为同一个生产函数拆出多个顶层测试函数，本轮触达时一起合并或调整。
- 断言使用 `require` 做前置条件和错误 gating，使用 `assert` 做结果校验。
- HTTP handler 使用 `httptest` + gin test context/router；下游依赖使用 mock service/fake service。
- 校验函数使用 table-driven cases，覆盖合法值、缺失值、边界值、非法枚举、非法分页/排序等。
- 只改测试代码和测试必要依赖；不为 UT 修改业务代码。确需 mock 无注入点的方法时，测试侧使用 `gomonkey`。
- 构造函数不作为补齐目标，不新增 `TestNew...` / `Test<Constructor>` 用例；已有构造函数测试本轮触达时删除或合并到真实业务行为测试中。

## 3. 当前覆盖快照

最近一次 driver 专项命令：

```bash
cd adp/vega/vega-backend/server
env GOCACHE=/tmp/go-build-cache go test ./driveradapters -cover
```

结果：

| 包 | 覆盖率 |
| --- | ---: |
| `driveradapters` | 69.9% |

## 4. Driver 两步计划

### DR1：全部修改

状态：已执行第一轮集中补齐。

范围：

- 全部 `*_handler.go`
- 全部 `validate*.go`
- 既有 `driveradapters` UT 风格整理

目标：

- 按“一函数一测试函数 + t.Run”统一现有测试。
- 补齐 handler 主路径、参数错误、body 解析错误、service error、not found、权限/visitor 场景。
- 补齐 validate 系列的边界值和非法输入，尤其是 resource / resource data / build task / connector type / discover schedule。
- 补齐 router/middleware 中可稳定隔离的行为，例如 content-type、language、health check、access log 的非外部依赖分支。
- 不改业务代码；handler 依赖通过 mock service/fake service 注入，必要时测试侧用 `gomonkey`。
- 开发过程中优先跑局部测试：`go test ./driveradapters -run Test<Name> -count=1`。

建议 commit：`test(vega-backend): cover driver adapter handlers and validators`。

已覆盖：

- 统一既有 `driveradapters` 顶层测试函数的 `t.Run` 组织。
- 补齐 resource/resource data/query/catalog/build task/discover task/discover schedule/connector type/auth resource handler 的主路径与关键异常路径。
- 补齐 resource data、logic view、connector type、通用 validate 的边界和非法输入。
- 补齐 router health check 与 JSON content-type middleware。
- raw query handler 使用测试侧 `gomonkey` patch 构造函数，不修改业务代码。

### DR2：收口

状态：已执行收口检查。

范围：

- `server/driveradapters` 全量复扫
- 覆盖率、函数级遗漏、测试风格一致性、全仓验证

目标：

- 跑 `go test ./driveradapters -coverprofile=/tmp/vega-driver-cover.out` 和 `go tool cover -func=/tmp/vega-driver-cover.out`，确认剩余低覆盖/0% 函数。
- 对 DR1 遗漏的非构造函数继续补 case；构造函数不补、不计入待补清单。
- 扫描所有顶层 `Test...` 是否包含 `t.Run`。
- 扫描所有 `*_test.go` 是否和原始业务文件名一一对应。
- 跑 `go test ./driveradapters -cover`、`go test ./...`。
- 更新本文档的 driver 覆盖率快照、已完成项、剩余说明。

建议 commit：`test(vega-backend): finish driver adapter test sweep`。

收口结果：

- `go test ./driveradapters -coverprofile=/tmp/vega-driver-cover.out -count=1` 通过，覆盖率 `69.9%`。
- 测试文件命名扫描通过：`*_test.go` 均对应同名业务 `.go`。
- 顶层 `Test...` 结构扫描通过：测试函数均包含 `t.Run` 场景。
- 剩余 0% 函数主要为外部接口薄 wrapper（`ByEx`）和纯构造函数；构造函数不作为本轮补齐目标。
