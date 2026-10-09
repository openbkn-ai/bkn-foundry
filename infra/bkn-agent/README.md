# bkn-agent

平台内置 Agent 运行时（Epic #202，设计文档：`docs/design/bkn-agent/features/202-bkn-agent.md`）。

**仅面向平台内部**：调用主体为平台模块（服务身份）与内部工程师，终端用户流量不可直达（硬约束）。

- 引擎：LangGraph（Python）
- 模型：mf-model-api（OpenAI 兼容，集群内 `/api/private`，model 为空走系统默认）
- 工具：执行工厂 toolbox 统一工具平面（默认挂 contextloader 内置工具集，
  执行走 operator-integration 执行代理，身份 header 透传）；外部 MCP 端点经
  `type: "mcp"` 显式挂载
- 技能：执行工厂 internal-v1（`/skill/content` 注入 + `/skill/files/read` 渐进读取）
- 存储：共享 `openbkn` 库，`agent_` 前缀表，迁移见 `migrations/bkn-agent/`

## 本地运行

```bash
pip install -r requirements.txt
uvicorn main:app --port 30800
```

关键环境变量见 `app/config.py`（RDS*、MF_MODEL_API_PRIVATE_BASE、OPERATOR_INTEGRATION_BASE、CHECKPOINTER_BACKEND）。

## Trace Evidence 安装与升级

全新 Helm 安装默认开启 `observability.evidencePublisher.enabled`。启用前需提供 Kafka brokers 和既有 Secret（默认 `bkn-trace-evidence-kafka`，非空 `username`、`password`）；标准安装器沿用平台现有 Kafka 凭据准备流程，安装 Agent 前只读检查最终引用。独立 Helm 安装由部署者提前准备这些引用。没有 Kafka 的部署应显式设置 `observability.evidencePublisher.enabled=false`；此时不渲染 Kafka 或准入配置，也不检查 Kafka Secret，Artifact 上传仍有自己的凭据。

标准安装器的覆盖顺序为：Chart/安装默认值 → 已安装的 Evidence publisher 配置 → 当前配置文件 → CLI `--set`。升级不会自动开启原来关闭的发布器；若要开启，需在持久配置文件中显式设为 true。仅保留受支持的 publisher 字段和 Secret 引用，不保留内联凭据或旧版内部 OAuth/签名配置。不使用全量 `--reuse-values`。

内部策略、配置、heartbeat、ACK 使用现有 `agent-observability-internal:8081`，不需要内部 OAuth 或签名 Secret。发布器启动不等于准入已开启或事件已持久化；中央策略、预算、Kafka 消费与 Ledger/投影仍各自遵循已有合同。运行时 Kafka/控制配置初始化失败会记录错误类型并保持 Agent 可启动，修正持久配置后重启；不会制造事件、绕过准入或认证记录完整。预算与消费者配置由各自部署配置管理。

**工具面零默认**：`agent.tools` 就是工具全集，没有任何隐式挂载 —— 零声明即零工具，要用工具在 agent 定义里显式写 `type: "toolbox"` / `"mcp"` / `"agent"` 引用（`type: "toolbox"` 引用失败报错，不静默降级）。内置 `read_skill_file` 只在声明了技能、或已经装了别的工具时才挂，不会单独把图撑出 tools 节点。

## API

契约冻结于 `docs/api/bkn-agent/bkn-agent.yaml`（OpenAPI 3.1，#212）。改 API 走 spec 先行：
先改实现里的路由/模型并跑 `python scripts/export_openapi.py` 重新导出，
`app/test/test_contract.py` 强制 spec 与实现一致。

`/api/bkn-agent/v1/`：agents CRUD、`POST /chat`（SSE）、`POST /run` + `GET /tasks/{id}`、
`POST /invoke/{agent_id}`（同步一次性，等到终态才返回）、`GET /threads/{id}`（会话历史）、
提示词管理与调用方覆写（/prompts、/agents/{id}/prompt）、导入导出（/export、/import：
agent 定义+prompt 当前版本，保留原 id upsert 幂等，同名不同 id 记 failed 不中断，
跨环境引用缺失记 warning）。

## 算子工厂注册

不再有。published agent 曾被自动注册进算子工厂 toolbox（#212），现已移除：
agent 只通过本服务自身的 `/api/bkn-agent/v1` 面对外，不再在执行工厂里留一份工具描述。
把某个 agent 挂给另一个 agent 用 `tool_refs` 里的 `type: agent`，不经过工厂。
