# vega-backend server logics/worker UT 补齐计划

> 日期：2026-07-10  
> 当前阶段：`adp/vega/vega-backend/server/logics`  
> 目标：先聚焦 logics 层，用 Go 原生 `testing` + `testify` 补齐 UT，并统一既有 UT 风格；logics 收口后再进入 worker。

## 1. 当前阶段范围

当前阶段只处理 `server/logics`，暂不展开 `server/worker`。

本轮 logics 目录现状：

| 项 | 数量/结果 |
| --- | ---: |
| 生产 Go 文件 | 103 |
| 测试 Go 文件 | 53 |
| 最近一次专项命令 | `env GOCACHE=/tmp/go-build-cache go test -gcflags=all=-l ./logics/... -cover` |

当前覆盖快照：

| 包 | 覆盖率 |
| --- | ---: |
| `logics` | 41.9% |
| `logics/auth` | 33.3% |
| `logics/build_task` | 56.0% |
| `logics/catalog` | 47.2% |
| `logics/connector_type` | 52.6% |
| `logics/connectors/factory` | 74.3% |
| `logics/connectors/local/fileset/anyshare` | 24.2% |
| `logics/connectors/local/index/opensearch` | 26.1% |
| `logics/connectors/local/table` | 90.9% |
| `logics/connectors/local/table/mariadb` | 29.8% |
| `logics/connectors/local/table/oracle` | 32.7% |
| `logics/connectors/local/table/postgresql` | 22.0% |
| `logics/connectors/remote` | 34.1% |
| `logics/dataset` | 72.5% |
| `logics/discover_schedule` | 75.2% |
| `logics/discover_task` | 62.2% |
| `logics/extensions` | 100.0% |
| `logics/filter_condition` | 62.1% |
| `logics/local_index` | 61.3% |
| `logics/permission` | 85.9% |
| `logics/query` | 31.8% |
| `logics/query/sqlglot` | 15.0% |
| `logics/rate` | 65.5% |
| `logics/resource` | 32.3% |
| `logics/resource_data` | 38.3% |
| `logics/resource_data/logic_view` | 0.0% |
| `logics/resource_data/logic_view/dsl` | 63.5% |
| `logics/resource_data/logic_view/sql` | 73.3% |
| `logics/user_mgmt` | 50.0% |

## 2. 通用测试组织规则

- 一个需要覆盖的非构造生产函数对应一个顶层测试函数。
- 顶层测试函数内部所有场景都用 `t.Run` 包裹。
- 测试文件名必须和原始业务文件名一一对应：`foo.go` 的测试写在 `foo_test.go`；不要新增按场景、bug、函数类型或包级 helper 拆分的测试文件。公共 test helper 放到最贴近的业务测试文件中。
- 方法测试命名优先使用 `Test<Type><Method>`，例如 `TestResourceServiceListResources`、`TestRawQueryServiceQuery`。
- 包级函数/helper 测试命名使用 `Test<Function>`，例如 `TestBuildCondition`、`TestNormalizeBuildTaskRequest`。
- 如果旧 UT 已经为同一个生产函数拆出多个顶层测试函数，本轮触达时一起合并或调整。
- 断言使用 `require` 做前置条件和错误 gating，使用 `assert` 做结果校验。
- service 层通过 fake/mock access、connector、manager 注入隔离下游依赖。
- connector/query/filter 纯逻辑优先 table-driven cases，覆盖合法值、缺失值、边界值、非法枚举、错误传播等。
- 只改测试代码和测试必要依赖；不为 UT 修改业务代码。确需 mock 无注入点的方法时，测试侧使用 `gomonkey`。
- 因为本轮可能使用 `gomonkey`，所有 `go test` 命令统一加 `-gcflags=all=-l` 关闭 inline。
- 构造函数不作为补齐目标，不新增 `TestNew...` / `Test<Constructor>` 用例；已有构造函数测试本轮触达时删除或合并到真实业务行为测试中。

## 3. Logics 两步计划

### LG1：全部修改

状态：待执行。

范围：

- `logics/auth`、`permission`、`user_mgmt`
- `logics/catalog`、`resource`、`resource_data`、`dataset`
- `logics/build_task`、`discover_task`、`discover_schedule`
- `logics/query`、`query/sqlglot`
- `logics/filter_condition`、`extensions`、`rate`、`local_index`
- `logics/connectors/**`
- `logics/driven_access.go`、`cascade.go`

目标：

- 先补低覆盖且纯逻辑多的包：`query`、`resource`、`resource_data`、`connectors/**`、`filter_condition`。
- 对 service 层补齐主路径、下游错误、not found、参数边界、状态流转、权限/鉴权开关分支。
- 对 connector 层补齐 SQL/DSL/OpenSearch 条件构造、字段映射、group/fulltext/query helper、remote connector 错误传播。
- 对 access 聚合、cascade、local index、rate limiter 等共享逻辑补齐稳定分支。
- 清理既有 logics UT 风格：文件命名一一对应、构造函数测试删除、重复顶层测试合并进同一个函数。
- 开发过程中优先跑局部测试：`go test -gcflags=all=-l ./logics/<pkg> -run Test<Name> -count=1`。

建议 commit：`test(vega-backend): cover logics service and connector behavior`。

### LG2：收口

状态：待执行。

范围：

- `server/logics` 全量复扫
- 覆盖率、函数级遗漏、测试风格一致性、全仓验证

目标：

- 跑 `go test -gcflags=all=-l ./logics/... -coverprofile=/tmp/vega-logics-cover.out -count=1` 和 `go tool cover -func=/tmp/vega-logics-cover.out`，确认剩余低覆盖/0% 函数。
- 对 LG1 遗漏的非构造函数继续补 case；构造函数不补、不计入待补清单。
- 扫描所有顶层 `Test...` 是否包含 `t.Run`。
- 扫描所有 `*_test.go` 是否和原始业务文件名一一对应。
- 扫描旧式 `goconvey`、不必要的 `reflect.TypeOf` gomonkey patch、构造函数测试。
- 跑 `go test -gcflags=all=-l ./logics/... -cover`、`go test -gcflags=all=-l ./...`。
- 更新本文档的 logics 覆盖率快照、已完成项、剩余说明，并记录下一阶段 worker 入口。

建议 commit：`test(vega-backend): finish logics unit test sweep`。

## 4. Worker 后续入口

logics LG2 完成后再进入 `server/worker`，继续沿用同一套测试组织规则，并重新记录 worker 专项覆盖快照与两步计划。
