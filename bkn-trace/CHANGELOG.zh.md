# 更新日志

本文件记录项目的重要变更。

## [Unreleased]

### 变更

- 按 service 筛选的 Trace 列表使用单条 SQL 计算总数与非空页，减少重复 canonical service 查询，并保持游标总数及空页行为；补充隔离 MariaDB/OpenSearch 容量验收。（#2150）

- Interaction 操作读取支持不含 payload 的 `view=summary`，默认 full 响应保持兼容；仅按 service 筛选的 Trace 列表改用 canonical MariaDB identity 计数与分页，避免固定候选投影扫描。（#2005、#2143、#2145）

- 记录完整性物化失败后采用进程内指数退避（30 秒至 5 分钟），减少重复证据读取和错误日志。源版本变化可立即重试，依赖恢复后仍能自动完成检查，完整性判定语义不变。（#2133）

- 关闭的 Audit Kafka consumer 以未配置的 `audit-ledger` 状态保留：审计查询返回带修复动作的不可重试配置错误，混合查询明确提示审计数据缺口。安装器升级时保留 consumer 的 Helm values，并在更新 release 前检查凭据与 MariaDB Secret 引用。
- Trace Core 现在通过带版本的 MariaDB Migration 统一创建 `bkn_trace_ee_provenance_analyses`，使社区版与企业版镜像都会先完成相同 Schema 初始化，再由可选企业路由使用该表。
- 模块目录由 `trace-ai/` 改名为 `bkn-trace/`，与平台 `bkn-*` 命名统一（展示名：BKN Trace）。Go module path 变更为 `github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability`；CI/发布流程、CODEOWNERS、Issue 路由同步更新。镜像与 Chart 名（`agent-observability`、`otelcol-contrib`）不变。
- 完整 OpenBKN 安装将 Trace Core 持久化到固定的 `bkn_trace` MariaDB 数据库，并将 Evidence 持久化到 OpenSearch；离线安装会把 Evidence 索引 Hook 镜像同步到离线仓库，在线安装保持 Chart 默认仓库，除非显式覆盖。
- 受管生命周期写入继续通过集群内部 `8081` Service 开放，并新增公开 `8080` 端口供 OAuth 鉴权后的 SDK 客户端调用。公开写入的 owner 身份由服务端派生，且仅允许部署方批准的租户；Evidence Event/Artifact 继续通过公开 `8080` 端口和独立 ingest token 写入。
- 完整安装器在安装 release 前创建或验证 Evidence ingest Secret，使 Context Loader 获取 token 的行为不依赖发布清单顺序。

### 升级说明

- `X-BKN-Trace-Query-Token` 读取鉴权及 `evidence.queryAuth.existingSecret` / `secretKey` Chart 参数已移除。平台内查询使用 OAuth；自定义集群外读取客户端必须迁移到 OAuth，旧的令牌参数升级后不会继续生效。
- 使用 external RDS 时，升级前必须创建固定数据库 `bkn_trace`，并让 `bkn-trace-core-mariadb` Secret 的 `dsn` 指向该数据库。安装前置检查会拒绝缺失、无效或指向其他数据库的 DSN。
- 内置 MariaDB 的 `openbkn`、`safe` 与 `bkn_trace` 数据库统一使用 `utf8mb4/utf8mb4_unicode_ci`；已有数据库不会被重建或修改字符集。

## [0.2.2] - 2026-04-10

### 优化

- 将 `agent-observability` Chart 默认 `image.tag` 调整为 `__VERSION__` 占位符，使打包出的 Chart 可以继承发布流程解析出的版本号。
- 更新 `agent-observability` 发布流程，在打包 Chart 前替换 values 中的 `__VERSION__` 占位符，确保 Chart 默认镜像 tag 与实际发布镜像 tag 保持一致。

## [0.2.1] - 2026-04-07

### 优化

- 统一 `agent-observability` 与 `otelcol-contrib` Helm Chart 的镜像 values 结构，改为使用 `image.registry`、`image.repository` 与 `image.tag`，以支持离线打包从默认 values 中提取镜像。
- 更新 `agent-observability` 发布流程，在打包 Chart 前将解析出的发布版本同步写入 Chart 默认 `image.tag`。

### 升级说明

- 如果部署时将包含 registry 的完整镜像地址覆盖到 `image.repository`，需要按新的 Chart values 结构拆分为 `image.registry` 与 `image.repository`。

## [0.2.0] - 2026-03-31

### 优化

- 更新 `.github/workflows/` 下的发布流程，统一改用迁移后的 `bkn-trace/` 路径，覆盖版本读取、Go 模块定位、Docker 构建上下文与 Helm Chart 打包路径。

### 文档

- 在 `README.md` 中补充 `opentelemetry-collector-contrib` 多架构 manifest 的创建与校验命令，便于后续镜像发布复用。

## [0.1.1] - 2026-03-27

### 优化

- 将按 `conversation_id` 查询 Trace 的默认结果上限提升到 `1000`，使单次请求可以返回更多匹配记录。

## [0.1.0] - 2026-03-25

项目首次发布。

### 新增

- 新增 `agent-observability` 服务，用于从 OpenSearch 查询智能体 Trace 数据。
- 新增 Trace 查询接口，支持原始 DSL 检索和按 `conversation_id` 查询，并生成 Swagger 文档。
- 新增 `agent-observability` 的 Docker、Helm 和 GitHub Actions 构建发布流程。
- 新增 `otelcol-contribute-chart` Helm Chart，用于在 Kubernetes 中部署 OpenTelemetry Collector Contrib。
- 新增 Collector 默认 OTLP 接入与 OpenSearch 导出配置，支持 Trace 和 Log 链路处理。
- 新增仓库级中英文 README，说明 Tracing AI 的定位、架构、核心能力与快速开始方式。

### 文档

- 新增 `agent-observability/docs/` 下的 Agent 链路系统文档，包括 PRD、设计文档、API 描述与 Swagger 产物。
