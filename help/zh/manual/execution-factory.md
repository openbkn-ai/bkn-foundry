# 🛠️ Execution Factory

## 📖 概述

**Execution Factory** 负责注册与执行智能体可调用的**算子**、**工具**与**技能（Skill）**，在策略控制下将 LLM 规划落地为具体业务动作（HTTP 工具、代码执行、MCP 等）。

核心能力：

| 能力 | 说明 |
| --- | --- |
| 算子（Operator） | 平台内置或用户注册的可执行单元，通过统一接口调用 |
| 工具（Tool） | HTTP/MCP/代码类工具，由智能体运行时按需调用 |
| 技能包（Skill） | 可分发、可安装的能力包，包含文档与元数据 |

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/agent-operator-integration/v1` | 算子集成与工具执行面 |

### CLI

#### 算子调用

通过 `openbkn call` 直接调用已注册的算子 API：

```bash
# 调用算子 — 传入 JSON 请求体
openbkn call POST /api/agent-operator-integration/v1/operators/op_text_extract/invoke \
  -d '{"input": {"url": "https://example.com/report.pdf"}, "params": {"format": "markdown"}}'

# 列出所有已注册算子
openbkn call GET /api/agent-operator-integration/v1/operators

# 获取算子详情
openbkn call GET /api/agent-operator-integration/v1/operators/op_text_extract

# 列出工具
openbkn call GET /api/agent-operator-integration/v1/tools

# 调用工具
openbkn call POST /api/agent-operator-integration/v1/tools/tool_web_search/invoke \
  -d '{"query": "BKN Foundry 最新版本", "limit": 5}'
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

# 5. 通过算子接口调用技能
openbkn call POST /api/agent-operator-integration/v1/operators/data_quality_checker/invoke \
  -d '{"input": {"table": "orders", "rules": ["not_null", "unique"]}}'
```

---

### TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

// 列出算子（算子端点无 typed 方法 —— 用通用 passthrough）
const operators = await bkn.call('/api/agent-operator-integration/v1/operators', { method: 'GET' });
console.log('算子:', operators);

// 调用算子
const result = await bkn.call(
  '/api/agent-operator-integration/v1/operators/op-weather/invoke',
  { method: 'POST', body: { city: 'Shanghai', units: 'metric' } },
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
# 列出算子
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/operators" \
  -H "Authorization: Bearer $(openbkn token)"

# 获取算子详情
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/operators/op_text_extract" \
  -H "Authorization: Bearer $(openbkn token)"

# 调用算子
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/operators/op_text_extract/invoke" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "input": {"url": "https://example.com/report.pdf"},
    "params": {"format": "markdown"}
  }'

# 列出工具
curl -sk "https://<访问地址>/api/agent-operator-integration/v1/tools" \
  -H "Authorization: Bearer $(openbkn token)"

# 调用工具
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/tools/tool_web_search/invoke" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "BKN Foundry 最新版本",
    "limit": 5
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
