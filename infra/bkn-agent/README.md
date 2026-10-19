# bkn-agent

平台内置 Agent 运行时（Epic #202，设计文档：`docs/design/bkn-agent/features/202-bkn-agent.md`）。

旧管理与服务调用接口继续面向平台内部。0.2.1 新增经过独立凭据校验的用户执行接口；统一网关只可开放这一组路由，不能把整个服务或旧接口公开。Chart 不自动创建 Ingress。

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

## 上下文预算（公开执行面）

公开执行（`/executions`）按 Agent 合并后的 `limits` 控制每次模型调用看到的内容，只改发给模型的副本，保存的会话保留完整工具结果。未设置时取以下默认值（`app/core/agent_settings.py`）：

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `data_result_chars` | 12000（原 8000） | 单个数据工具结果的字符上限，超出部分截断并提示模型少取列、不要分页 |
| `schema_result_chars` | 24000 | 单个结构/发现类工具结果的字符上限 |
| `keep_tool_results` | 3 | 当前问题至少保留最近 3 个工具结果；在 `keep_tool_results × data_result_chars` 的预算内继续保留更早的结果（原先只按条数保留 3 个） |
| `summarize_after_tokens` | 60000 | 会话超过该 token 数后，把之前的问题压缩成摘要；当前问题不参与压缩 |

被移出的结果换成占位符，写明原调用和大小。同一问题内重复的只读调用（`run_cypher`、`query_object_instance` 等）直接复用之前的成功结果，不再请求后端。

## 算子工厂注册

不再有。published agent 曾被自动注册进算子工厂 toolbox（#212），现已移除：
agent 只通过本服务自身的 `/api/bkn-agent/v1` 面对外，不再在执行工厂里留一份工具描述。
把某个 agent 挂给另一个 agent 用 `tool_refs` 里的 `type: agent`，不经过工厂。

## 内置 Agent 默认数据（#2170）

内置 Agent 使用固定 ID，配置、共享 Prompt 和 Prompt 历史保存在现有数据库表中。默认数据统一由 `core-data-migrator` 安装，不使用 JSON 配置包、额外初始化表或应用启动 Job；`deploy/` 目录已移除。管理员通过既有管理 API 修改共享配置，初始化不覆盖已有 Agent。

0.2.1 的首装和升级路径：

- 首次安装执行 `migrations/bkn-agent/mariadb/0.2.1/init.sql`，保留原十张表和语义理解定义，并添加 Studio 智能问答及两个业务溯源 Agent。
- 升级依次执行 `01-seed-studio-smart-qa.sql`、`02-seed-business-provenance.sql` 和 `03-upgrade-business-provenance-defaults.sql`。前两条只写缺失默认数据；第三条补齐直接从旧 EE 版本升级时遗漏的官方默认修复，不新增或修改表结构。
- 已有固定 ID 的 Agent 保留模型、工具、状态、Prompt 引用和历史。业务溯源优化 Agent 仅在原管理主体仍是最后修改者、完整 limits 等于旧默认值时，将 180 秒升级到 600 秒；仅对精确匹配 0.1.4/0.1.5 已发布默认内容、由原主体维护且无自定义变量的 Prompt 追加新版本。管理员编辑、回滚和冲突版本保留。修改共享 Prompt 使用原版本管理能力；个人覆写表和旧 API 不在本次删除。
- 新 Agent 最后写入，兼容的未完成默认 Prompt 可在脚本重试时补全；Prompt 冲突或名称冲突报错，不接管客户配置。迁移器按语句提交，不承诺跨表事务回滚，冲突时可能保留尚未引用的默认 Prompt 行。

Studio 智能问答固定 ID 为 `studio_smart_qa`，初始为 draft，模型留空继承平台默认，声明完整 Context Loader MCP。`runtime.contextLoaderMcpUrl` / `CONTEXT_LOADER_MCP_URL` 继续选择完整版 `/mcp/` 或紧凑版 `/mcp-compact/`。公共用户入口、网络范围及权限检查见下文 #2173；不创建基础数据问答或图探索占位 Agent。

