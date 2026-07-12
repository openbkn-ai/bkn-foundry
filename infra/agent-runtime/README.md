# agent-runtime

平台内置 Agent 运行时（Epic #202，设计文档：`docs/design/agent-runtime/features/202-agent-runtime.md`）。

**仅面向平台内部**：调用主体为平台模块（服务身份）与内部工程师，终端用户流量不可直达（硬约束）。

- 引擎：LangGraph（Python）
- 模型：mf-model-api（OpenAI 兼容，集群内 `/api/private`，model 为空走系统默认）
- 工具：MCP（agent-retrieval 内置工具集 / 算子工厂 toolbox）
- 技能：capabilities-lab（`/skill/content` 注入 + `/skill/files/read` 渐进读取）
- 存储：共享 `openbkn` 库，`agent_` 前缀表，迁移见 `migrations/agent-runtime/`

## 本地运行

```bash
pip install -r requirements.txt
uvicorn main:app --port 30800
```

关键环境变量见 `app/config.py`（RDS*、MF_MODEL_API_PRIVATE_BASE、AGENT_RETRIEVAL_MCP_URL、CAPABILITIES_LAB_BASE、CHECKPOINTER_BACKEND）。

## API

`/api/agent-runtime/v1/`：agents CRUD、`POST /chat`（SSE）。任务面（/run、/tasks）与提示词管理面（/prompts）随 M3/M4 落地（issue #208 / #209）。
