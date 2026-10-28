# 🛠️ Execution Factory

## 📖 概述

**Execution Factory** 负责注册与执行智能体可调用的**工具箱工具**、**MCP** 与**技能（Skill）**，在策略控制下将 LLM 规划落地为具体业务动作（HTTP 工具、代码执行、MCP 等）。

核心能力：

| 能力 | 说明 |
| --- | --- |
| 工具箱与工具（ToolBox / Tool） | 工具箱把一组工具打包发布：`openapi` 工具箱把工具代理到外部服务，`function` 工具箱承载平台函数；工具由智能体运行时按需调用 |
| MCP | 接入的 MCP Server 及其工具 |
| 技能包（Skill） | 可分发、可安装的能力包，包含文档与元数据 |

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/agent-operator-integration/v1` | 工具箱、MCP、Skill 管理与工具执行面 |

### CLI

#### 工具箱与工具

工具需处于启用状态、且所在工具箱已发布，才能被执行。

```bash
# 列出工具箱（ID 字段为 box_id）
openbkn toolbox list

# 列出工具箱内的工具
openbkn tool list --toolbox <box_id>

# 查看单个工具：元数据、参数、使用规则
openbkn tool get <tool_id> --toolbox <box_id>

# 执行已发布工具箱中已启用的工具
openbkn tool execute <tool_id> --toolbox <box_id> \
  --body '{"query": "BKN Foundry 最新版本", "limit": 5}'
```

也可以通过 `openbkn call` 按路径直接调用 Execution Factory API：

```bash
openbkn call GET /api/agent-operator-integration/v1/tool-box/list
```

#### 技能包管理

```bash
# 列出已安装的技能包
openbkn skill list

# 浏览技能市场
openbkn skill market

# 注册新技能包（上传 ZIP）
openbkn skill register --zip-file ./my-skill-v1.0.zip

# 查看技能包目录结构
openbkn skill content my-skill

# 读取技能包中的指定文件（渐进式读取）
openbkn skill read-file my-skill SKILL.md
openbkn skill read-file my-skill src/main.py

# 从市场安装技能包
openbkn skill install my-skill
openbkn skill install my-skill --version 1.2.0
```

#### 端到端流程

```bash
# 1. 浏览市场，寻找合适的技能包
openbkn skill market

# 2. 安装技能包
openbkn skill install data-quality-checker

# 3. 确认已安装
openbkn skill list

# 4. 查看技能说明
openbkn skill read-file data-quality-checker SKILL.md
```

---

### TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

// 列出工具箱（通用 passthrough）
const toolboxes = await bkn.call('/api/agent-operator-integration/v1/tool-box/list', { method: 'GET' });
console.log('工具箱:', toolboxes);

// 执行已发布工具箱中的工具
const result = await bkn.call(
  '/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>',
  { method: 'POST', body: { body: { city: 'Shanghai', units: 'metric' } } },
);
console.log('结果:', result);

// 列出 Skill
const skills = await bkn.skills.list();
console.log('skills:', skills);

// 市场搜索
const marketResults = await bkn.skills.market({ query: '数据分析' });
console.log('market:', marketResults);

// 从本地目录注册 Skill
const registered = await bkn.skills.register('./my-skill');
console.log('registered:', registered);

// 获取 Skill 内容
const content = await bkn.skills.content('skill-kg-analyzer');
console.log('content:', content);

// 读取 Skill 内某个文件
const fileContent = await bkn.skills.readFile('skill-kg-analyzer', 'SKILL.md');
console.log('file:', fileContent);

// 从市场下载并安装到目标目录
await bkn.skills.install('skill-kg-analyzer', './installed/skill-kg-analyzer');
```

---

### curl

```bash
# 列出工具箱
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/tool-box/list" \
  -H "Authorization: Bearer $(openbkn token)"

# 获取工具箱详情
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/tool-box/<box_id>" \
  -H "Authorization: Bearer $(openbkn token)"

# 列出工具箱内的工具
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/tool-box/<box_id>/tools/list?page=1&page_size=10" \
  -H "Authorization: Bearer $(openbkn token)"

# 执行工具
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "body": {"query": "BKN Foundry 最新版本", "limit": 5}
  }'

# 列出已安装技能包
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/skills" \
  -H "Authorization: Bearer $(openbkn token)"

# 注册技能包
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/skills" \
  -H "Authorization: Bearer $(openbkn token)" \
  -F "file=@./my-skill-v1.0.zip"

# 从市场安装技能包
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/skills/install" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "data-quality-checker",
    "version": "1.2.0"
  }'
```
