# Context Loader

English: [README.md](README.md).

## 模块概述

Agent Retrieval 是 ADP context-loader 中的检索服务组件，面向知识网络语义检索、行动召回、逻辑属性解析以及 MCP（Model Context Protocol）服务。该模块与 ontology-query、ontology-manager、算子集成等后端服务集成，为智能体提供知识检索与工具调用能力。

## 核心功能

核心功能为五个工具及 Context Loader MCP 服务：

1. **kn_search**：知识网络搜索，支持概念与实例级语义检索
2. **query_object_instance**：按对象类型查询知识网络中的实例
3. **query_instance_subgraph**：按深度与关系配置查询实例周边子图
4. **get_action_info**：获取行动元数据、入参 Schema 与执行结果 Schema（`output_schema`），供算子/智能体工具使用
5. **get_logic_properties_values**：解析逻辑属性并获取属性值
6. **Context Loader MCP Server**：对外提供上述五个工具，供 Cursor、Claude Desktop 等 MCP 客户端调用

## Interaction 终态提交预算

finish 的准备工作保留现有 1 秒 Trace 观察预算；最终 POST／幂等重放另用默认
5 秒总预算，最多 3 次完全相同的请求，包含退避，不是每次都获得 5 秒。
配置为 `observability.lifecycle.finish_timeout_ms` 或
`BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS`，允许 1–30000 毫秒，无效值回退到 5000。
工具 attempt 的既有收尾预算不变。授权准备完成后，即使调用方断开，仍在此有限预算内
尝试完成提交。只有 Core 返回匹配的 Interaction ID 和所请求终态，才确认成功。
`interaction_finish_unconfirmed` 是 MCP 工具错误，指引 `retry_same_finish`：重试原 finish，
不要重跑业务工具。错误附带阶段、原因码和 request ID；领域拒绝不重试、不覆盖。
不增加持久化客户端重试队列；进程退出或持续故障时，未被 Core 接受的 Interaction
仍可能保持 active，直到现有租约规则回收。

## 权限边界

对象查询面（`search_schema`、`get_kn_detail`、`get_object_types`、对象实例、子图和逻辑属性）统一消费 ontology-query 的授权安全结果：`none` 属性不暴露，`schema` 只暴露元数据，`masked` 实例值按列掩码规则返回；规则仅作用于查询结果中的属性值，不修改源数据。仅 `full` 属性可用于过滤、排序、对象标识或逻辑属性依赖。REST 与 MCP 复用同一服务；Schema、对象实例和逻辑属性响应会返回 ontology-query 提供的有效权限摘要。

`list_resources`、`describe_resource`、`run_sql` 属于独立的物理资源查询面，按调用者自己的 Vega 数据资源权限授权，不受对象属性级别约束，可能暴露原始物理列。对象查询实现不得在内部调用这三个工具；仅当调用者明确要求物理资源分析时使用。

## 技术架构

### 技术栈

- **语言**：Go 1.26.0
- **Web 框架**：Gin 1.11
- **可观测**：OpenTelemetry
- **缓存**：Redis（可选，支持哨兵模式）
- **校验**：go-playground/validator

### 项目结构

``` bash
server/
├── driveradapters/     # HTTP/MCP 适配层
│   ├── knactionrecall/
│   ├── knlogicpropertyresolver/
│   ├── knqueryobjectinstance/
│   ├── knquerysubgraph/
│   ├── knsearch/
│   ├── mcp/            # MCP 服务与 Schema
│   └── mcpproxy/
├── drivenadapters/     # 后端/数据访问
│   ├── ontology_query.go
│   ├── ontology_manager.go
│   ├── operator_integration.go
│   ├── data_retrieval.go
│   ├── agent_app.go
│   └── ...
├── interfaces/         # 端口接口定义
├── logics/             # 业务逻辑
│   ├── knactionrecall/
│   ├── knlogicpropertyresolver/
│   ├── knquerysubgraph/
│   └── knsearch/
├── infra/              # 配置、日志、遥测、国际化
│   ├── config/
│   ├── logger/
│   ├── telemetry/
│   └── localize/
└── main.go
```

## 快速开始

### 环境要求

- Go 1.26.0+
- 可访问的 ontology-query、ontology-manager 等后端（或通过 ktctl 连接远程集群）

### 本地开发

1. **克隆**

   ```bash
   git clone <仓库地址>
   cd context-loader/agent-retrieval
   ```

2. **配置**
   编辑 `server/infra/config/agent-retrieval.yaml`（及按需 `agent-retrieval-secret.yaml`）中的服务地址与端口。若本地连远程集群，需先配置 ktctl，再执行 `make build`，以便将配置复制到 `/sysvol/config` 与 `/sysvol/secret`。

3. **依赖**

   ```bash
   go mod download
   ```

