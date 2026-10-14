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
pip install --require-hashes -r requirements.lock
uvicorn main:app --port 30800
```

关键环境变量见 `app/config.py`（RDS*、MF_MODEL_API_PRIVATE_BASE、OPERATOR_INTEGRATION_BASE、CHECKPOINTER_BACKEND）。

## 0.2.1 LangGraph 依赖基线（#2174 / #537）

运行锁固定 LangGraph 1.2.15，并锁定 LangChain、checkpoint/MySQL saver、MCP adapters 和可观测集成的完整依赖树；`requirements.txt` 继续表达可接受范围。Docker 从 `requirements.lock` 安装，CI 从同一运行锁和受它约束的 `test-requirements.lock` 安装，均验证分发文件 hash。测试依赖不进入运行镜像。

```bash
pip install --require-hashes -r test-requirements.lock
OTEL_ENABLED=false python -m pytest -q
```

显式升级时生成两份协调的锁，评审版本与 hash 差异后再测试，不在构建时重新解析：

```bash
uv pip compile requirements.txt --python-version 3.12 --universal --generate-hashes --output-file requirements.lock
uv pip compile requirements.txt test-requirements.txt --constraint requirements.lock --python-version 3.12 --universal --generate-hashes --output-file test-requirements.lock
```

`fixtures/langgraph/checkpoint-1.2.9.json` 来自隔离的 LangGraph 1.2.9 环境，记录捕获时其他依赖版本。兼容测试在新基线读取该 checkpoint，并继续真实 Agent 对话；它验证库层兼容，不代表线上所有历史 checkpoint 或 MariaDB 已验证。捕获脚本为 `scripts/capture_langgraph_checkpoint.py`，只使用内存 saver、假模型与本地工具，无外部调用。

回退时将 `requirements.txt`、两份锁和相关适配恢复为同一已验证提交并重新构建，不删除历史 checkpoint；在真实数据库完成恢复兼容验收前，不执行线上升级或回退。

## Trace Evidence 安装与升级

全新 Helm 安装默认开启 `observability.evidencePublisher.enabled`。启用前需提供 Kafka brokers 和既有 Secret（默认 `bkn-trace-evidence-kafka`，非空 `username`、`password`）；标准安装器沿用平台现有 Kafka 凭据准备流程，安装 Agent 前校验 brokers、队列与重试参数的运行时范围，并只读检查最终引用。独立 Helm 安装由部署者提前准备这些引用。没有 Kafka 的部署应显式设置 `observability.evidencePublisher.enabled=false`；此时不渲染 Kafka 或准入配置，也不检查 Kafka Secret，Artifact 上传仍有自己的凭据。

标准安装器的覆盖顺序为：Chart/安装默认值 → 已安装的采集开关及队列参数 → 当前配置文件 → CLI `--set`。升级不会自动开启原来关闭的发布器；若要开启，需在持久配置文件中显式设为 true。连接地址与 Secret 引用按当前安装配置解析，不沿用旧安装器强制写入的连接默认值（包括已退役的 8080 配置接口）。自定义连接需保存在当前配置文件或通过安装环境变量/CLI 显式提供。仅保留受支持的采集开关和队列参数，不保留内联凭据或旧版内部 OAuth/签名配置。不使用全量 `--reuse-values`。

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

### Lifecycle owner attribution

For Context Loader interactions, `bkn_start_interaction` returns response-only
`owner` metadata derived from its authenticated account context after Core accepts
Start. Agent preserves that owner with the interaction for Kafka evidence and
private artifact ingress. Question and result artifact bodies also carry the
existing `application_principal_id` and `effective_subject_id` fields from that
owner, because Core persists artifact attribution from the body. Without a
lifecycle owner they retain the trace identity/account fallback used by evidence.
It does not replace caller authentication, tool headers,
or model-visible parameters. Older Context Loader responses without owner still
load tools, but log a coverage warning and cannot establish corrected evidence
attribution. Upgrade Context Loader together with Agent for this contract.
