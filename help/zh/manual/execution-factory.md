# 🛠️ Execution Factory

## 📖 概述

**Execution Factory** 负责注册与执行智能体可调用的**工具箱工具**、**MCP** 与**技能（Skill）**，在策略控制下将 LLM 规划落地为具体业务动作（HTTP 工具、代码执行、MCP 等）。

核心能力：

| 能力 | 说明 |
| --- | --- |
| 工具箱与工具（ToolBox / Tool） | 工具箱把一组工具打包发布：`openapi` 工具箱把工具代理到外部服务，`function` 工具箱承载平台函数（Python 函数或 Cypher 函数）；工具由智能体运行时按需调用 |
| MCP | 接入的 MCP Server 及其工具 |
| 技能包（Skill） | 可分发、可安装的能力包，包含文档与元数据 |

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/agent-operator-integration/v1` | 工具箱、MCP、Skill 管理与工具执行面 |

### Cypher 函数

函数工具箱里的函数有两种脚本类型：`python`（在沙箱中运行代码）和 `cypher`。Cypher 函数的实现就是一条只读 Cypher 语句，语句里的 `$name` 就是函数入参；它不进沙箱，由平台以调用人的身份在知识网络中执行，行列权限照常生效。它可以挂成对象类的逻辑属性，也可以像其他工具一样被智能体、MCP 调用。

| 字段 | 说明 |
| --- | --- |
| `script_type` | `cypher` |
| `kn_id` | 语句查询的知识网络 |
| `code` | 只读 Cypher 语句，语法见 [Cypher 查询手册](cypher.md) |
| `inputs` | 与语句中的 `$name` 一一对应；类型为 `string`、`number`、`boolean`，或它们的 `array`（用于 `IN $name`）；都必填，都要写说明 |
| `result_shape` | `value`：返回 `{"value": x}`，只能一列标量，0 行为 `null`，多于 1 行报错；`table`：返回 `{columns, entries, limited}` |

保存前平台会让知识网络编译一次语句（不读数据）：语法错误、写操作、对象类或属性不存在、没有读取权限，以及 `value` 形状返回多列或节点、关系、列表时，都拒绝保存并给出原因。

```bash
# 在函数工具箱 <box_id> 中新建一个 Cypher 函数：某供应商供应的物料数
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/tool-box/<box_id>/tool" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "metadata_type": "function",
    "function_input": {
      "name": "supplier_material_count",
      "description": "供应商供应的物料数",
      "script_type": "cypher",
      "kn_id": "<kn_id>",
      "code": "MATCH (s:supplier {supplier_id: $id})-[:supplies]->(m:material) RETURN count(m)",
      "inputs": [{"name": "id", "type": "string", "required": true, "description": "供应商的 supplier_id"}],
      "result_shape": "value"
    }
  }'

# 启用后调用（工具箱需已发布）：返回 {"status_code": 200, "body": {"value": 12}, ...}
curl -sk -X POST "https://<访问地址>/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"body": {"id": "S001"}}'
```

用作逻辑属性时：

- 起点按主键匹配。单主键的对象类用 `$id`；联合主键每个主键一个同名参数，如 `$order_id`、`$line_no`。参数绑定到对应主键属性。
- 选 `value` 形状，`ResultPath` 写 `$.value`。
- 逻辑属性的值按查看人的权限计算：知识网络代理只负责校验这个逻辑属性能否调用该函数，语句以查看人身份执行，行权限和属性权限照常生效，查看人看不到的数据不会算进结果。每个实例执行一次查询，纯数值汇总优先用对象指标。

注意：

- 编辑时不传 `script_type` 就沿用函数原来的类型。脚本类型不能在 `python` 与 `cypher` 之间修改，要换类型就新建函数。
- 语句只能替换值，不能替换对象类、关系类和属性名；参数不接受 `null`，需要「不填就不过滤」时建两个函数。
- 导入工具箱时不校验语句（目标环境可能还没有这个网络），语句失效会在调用时报错。
- `LIMIT`、`SKIP` 后面可以写整数，也可以写 `$参数`（类型为 `number`，调用时须传非负整数），用来做“前 N 条”或分页。`table` 形状不写 `LIMIT` 时最多返回 1000 行，超出时 `limited` 为 `true`；自己写了 `LIMIT` 时按它截断，`limited` 不再提示。`LIMIT` 最大 10000。
- `value` 形状用 `LIMIT 1` 取一行时要配 `ORDER BY`，否则取到哪一行不确定；能聚合（`count()`、`max()` 等）时优先聚合。
- 数字有两种返回形式：decimal 属性的值和对它的 `sum()` 以字符串返回（如 `"13833141.000000"`），保留数据源的精度；计算出的值（如 `sum(a) * 100.0 / sum(b)` 这样的比值）以数字返回（如 `72.8975147437592`）。调用方比较或计算前要把两种形式都转成数字。
- 一条语句在数据源上最多执行 55 秒（bkn-backend 配置 `cypherQueryTimeoutSec`），调用时的 `timeout` 只决定调用方等多久，不能延长这个上限。多跳汇总在全量数据上容易超时，例如对所有产品展开 3 跳 BOM：不加条件会超时，用 `STARTS WITH $prefix`、主键等条件缩小起点后能在时限内返回。保存前先用典型参数试算，看耗时。

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
