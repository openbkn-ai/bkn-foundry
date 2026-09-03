# BKN Backend

中文 | [English](README.md)

`bkn-backend` 是 BKN Engine 的建模与管理服务。它负责业务知识网络
（Business Knowledge Network）的元数据模型，并提供创建、校验、更新、
搜索、导入和导出 BKN 定义的 API。

## 当前能力范围

已实现能力：

- 知识网络创建、列表、详情、更新、删除、名称查询和校验。
- 对象类管理，包括数据属性、逻辑属性、校验、样例数据查询和部分数据属性更新。
- 关系类管理和关系类型路径查询。
- 行动类管理、校验和概念搜索。
- 概念组管理，包括嵌套概念和对象类成员关系。
- 指标定义管理和校验。
- 风险类管理。
- 行动调度管理，包括 cron 校验、状态更新和下次运行时间计算。
- 通过 VEGA 集成查询资源列表。
- BKN 包上传和下载，用于导入导出。
- 通过 OpenSearch 和 model-factory embedding 进行概念索引与搜索。
- BKN Trace outbox 查看、重试和废弃操作。

重要边界：

- `branch` 作为数据维度通过查询参数支持，但服务没有提供完整的分支生命周期 API，例如创建、合并、发布或回滚。
- 权限资源 hook 已存在，但细粒度资源权限强制执行在服务层尚未完整闭环。
- 自然语言查询规划和上下文加载不由本服务负责，对应能力在 `adp/context-loader`。
- 对象样例数据和资源相关操作依赖 VEGA 可用。
- 向量搜索和概念索引依赖 OpenSearch 与 model-factory embedding 配置。

## 主要 API 分组

公开 API 前缀：

```text
/api/bkn-backend/v1
```

内部 API 前缀：

```text
/api/bkn-backend/in/v1
```

代表性公开路由注册在 `server/driveradapters/routers.go`：

| 资源 | 路由 |
| --- | --- |
| 知识网络 | `/knowledge-networks`、`/knowledge-networks/{kn_id}`、`/knowledge-networks/{kn_id}/validation` |
| 概念组 | `/knowledge-networks/{kn_id}/concept-groups` |
| 对象类 | `/knowledge-networks/{kn_id}/object-types` |
| 关系类 | `/knowledge-networks/{kn_id}/relation-types`、`/knowledge-networks/{kn_id}/relation-type-paths` |
| 行动类 | `/knowledge-networks/{kn_id}/action-types` |
| 指标 | `/knowledge-networks/{kn_id}/metrics` |
| 风险类 | `/knowledge-networks/{kn_id}/risk-types` |
| 行动调度 | `/knowledge-networks/{kn_id}/action-schedules` |
| BKN 导入导出 | `/bkns`、`/bkns/{kn_id}` |
| 资源 | `/resources` |
| Trace outbox | `/api/bkn-backend/v1/trace/outbox` |

权威 OpenAPI 源文件位于仓库根目录：

```text
docs/api/bkn/*.yaml
```

在仓库根目录运行：

```bash
npm install
make api-docs-lint
make api-docs-html
```

## 架构

```text
bkn-backend/
  server/
    common/              # 公共工具、配置、条件处理和 trace helper
    config/              # 服务配置
    drivenadapters/      # DB、OpenSearch、VEGA、model-factory、bkn-safe 客户端
    driveradapters/      # HTTP handler、router 和请求校验
    errors/              # BKN Backend 错误定义
    interfaces/          # DTO 和服务接口
    locale/              # 国际化资源
    logics/              # 业务逻辑
    version/             # 构建和版本元数据
    worker/              # 后台概念同步和任务执行
    bkn-specification/   # BKN 包解析和序列化
```

核心逻辑包：

| 包 | 职责 |
| --- | --- |
| `knowledge_network` | 知识网络生命周期编排 |
| `object_type` | 对象类校验、CRUD、索引和样例数据 |
| `relation_type` | 关系类校验、CRUD 和路径支持 |
| `action_type` | 行动类校验、CRUD、搜索和来源检查 |
| `concept_group` | 概念组树和成员管理 |
| `metric` | 指标定义校验、CRUD 和搜索 |
| `risk_type` | 风险类 CRUD 和概念搜索 |
| `action_schedule` | 行动调度元数据和 cron 处理 |
| `permission` | 权限资源集成 hook |
| `bkn` | BKN 导入导出服务 |

## 本地开发

依赖要求：

- Go 1.25+
- MariaDB 11.4+ 或 DM8
- OpenSearch 2.x
- 调试集成路径时，需要可访问的 VEGA、model-factory 和 bkn-safe 依赖。

本地配置文件：

```text
server/config/bkn-backend-config.yaml
```

本地运行：

```bash
cd adp/bkn/bkn-backend/server
go mod download
go run main.go
```

默认端口：

```text
http://localhost:13014
```

健康检查：

```bash
curl http://localhost:13014/api/bkn-backend/v1/health
```

## 测试

使用模块 Makefile：

```bash
cd adp/bkn/bkn-backend
make test
make test-cover
make lint
make ci
```

Makefile 会为单元测试设置 `I18N_MODE_UT=true`。覆盖率产物输出到：

```text
adp/bkn/bkn-backend/test-result/
```

直接运行单个包的示例：

```bash
cd adp/bkn/bkn-backend/server
I18N_MODE_UT=true go test ./logics/object_type/... -v
```

依赖外部服务的集成测试必须通过显式环境变量或 build tag 隔离，不应进入默认
`make test` 路径。

## 构建与部署

构建服务二进制：

```bash
cd adp/bkn/bkn-backend/server
go build -o bkn-backend .
```

构建 Docker 镜像：

```bash
cd adp/bkn/bkn-backend
docker build -t bkn-backend:latest -f docker/Dockerfile .
```

Helm chart：

```text
adp/bkn/bkn-backend/helm/bkn-backend/
```

常规产品部署入口是仓库级 `deploy/` 安装器，它会将本服务和依赖一起安装。

## 维护检查项

- 保持 `server/driveradapters/routers.go` 与 `docs/api/bkn/*.yaml` 同步。
- README 只描述已实现的服务行为，不描述属于其他仓库或产品 UI 的能力。
- 修改校验、导入导出、索引或 API 行为时，同步更新测试。
- 权限、分支生命周期、模型/索引兼容性相关变更应视为跨服务变更，并明确说明影响范围。