4. **运行**

   ```bash
   # 方式一：配合 ktctl 与 make（推荐开发）
   make build

   # 方式二：仅启动进程（使用默认配置路径）
   cd server && go run main.go
   ```

   默认监听 `http://0.0.0.0:30779`。另在 `30780` 端口仅暴露经过鉴权的
   公共接口，供沙箱负载访问；可信 `/in` 接口仍只在 `30779` 端口提供。

### Make 目标

- `make help`：查看目标说明
- `make lint`：执行 golangci-lint
- `make test`：单元测试
- `make test-cover`：带覆盖率的测试
- `make generate-mock`：生成 mock
- `make helm-template`：渲染 Helm 模板
- `make preview`：预览 API 文档（在 `docs/` 下）

### Docker

需在 **agent-retrieval 目录**下构建，与 CI 使用的上下文一致，使 `go.mod` 和 `server/` 位于构建根目录：

```bash
# 在 context-loader/agent-retrieval 目录执行
docker build -t agent-retrieval:latest -f docker/Dockerfile .
```

运行：

```bash
docker run -d -p 30779:30779 --name agent-retrieval agent-retrieval:latest
```

### Kubernetes / Helm

在 `agent-retrieval` 目录下：

```bash
helm install agent-retrieval ./helm/agent-retrieval/
# 或仅渲染模板：
helm template agent-retrieval ./helm/agent-retrieval -n <命名空间> -f ./helm/agent-retrieval/values.yaml
```

## 配置说明

主配置文件：`server/infra/config/agent-retrieval.yaml`。

主要配置项：

- **project**：`host`、`port`（默认 30779）、`sandbox_port`（默认 30780）、`language`、`logger_level`、`debug`
- **ontology_query**、**ontology_manager**、**data_retrieval**、**operator_integration**：各后端服务地址与端口
- **oauth**：Hydra OAuth（对外 API）
- **redis**：可选 Redis（如哨兵模式）缓存
- **concept_search_config**、**deploy_agent**、**rerank_llm**：功能相关配置

敏感信息：`server/infra/config/agent-retrieval-secret.yaml`（按需加入版本控制忽略）。

## 监控与运维

- **就绪探针**：`GET /health/ready`
- **存活探针**：`GET /health/alive`
- **日志**：结构化日志，级别与输出方式可在配置中调整
- **链路追踪**：OpenTelemetry，在 `observability` 配置中启用与配置

## 开发规范

- 遵循 Go 官方编码规范与项目约定
- 保持清洁架构：driver/driven 适配层、接口与业务逻辑分层清晰
- 提交前执行 `make lint` 与 `make test`（或 `make test-cover`）

## 版本

版本号见项目根目录下的 `VERSION` 文件。

## 支持与联系

- **团队**：OpenBKN
- **文档**：见 `docs/`（PRD、API、发布说明等）。

## 可选 HTTP MCP 扩展

Context Loader 可从 HTTP 扩展提供方加载付费工具，不内嵌其业务逻辑。通过 Helm
`mcpExtensionManifestURL` 或环境变量 `CONTEXT_LOADER_EXTENSION_MANIFEST_URL`
指定清单地址。默认为空，现有工具目录不变。修改清单内容或地址后须重启 Context
Loader；已配置的提供方加载失败时启动失败，不发布无法调用的工具。

对于付费目录接口，可同时通过 `mcpExtensionManifestJSON` 提供部署工具定义，
在许可证激活前完成注册；工具定义本身不授予付费能力。
使用付费扩展时，设置 `licenseHub.url`，例如
`--set-string licenseHub.url=http://bkn-safe:3000`；非空时 Chart 注入 `BKN_SAFE_URL`。
此项默认为空，不从平台公共的 `bknSafe.url` 自动继承，因此社区默认部署保持原行为。
发现和调用工具仍需有效许可证和当前用户权限。

升级时应将地址保存在 Helm values 中，不能仅修改 Pod。Eval 企业部署描述同时提供
工具清单和许可证地址；社区安装器会保留已有 Helm 配置及运行中的非空自定义许可证地址。

Eval 使用 EE 后端的清单地址
`http://<bkn-backend-host>:<port>/api/bkn-backend/v1/eval-mcp/tools`，并在该后端设置
`OPENBKN_EVAL_ENABLED=true`，不再配置独立外部参评开关。
客户端仍连接 `/api/agent-retrieval/v1/mcp/` 或精简 MCP 入口，受专业版及以上许可和后端
用户权限控制。提供的工具为 `bkn_eval_list_suites`、`bkn_eval_start_run`、
`bkn_eval_next_case`、`bkn_eval_submit_result`、`bkn_eval_get_progress`。

提供方维护工具 Schema 及同源调用路径。Context Loader 只向该路径转发已认证传输的
`Authorization`，不信任账号头或工具参数中的凭据，也不跟随重定向。Eval 控制工具可在
没有既有 Interaction 时发起运行；业务工具仍要求原有生命周期上下文。每道评测题目的
Interaction 和 Trace 由 Eval 后端创建与核验，已有业务工具及证据处理保持不变。
