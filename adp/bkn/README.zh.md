# BKN Engine

中文 | [English](README.md)

`adp/bkn` 是 BKN Engine 的后端子系统，负责业务知识网络
（Business Knowledge Network）的建模与查询能力。它属于 BKN Foundry
后端，不包含独立产品 UI。

当前子系统由两个 Go 服务组成：

| 服务 | 路径 | 默认端口 | 职责 |
| --- | --- | --- | --- |
| BKN Backend | `bkn-backend/` | `13014` | 知识网络建模、校验、导入导出、概念索引、指标、风险类和行动调度 |
| Ontology Query | `ontology-query/` | `13018` | 对象数据查询、属性查询、子图查询、行动执行、行动日志和指标数据查询 |

## 能力范围

已实现并处于维护中的能力：

- 知识网络 CRUD 与校验。
- 对象类、关系类、行动类、概念组、指标、风险类和行动调度管理。
- 通过公开 API 导入和导出 BKN 包。
- 基于 OpenSearch 和已配置 embedding 模型的概念索引与搜索。
- 对象实例、对象属性、子图和指定对象起点的子图查询。
- 行动异步执行、执行记录、状态查询、日志查询和取消。
- 通过 Ontology Query 与 VEGA resource data 执行指标查询和 dry-run。
- BKN Trace outbox 的查看、手动重试和废弃操作。

已知边界：

- `branch` 作为模型和查询维度已被支持，但该目录没有提供完整的分支生命周期 API，例如创建、合并、发布或回滚。
- `bkn-backend` 中的细粒度权限集成尚未完全强制执行，部分权限 hook 仍在服务层关闭。
- 自然语言规划和高层上下文召回不在 `adp/bkn` 内完成，对应能力属于 `adp/context-loader`。
- 查询执行依赖 BKN 元数据、VEGA resource data、OpenSearch 索引和 model-factory embedding 配置。
- 部分高级指标字段尚未形成完整端到端能力，应以 OpenAPI 说明和集成测试为准。

## 仓库结构

```text
adp/bkn/
  bkn-backend/       # 建模与管理服务
  ontology-query/    # 查询与行动执行服务
  AGENTS.md          # 本子系统的 Agent 协作规则
  README.md          # 英文说明
  README.zh.md       # 本文件
```

两个服务大体遵循相同目录结构：

```text
server/
  common/            # 公共工具、配置和条件处理
  config/            # 本地配置文件
  drivenadapters/    # 数据库、OpenSearch、model-factory、VEGA 和下游客户端
  driveradapters/    # HTTP handler 和 router
  errors/            # 服务错误定义
  interfaces/        # DTO 和服务接口
  locale/            # 国际化资源
  logics/            # 业务逻辑
  main.go            # 服务入口
```

`bkn-backend` 额外包含 `worker/`，用于后台索引和任务执行；还包含
`server/bkn-specification/`，用于 BKN 包解析、序列化和导入导出。

## API 文档

权威 API 文档位于仓库根目录：

| 服务 | OpenAPI 源文件 |
| --- | --- |
| BKN Backend | `docs/api/bkn/*.yaml` |
| Ontology Query | `docs/api/ontology-query/ontology-query.yaml` |

在仓库根目录生成或检查文档：

```bash
npm install
make api-docs-lint
make api-docs-html
```

公开 API 前缀：

```text
/api/bkn-backend/v1
/api/ontology-query/v1
```

内部 API 前缀：

```text
/api/bkn-backend/in/v1
/api/ontology-query/in/v1
```

## 本地开发

依赖要求：

- Go 1.24+
- MariaDB 11.4+ 或 DM8
- OpenSearch 2.x
- 按各服务 `server/config/*.yaml` 配置可访问的依赖服务

运行 BKN Backend：

```bash
cd adp/bkn/bkn-backend/server
go mod download
go run main.go
```

运行 Ontology Query：

```bash
cd adp/bkn/ontology-query/server
go mod download
go run main.go
```

健康检查：

```bash
curl http://localhost:13014/health
curl http://localhost:13018/health
```

## 测试

每个服务都通过自己的 Makefile 提供标准入口：

```bash
cd adp/bkn/bkn-backend
make test
make test-cover
make lint
make ci

cd ../ontology-query
make test
make test-cover
make lint
make ci
```

单元测试需要 `I18N_MODE_UT=true`；Makefile 会自动设置。

## 部署

常规部署入口是仓库级 `deploy/` 安装器和 release manifest。

服务级镜像构建示例：

```bash
cd adp/bkn/bkn-backend
docker build -t bkn-backend:latest -f docker/Dockerfile .

cd ../ontology-query
docker build -t ontology-query:latest -f docker/Dockerfile .
```

Helm chart 位于：

```text
adp/bkn/bkn-backend/helm/bkn-backend/
adp/bkn/ontology-query/helm/ontology-query/
```

## 维护说明

- 保持本 README 与实际 router 和根部 OpenAPI 文件同步。
- 不要重新引入旧的 `api_doc/*.html` 文档入口。
- 如果某项能力由其他子系统实现，应明确写出所属子系统，不要描述为 `adp/bkn` 原生能力。
