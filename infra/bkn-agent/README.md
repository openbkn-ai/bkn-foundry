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

关键环境变量见 `app/config.py`（RDS*、MF_MODEL_API_PRIVATE_BASE、BKN_AGENT_DEFAULT_TOOLBOXES、OPERATOR_INTEGRATION_BASE、CHECKPOINTER_BACKEND）。默认 toolbox 拉取失败降级告警不击穿对话；`type: "toolbox"` 显式引用失败报错。

## API

契约冻结于 `docs/api/bkn-agent/bkn-agent.yaml`（OpenAPI 3.1，#212）。改 API 走 spec 先行：
先改实现里的路由/模型并跑 `python scripts/export_openapi.py` 重新导出，
`app/test/test_contract.py` 强制 spec 与实现一致。

`/api/bkn-agent/v1/`：agents CRUD、`POST /chat`（SSE）、`POST /run` + `GET /tasks/{id}`、
`POST /invoke/{agent_id}`（同步一次性，算子工厂 toolbox 回调）、`GET /threads/{id}`（会话历史）、
提示词管理与调用方覆写（/prompts、/agents/{id}/prompt）、导入导出（/export、/import：
agent 定义+prompt 当前版本，保留原 id upsert 幂等，同名不同 id 记 failed 不中断，
跨环境引用缺失记 warning）。

## 内置 agent 预置

平台内置 agent 的定义随代码走，启动时幂等写库（`app/bootstrap/preset_sync.py`，
包在 `app/bootstrap/presets/*.yaml`）。目前有 Vega 语义理解的两个：
`resource-semantic-understanding`、`catalog-semantic-understanding`——
id 被 vega-backend 硬编码引用，改名即断。

文件形态 = `/export` 响应里的 items 列表，提示词用 `|-` 块标量，便于评审与回滚。
维护方式：在环境里调好 → `POST /export` → 把 items 粘回 YAML。段落保持单行、与库内
内容逐字一致，否则每次启动都会多发一个提示词版本。

写入复用 `/import` 的按条 upsert（`app/core/impex.py`），但不查归属（内置 agent 是
平台资产，存量环境里可能由工程师手工建过）。`model` 与 `limits` 归环境管：已存在的
agent 保留库内现值，包里 `model` 恒为空即走系统默认大模型。预置失败只记 ERROR
日志，不阻断启动。

## 算子工厂注册

published 状态的 agent 自动注册进算子工厂 toolbox（`app/bootstrap/toolbox_sync.py`，
ToolDependencySync 同款机制）：启动时全量 upsert（指数退避直到成功），agent 增删改后
异步重同步。upsert 为整包替换，取消发布/删除自动下架。开关 `BKN_AGENT_TOOLBOX_SYNC`。