业务溯源两个 Agent 保留原固定 ID、owner 和工具声明，缺失时安装当前默认 Prompt/限制；旧默认值仅按上述条件修复。SQL 登记不创建 EE 管理身份，也不开放许可证控制的 EE 入口。`python -m app.bootstrap.business_provenance` 保留为现有部署脚本的兼容命令，只验证管理主体、Agent 归属及共享 Prompt，不再调用 `/import` 或覆盖管理员修改。原 JSON 文件覆盖环境变量不再支持，显式设置时会报错；共享配置改为通过管理 API 维护。旧手工导入脚本 `scripts/import-business-provenance-optimizer.sh` 一并移除。

迁移脚本执行、生产配置与部署仍需 Owner 确认。本轮未连接真实数据库执行迁移。若测试环境已经运行旧版 PR 的 0.2.1 三表脚本，不自动 DROP 表或伪造迁移记录；迁移器不会重跑已安装版本的新编号脚本，应单独核对该环境的版本和脚本进度后安排补充迁移。

完整设计维护于 [bkn-docs 的简化方案](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn-agent/design/issue-2151-builtin-agent-registration.md)。

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


## 用户执行接口（#2173）

`POST /api/bkn-agent/v1/executions` 返回 SSE；`GET /api/bkn-agent/v1/executions/{run_id}` 读取运行状态与已提交结果；`POST /api/bkn-agent/v1/executions/{run_id}/cancel` 请求停止。三者均要求 `Authorization: Bearer ...`，OAuth 通过 Hydra 校验，`bak_` 通过 bkn-safe 校验，外部账号头不作为身份依据。依赖地址通过 `runtime.hydraAdminBase` / `HYDRA_ADMIN_BASE` 和 `runtime.bknSafeBase` / `BKN_SAFE_BASE` 配置，向下游继续传递原调用凭据。

首期仅接入 `studio_smart_qa`。调用前须由管理员完成共享配置并发布，且调用者具有该 Agent 的 `use` 和知识网络的 `view_detail` 权限；实际数据与行动权限继续由下游校验。本接口不自动发布、授予权限或修改默认配置。

```json
{
  "agent_id": "studio_smart_qa",
  "message": "分析当前网络中的订单",
  "client_request_id": "request-unique-01",
  "context": {"kn_id": "network-01"}
}
```

后续轮次附带首轮 `meta` 中的 `thread_id`；切换网络创建新线程。请求不能覆盖模型、工具、Skill 或 Prompt。新入口始终使用共享 Prompt，旧接口的个人覆写和请求覆写保持兼容。

SSE 为 `meta/token/tool_started/tool_finished/usage/result/terminal`，携带 `run_id` 和单调序号。工具事件关联真实调用 ID，参数/结果按大小限制并脱敏。结果先写入原 `t_agent_task` 再发送成功终态；GET 可恢复已提交结果。缺少真实用量时显式返回不可用。接口详情与错误以 [OpenAPI](../../docs/api/bkn-agent/bkn-agent.yaml) 为准。

完整 Context Loader MCP 继续按已有 full/compact 端点注册。执行时校验网络范围；无可信网络约束的工具（例如任意代码执行、跨 Agent 调用或其他未适配类型）明确不可用，不静默放行。此交付不代表图探索工具、画布结果或前端迁移已完成。

相同账号类型、账号 ID 和 `client_request_id` 不重复执行；相同 ID 不同输入或活动运行返回409，终态重试返回已存结果。停止接口返回实际已提交状态，可能需要随后 GET 等待取消完成。断线停止执行，进程中断沿用旧任务恢复机制标记失败；不承诺 SSE 重放、断线后台续跑或多副本协调，Chart 的单副本限制保持原样。不增加数据库表或列。
