# Skill 包模板：让 Agent 按声明调用指标、函数与行动

本目录是一份可直接下载使用的模板，回答两个问题：

1. 一个 Skill 如何声明它要用到的知识网络能力（指标、函数、行动、MCP 工具），让 Agent 读完 SKILL.md 就知道调什么、怎么调、结果怎么读、哪一步要先问用户。
2. 一个函数（Function）如何在平台沙箱里通过 bkn-osdk 读取知识网络数据，并注册为可被 Agent 调用的工具。

平台侧不需要任何新接口。Agent 已经拥有 `get_skill_content`、`search_capabilities`、`get_kn_detail`、`query_metric`、`execute_tool`、`execute_action` 这组工具；Skill 只负责把「用哪些、怎么用」写清楚。

## 目录

| 路径 | 内容 |
| --- | --- |
| [`skill/SKILL.md`](skill/SKILL.md) | Skill 包主文档模板，占位符用 `{{...}}` 标出 |
| [`skill/references/report-spec.md`](skill/references/report-spec.md) | 报告格式模板 |
| [`functions/function.py.template`](functions/function.py.template) | 沙箱函数骨架：`handler(event)` + bkn-osdk 读数 |
| [`examples/demand-deliverability-assessment/`](examples/demand-deliverability-assessment/) | 完整样例：需求可交付性评估 Skill，在测试环境实跑通过 |
| [`examples/functions/l1_kitting_check.py`](examples/functions/l1_kitting_check.py) | 完整样例：一级 BOM 齐套检查函数，通过 bkn-osdk 读 BOM 与库存 |
| [`examples/functions/call_l1_check.py`](examples/functions/call_l1_check.py) | 完整样例：函数调函数，通过 `kn.execute_tool` 以同一身份、同一受管会话调用上一个函数 |

## 一、Skill 包结构

```
{skill_dir}/
├── SKILL.md                 # 必需。frontmatter 至少含 name、description
└── references/              # 可选。报告格式、业务规则、输入输出契约
    └── report-spec.md
```

执行工厂只解析 frontmatter 的 `name`、`description`、`metadata`（自由结构，原样保存）。正文不做结构校验，因此「能力依赖」表的写法是约定，不是 schema。

## 二、能力依赖怎么声明

### frontmatter：机器可读的清单

```yaml
metadata:
  bkn_scope: {{kn_id}}
  uses:
    - type: metric      # metric | function | action | mcp_tool
      name: 库存可用量   # 与知识网络 / 执行工厂中的登记名完全一致
      purpose: 可用库存总口径
    - type: function
      name: 要X套净需求与齐套
      purpose: 齐套判断与建议补货量
    - type: action
      name: 模拟确认补货计划
      purpose: 缺料时生成补货计划回执
      confirm: true     # 执行前必须取得用户明确确认
```

### 正文：Agent 照着调的「能力依赖」表

| 名称 | 类型 | 用途 | 调用方式 | 结果怎么读 | 需确认 |
|------|------|------|----------|------------|--------|
| 库存可用量 | 指标 | 可用库存总口径 | `query_metric`，`time.instant=true` | `datas[0].values[0]` | 否 |
| 要X套净需求与齐套 | 函数 | 齐套判断 | `execute_tool`，参数 `product`、`qty`、`substitute_enabled` | `kitting_ok`、`gaps[]` | 否 |
| 模拟确认补货计划 | 行动 | 生成补货计划回执 | 先 `get_action_info`，再 `execute_action` | 回执 `simulation=true` | **是** |

写法要点：

- **名称就是查找键。** Agent 用 `search_capabilities` 按名称在当前知识网络内找函数与 MCP 工具，用 `get_kn_detail` / `get_object_types` 找指标与行动类型。名称写错，Agent 就找不到。
- **只声明本知识网络已挂载的能力。** 未挂载的能力搜不到，Agent 应如实告知「未挂载」，不得换用其他网络里的同名能力。
- **需要人工确认的行动标 `confirm: true`。** 平台不做两阶段拦截，靠 Skill 正文约束 Agent「未得到肯定答复不得执行」。
- **找 Skill 时带 `types: ["skill"]`。** `search_capabilities` 带 query 时函数会占满前几条，不收窄类型 Skill 会被挤出。
- **文本字段的定位用 `match`。** 建了全文索引的字段（`condition_operations` 含 `match`）用 `==` 可能被资源层拒绝，Skill 正文里写明。

## 三、函数怎么写（bkn-osdk）

### 运行环境

平台沙箱预装 `bkn_osdk` 与 `sandbox_sdk`。通过 `execute_tool` 或行动触发时，执行工厂向沙箱注入：

| 环境变量 | 含义 |
| --- | --- |
| `BKN_BASE_URL` | 集群内平台地址，bkn-osdk 据此回访 BKN |
| `BKN_TOKEN` | 调用者凭据，函数以调用者身份读数 |
| `BKN_CONVERSATION_ID` / `BKN_INTERACTION_ID` | 当前受管交互，函数内的每次读数都挂到同一条证据链 |
| `BKN_PARENT_OPERATION_ID` | 触发本次执行的操作，嵌套读数记为它的子操作 |

因此函数代码里**不写 URL、不写 Token、不传 bkn_context**，直接 `from bkn_osdk import kn` 调用。

### 三条硬约束

