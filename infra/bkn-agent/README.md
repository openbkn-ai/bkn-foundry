# bkn-agent

平台内置 Agent 运行时（Epic #202，设计文档：`docs/design/bkn-agent/features/202-bkn-agent.md`）。

**仅面向平台内部**：调用主体为平台模块（服务身份）与内部工程师，终端用户流量不可直达（硬约束）。

- 引擎：LangGraph（Python）
- 模型：mf-model-api（OpenAI 兼容，集群内 `/api/private`，model 为空走系统默认）
- 工具：MCP（agent-retrieval 内置工具集 / 算子工厂 toolbox）
- 技能：capabilities-lab（`/skill/content` 注入 + `/skill/files/read` 渐进读取）
- 存储：共享 `openbkn` 库，`agent_` 前缀表，迁移见 `migrations/bkn-agent/`

## 本地运行

```bash
pip install -r requirements.txt
uvicorn main:app --port 30800
```

关键环境变量见 `app/config.py`（RDS*、MF_MODEL_API_PRIVATE_BASE、AGENT_RETRIEVAL_MCP_URL、CAPABILITIES_LAB_BASE、CHECKPOINTER_BACKEND）。

## API

契约冻结于 `docs/api/bkn-agent.yaml`（OpenAPI 3.1，#212）。改 API 走 spec 先行：
先改实现里的路由/模型并跑 `python scripts/export_openapi.py` 重新导出，
`app/test/test_contract.py` 强制 spec 与实现一致。

`/api/bkn-agent/v1/`：agents CRUD、`POST /chat`（SSE）、`POST /run` + `GET /tasks/{id}`、
`POST /invoke/{agent_id}`（同步一次性，算子工厂 toolbox 回调）、`GET /threads/{id}`（会话历史）、
提示词管理与调用方覆写（/prompts、/agents/{id}/prompt）。

## 算子工厂注册

published 状态的 agent 自动注册进算子工厂 toolbox（`app/bootstrap/toolbox_sync.py`，
ToolDependencySync 同款机制）：启动时全量 upsert（指数退避直到成功），agent 增删改后
异步重同步。upsert 为整包替换，取消发布/删除自动下架。开关 `BKN_AGENT_TOOLBOX_SYNC`。