1. 入口函数必须叫 `handler(event)`，`event` 就是工具入参字典。
2. 代码前面会被拼接一段包装，**不能写 `from __future__ import ...`**。
3. 工具参数 `type` 只收 `string` / `number` / `boolean` / `array` / `object`，写 `integer` 直接 400。

### 骨架

见 [`functions/function.py.template`](functions/function.py.template)。分页读取、按属性过滤、返回结构化结果三段都在里面；完整可跑的版本见 [`examples/functions/l1_kitting_check.py`](examples/functions/l1_kitting_check.py)。

### 函数调函数

一个函数可以通过平台调用另一个已挂载到同一知识网络的函数：

```python
from bkn_osdk import kn

def handler(event):
    answer = kn.execute_tool(event["kn_id"], event["box_id"], event["tool_id"],
                             {"kn_id": event["kn_id"], "product": event["product"], "qty": 50})
    body = answer.get("body", answer)          # 被调函数的原始响应
    if body.get("exit_code") not in (0, None): # HTTP 200 不等于被调函数成功
        return {"ok": False, "reason": body.get("stderr", "")[-300:]}
    return {"ok": True, **(body.get("result") or {})}
```

- `kn.execute_tool` 自动带上当前沙箱的受管会话，被调函数的沙箱因此拿到同一份调用者凭据；trace 里被调函数挂在本函数 `execute_tool` 操作之下，再往下是它自己的读数。
- 前提：沙箱预装的 bkn-osdk 含 `kn.execute_tool`（bkn-sdk #100 起）。更早的版本只能 `bkn_osdk.call("/api/agent-retrieval/v1/kn/execute_tool", ...)` 裸打，并且**必须**把 `BKN_CONVERSATION_ID` / `BKN_INTERACTION_ID` / `BKN_PARENT_OPERATION_ID` 拼成 `bkn_context` 放进请求体；漏掉的话被调函数的沙箱不会注入任何凭据。
- 被调函数按自己的挂载与权限独立生效；平台不做调用深度与循环检测，别写互相调用的函数。

完整样例见 [`examples/functions/call_l1_check.py`](examples/functions/call_l1_check.py)。

### 从代码到工具（openbkn CLI 0.1.5+）

```bash
# 1. 先在沙箱跑通；--pass-token 让代码以你的身份回访 BKN
openbkn function run ./l1_kitting_check.py \
  --event '{"kn_id":"<kn_id>","product":"382-000005","qty":50}' --pass-token

# 2. 建函数类工具箱（名称只允许中文、字母、数字、下划线）
openbkn toolbox create --name skill_template_demo --type function

# 3. 把代码注册成工具；inputs 的 type 不能写 integer
openbkn tool create ./l1_kitting_check.py --toolbox <box-id> --name l1_kitting_check \
  --description "一级 BOM 齐套检查" \
  --inputs '[{"name":"kn_id","type":"string","required":true},
             {"name":"product","type":"string","required":true},
             {"name":"qty","type":"number"}]' \
  --outputs '[{"name":"kitting_ok","type":"boolean"},{"name":"gaps","type":"array"}]'

# 4. 启用（默认 disabled，这一步是硬门）并发布工具箱
openbkn tool enable <tool-id> --toolbox <box-id>
openbkn toolbox publish <box-id>

# 5. 挂到知识网络（CLI 子命令待 bkn-sdk #90，先走接口或 Studio「函数」页）
openbkn call -X POST /api/bkn-backend/v1/knowledge-networks/<kn_id>/capabilities \
  -d '{"capabilities":[{"capability_type":"function","box_id":"<box-id>","capability_id":"<tool-id>"}]}'
```

对应的 REST 接口（不依赖 CLI 版本）：

| 步骤 | 接口 |
| --- | --- |
| 试跑 | `POST /api/agent-operator-integration/v1/function/execute`，body `{code, event, bkn_token, timeout}` |
| 建箱 | `POST /api/agent-operator-integration/v1/tool-box`，body `{metadata_type:"function", box_name, box_desc, source:"custom"}` |
| 建工具 | `POST /api/agent-operator-integration/v1/tool-box/{box_id}/tool`，body `{metadata_type:"function", function_input:{name, description, script_type:"python", code, inputs, outputs}, use_rule}` |
| 启用 | `POST /api/agent-operator-integration/v1/tool-box/{box_id}/tools/status`，body `[{tool_id, status:"enabled"}]` |
| 发布 | `POST /api/agent-operator-integration/v1/tool-box/{box_id}/status`，body `{status:"published"}` |
| 挂载 | `POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/capabilities` |

## 四、Skill 注册与挂载

```bash
openbkn skill register ./{skill_dir}            # 返回 skill_id，状态 unpublish
openbkn skill set-status <skill_id> published
openbkn call -X POST /api/bkn-backend/v1/knowledge-networks/<kn_id>/capabilities \
  -d '{"capabilities":[{"capability_type":"skill","capability_id":"<skill_id>"}]}'
```

挂载后，Agent 在该知识网络内 `search_capabilities`（`types: ["skill"]`）即可找到，`get_skill_content` 读取正文。

## 五、验收怎么看

一次完整的受管交互里应当能看到：`get_skill_content` → `query_metric` → `execute_tool` → `get_action_info` → `execute_action` 依次落在同一个 `interaction_id` 下，`bkn_finish_interaction` 返回 `evidence_status: complete`。函数内部通过 bkn-osdk 发起的读数会作为 `execute_tool` 的子操作出现在同一条链上。

样例目录中的 Skill 与函数均在 0.1.5 测试环境上按此流程实跑通过。
