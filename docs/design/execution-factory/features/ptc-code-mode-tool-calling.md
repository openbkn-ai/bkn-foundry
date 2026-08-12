# 代码化工具调用：把 BKN 能力从工具面收进沙箱

- Issue：待建
- 分支：`feature/ptc-code-mode-tool-calling`
- 模块：execution-factory（operator-integration）、infra/sandbox、context-loader（agent-retrieval，仅作为 schema 来源）、bkn-agent
- 状态：draft

## 1. 背景与问题

Agent 取用 BKN 能力的现状是「每个能力一个工具」：context-loader 的 21 个工具经执行工厂
toolbox 暴露给 bkn-agent，模型逐个调用，每次调用的完整返回进入对话上下文。

这条路径有四项代价，其中前两项是主要的。

**中间结果全量进上下文。** 典型链路「列知识网络 → 取网络详情 → 展开对象类 → 查实例」，
四步的返回全部落在上下文里，而模型真正需要的往往只是最后一步结果中的若干行。
`get_kn_detail` 在引入 `detail_level` 之前单次返回可达 143K token，是这一问题最直接的量级参考。

**数据加工发生在模型推理里。** 过滤、计数、排序、按 ID 关联两个结果集，这些操作目前依赖模型
在几百行 JSON 上心算完成。既消耗输出 token，也是错误的主要来源——尤其是聚合与关联。

**往返次数等于步骤数。** 每一步都是一次完整请求，对话历史逐轮重发，延迟线性累加。

**工具集变动使前缀缓存整体失效。** `tools` 数组渲染在提示词最前端，新增或调整任一工具都会
让全部缓存作废。当前每引入一个 BKN 工具就触发一次。

需要澄清一个常见误解：工具结果本身是可缓存的（追加在 messages 末尾，前缀不变，下一轮按
cache read 计价）。缓存降低的是**成本**，不改变**上下文占用**。100K token 的工具结果即便
以十分之一单价读取，仍然占据 100K 窗口、稀释注意力、推高压缩频率。本方案针对的是后者。

## 2. 方案总览

顶层工具面收敛为一个 `run_code`；21 个 BKN 能力下沉为沙箱内的 Python 函数，由 codegen
从既有 JSON Schema 生成；沙箱以标准 MCP 客户端身份回访 context-loader 的对外 MCP 端点。

```text
bkn-agent
  └─ run_code(code)                      ← 顶层工具面只此一个
       └─ 执行工厂 /function/execute      ← 下发受限凭据，放行定向出口
            └─ 沙箱控制面 execute-sync
                 └─ 沙箱容器：模型编写的脚本
                      └─ _tools.py（codegen 产物，内封 MCP 客户端）
                           └─ context-loader /api/agent-retrieval/v1/mcp
```

仅脚本的 stdout 返回模型，中间结果留在沙箱内。

**回访走对外 MCP 端点，不走 `/in` 内部接口。** 这是本方案的安全基点，理由见 3.3。

## 3. 详细设计

### 3.1 codegen：构建期脚本

输入为 `adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas/` 下的 21 个
工具 schema（排除 `tools_meta.json` 与 `locales/`），输出两份产物：

| 产物 | 用途 | 消费方 |
|---|---|---|
| `_tools.py` | 沙箱内可调用的函数 | 模型编写的脚本 |
| `tool_digest.md` | 函数签名清单与调用顺序说明 | `run_code` 的 tool description |

映射规则：

| Schema 元素 | 生成结果 |
|---|---|
| 文件名 `<tool>.json` | 函数名 `<tool>`、MCP `tools/call` 的 `name` |
| `required` 中的属性 | 必填位置参数 |
| 其余属性 | 带 `default` 的关键字参数 |
| `type` | Python 类型注解 |
| `description` | 函数 docstring |

改走 MCP 后，工具名即 MCP 工具名，不再需要路由映射表——此前 `/in` 方案中
`get_logic_properties_values` 对应 `/kn/logic-property-resolver` 的例外情况自然消失。
这是 MCP 相较 `/in` 的一项附带收益：MCP 的工具名与 schema 文件名同源，无第二套命名。

CI 增加一步校验：重跑 codegen 后 `git diff` 必须为空，防止 schema 变更后忘记重新生成。
`_tools.py` 与 `.pb.go` 同性质，属编译产物，不接受手工编辑。

### 3.2 沙箱运行时

`_tools.py` 内封一个 MCP 客户端，所有函数收敛到统一的 `_call`。**MCP 会话在模块级复用**，
一次 `run_code` 内的多次工具调用共用同一个 session，`initialize` 握手只发生一次：

```python
async def _get_session() -> ClientSession:
    global _stack, _session
    if _session is None:
        _stack = contextlib.AsyncExitStack()
        http_client = await _stack.enter_async_context(
            create_mcp_http_client(headers={"Authorization": f"Bearer {_TOKEN}"}))
        read, write = await _stack.enter_async_context(
            streamable_http_client(_MCP_URL, http_client=http_client))
        _session = await _stack.enter_async_context(ClientSession(read, write))
        await _session.initialize()
    return _session
```

MCP 的两项形式代价——握手往返与返回值需从 text content 反解——都在 stub 内封掉，
模型侧看到的仍是普通同步函数调用。

错误必须结构化回传。服务端 message 原样带出，使 traceback 进入 stdout 后模型能据此修正参数——
这个修正循环在同一次 `run_code` 内即可完成，无需回到模型，是本方案相对逐个工具调用的核心增益之一。
P0 实测中，一次因下游服务不可用而失败的调用，其完整服务端报文（含具体服务与端口）原样出现在
`ToolError` 里，模型据此可区分「参数写错」与「环境故障」，而不是笼统重试。

`_MCP_URL` 与 `_TOKEN` 经沙箱执行接口的 `env_vars` 注入；`_tools.py` 与 mcp SDK 烤进沙箱
基础镜像，避免每次执行重复安装与写入。

#### mcp SDK 版本必须钉死

P0 实测发现 SDK 的客户端 API 与早期版本存在多处差异：传输函数名为 `streamable_http_client`、
请求头须经 `create_mcp_http_client` 传入而非传输函数的 `headers` 参数、传输返回二元组、
结果字段为 `is_error`。这些是 codegen 生成代码直接依赖的接口。

**沙箱基础镜像与 codegen 必须钉同一个 mcp 版本**，且该版本写入镜像构建文件而非浮动安装。
SDK 升级视为需要重跑 codegen 并回归冒烟的变更——否则 21 个 stub 会在同一时刻整体失效，
且失败点在运行时而非构建期。

#### 会话生命周期上下文（`bkn_context`）

业务类工具调用受会话守卫约束，缺少上下文时服务端返回 `conversation_required` 或
`interaction_required`。该上下文是**工具入参**而非请求头——服务端从 `input["bkn_context"]`
读取 `conversation_id`、`interaction_id` 等字段
（`agent-retrieval/server/driveradapters/lifecycle_middleware.go`）。

处理方式：**agent 侧在发起执行时，将已开启的 conversation 与 interaction 经 `env_vars`
透传进沙箱；`_call` 在每次调用时读取并注入 `bkn_context`。**

```python
def _business_context() -> dict | None:
    conversation = os.environ.get("BKN_CONVERSATION_ID", "").strip()
    interaction = os.environ.get("BKN_INTERACTION_ID", "").strip()
    if not conversation or not interaction:
        return None
    return {"conversation_id": conversation, "interaction_id": interaction}
```

三点取舍：

1. **不出现在函数签名与 digest 中。** 它是链路管道，不是模型该决策的参数；暴露给模型只会
   增加出错面与上下文开销。
2. **沙箱不自行开启 interaction。** `bkn_start_interaction` / `bkn_finish_interaction` 由
   agent 侧管理，沙箱沿用同一个 interaction，证据链才连续——否则一次任务会分裂成两条互不
   关联的记录。这也是 codegen 将两者列入 `SKIP_TOOLS` 的原因。
3. **在调用时读取而非导入时读取。** 同一个沙箱 session 可能服务多次执行，上下文按执行变化。

#### `response_format` 覆盖为 `json`

schema 默认 `response_format=toon`，是为「返回值直接进模型上下文」优化的省 token 文本格式。
代码模式下返回值先由脚本处理，需要的是可下标访问的结构，因此 codegen 通过 `DEFAULT_OVERRIDES`
将该参数默认值覆盖为 `json`。

这是两种消费模式的权衡差异，不是 schema 的缺陷：MCP 外部客户端直接把结果喂给模型，toon 更省；
沙箱把结果喂给代码，json 才可用。同一份 schema 服务两种模式，差异由 codegen 承担。

### 3.3 凭据与网络边界

**沙箱执行的是模型生成的不可信代码。** 脚本可以绕开 `_tools.py` 自行构造 HTTP 请求，
因此安全性不能依赖 stub 的正确使用，只能依赖沙箱手里那份凭据的权限边界。

这正是选择 MCP 端点而非 `/in` 的决定性理由：

| | `/in` 内部接口 | MCP 对外端点 |
|---|---|---|
| 鉴权 | 信任 header、不校验 token | `Authorization: Bearer`，OAuth access token 或 AppKey（`bak_` 前缀） |
| 沙箱伪造身份 | 可行——脚本自拼 header 即可冒充任意调用者 | 不可行——权限上限即所持 token 的权限 |
| 额外组件 | 需自建网关剥离并重注身份 | 无 |

`/in` 方案中的身份注入网关，是 `/in` 自身鉴权缺失带来的成本，而非本方案的固有需求。
走 MCP 后该组件整体消失，攻击面同步收敛。

#### 凭据形态：现有 AppKey（`bak_`）

**本期决定采用现有 AppKey，不等待作用域机制。** 需明确其代价：AppKey 按设计「以其所有者的
身份认证」，验证后解析出所有者的 id 与 account_type，下游授权与该所有者持 OAuth token
完全一致——`APIKey` 结构体中不存在 scope 字段或只读位，`ExpiresAt` 允许为 nil 即永不过期
（`bkn-safe/server/internal/model/model.go`）。因此一把 AppKey 的权限上限等于其所有者的
全部权限。

在缺少作用域机制的前提下，通过既有授权体系施加等效约束：

1. **沙箱专用服务账号。** 为沙箱单独建立一个应用账号作为 AppKey 的所有者，仅授予其执行
   任务所需知识网络的权限。由于 AppKey 继承所有者权限，最小权限由账号授权承担，无需修改
   AppKey 模型。这是本期作用域控制的**主要手段**。
2. **强制设置 `ExpiresAt`。** 模型层已支持过期时间，仅非强制。沙箱 AppKey 一律签发带
   TTL 的版本并纳入轮换。
3. **定向出口。** 沙箱容器默认 `NetworkMode=none`，仅放行至 context-loader MCP 端点的
   单条出口，不开放通用出网。
4. **用量监控。** `LastUsedAt` 与 MCP 服务端审计日志结合，用于发现异常调用与识别失效密钥。

审计由 MCP 端点侧原生承担：谁、以何凭据、调用了哪个工具，在服务端即可还原。

**残留缺口**：上述手段的粒度是「账号」，不是「本次执行」。同一把 key 在有效期内对该服务账号
授权范围内的全部知识网络均可读写，泄露后须人工吊销。收敛该缺口需要 AppKey 支持作用域，
见第 8 节。

**待验证的约束**（见第 6 节风险）：MCP 会话守卫对 `bkn_context` 的要求。

### 3.4 `run_code` 工具定义

注册到执行工厂 toolbox，参数为 `code`（Python 源码）与可选 `timeout`。

其 description 直接采用 `tool_digest.md`，内容包含四部分：

1. 环境说明——工作目录已预置 `_tools.py`，仅 stdout 返回，故应在脚本内完成过滤与聚合
2. 函数签名清单，按「发现类 / 查询类 / 执行类」分组
3. 调用顺序约束——`kn_id`、`ot_id` 不可臆造，须先经 `list_knowledge_networks`、
   `get_kn_detail`、`get_object_types` 取得
4. 一个端到端示例，以及自省方式（`help(fn)` 读取完整 schema）

**采用两级信息披露。** 完整 schema 不进上下文：`query_object_instance` 仅 `condition` 一项的
描述即达数百 token，21 个工具全量展开不可行。签名与顺序说明常驻上下文（预估 1.5K token 量级），
完整 schema 留在 docstring 中由模型按需读取。该思路与 `get_kn_detail` 的 `detail_level` 一致。

### 3.5 `find_tools`：条件性

若 `tool_digest.md` 的 token 量超出 3K，则增加 `find_tools(query)` 作为第二个顶层工具，
按语义检索工具清单并返回签名与完整 schema，description 中仅保留一句总述。

21 个工具处于是否需要该机制的临界规模。**先测量再决定**：低于 3K 时全量常驻更优——
少一次往返，且模型规划时可见全貌，路由准确性更高。

**P0 测量结论：不做 `find_tools`。** 生成的 `tool_digest.md` 为 3848 字符，粗估 1.2–1.6K
token，明显低于阈值，全量常驻。若后续工具数显著增长导致 digest 超过 3K，再按本节启用。
（粗估基于中英字符分别计权，精确值待有 API 凭据时以 `count_tokens` 复核。）

## 4. 与现有工具面的关系

| 面 | 消费方 | 本方案影响 |
|---|---|---|
| MCP（`/mcp`） | 外部客户端、**沙箱** | 契约不变，新增一类消费方 |
| 执行工厂 toolbox | bkn-agent | 21 条工具条目收敛为 `run_code` 一条 |
| `/in` REST | 内部服务 | 不变，本方案不使用 |

沙箱作为标准 MCP 客户端接入，与外部客户端走完全相同的契约。这意味着 MCP 面的既有鉴权、
限流与审计能力被直接复用，无需为沙箱另建一套。

**与在途工作的冲突需先协调。** 若干 worktree 中存在 `context_loader_toolset.adp`，其内容是将
context-loader 工具批量注册进 toolbox，与本方案的收敛方向相反。开工前须确认该项工作的状态与
落地计划，避免两边同时推进。

一项附带收益：新增内置工具的手工对齐点从三处（`/in` 路由、`.adp`、MCP）减少为两处。
`.adp` 不再随工具增减变动（`run_code` 恒定），`_tools.py` 由 codegen 自动跟随。

## 5. 分期

### P0 — Spike，验证收益（连通性部分已完成）

**已完成部分**（对开发 VM 实测，`feature/ptc-code-mode-tool-calling`）：

| 验证项 | 结果 |
|---|---|
| MCP 端点连通与 AppKey 鉴权 | 通过 |
| 远端工具集与本地 stub 比对 | 远端 22 / 本地 21，差异均可解释（`execute_skill` 条件注册未启用；2 个生命周期工具按设计不生成） |
| 读工具实调（三级链路） | 通过：`list_knowledge_networks` → `get_kn_detail` → `get_object_types` |
| 会话复用 | 通过，一次执行内仅握手一次 |

**已取得的收益量级**（开发 VM 实测，worldcup 知识网络）：

| 任务 | 中间结果 | 回模型 | 压缩比 |
|---|---|---|---|
| 判定 28 个对象类中哪些字段支持 `knn` 向量检索 | 130,200 字符 | 30 字符 | 4340x |
| 统计 1000 条进球记录得出历史射手榜 Top 5 | 797,317 字符 | 167 字符 | 4774x |

第二项的意义超出「省 token」：797K 字符的 JSON 约合 20 万 token，单次工具返回即接近或超出
可用上下文。**现状下该问题不是成本高，而是无法可靠完成**——只能分页多轮拉取并逐轮摘要，
而基于有损摘要再做聚合，结果必然失真。代码模式下这是一次调用加一个 `Counter`。

这也印证了第 1 节的判断：真正的收益是把数据加工从模型推理挪进代码，省 token 是副产品。

**codegen 已实现**，非手工 stub——原计划的手工 3 工具阶段被跳过，因生成器成本低于预期。

**剩余部分**：写工具（`execute_action`）调用、越权拒绝验证、eval-set 的三项指标测量
（首次成功率、端到端 token、端到端延迟）。上述测量为人工编写脚本所得，尚未验证模型
自主写出等价脚本的成功率——那是 P0 准入门槛的核心指标，需接入 `run_code` 后才能测。



范围最小化：手工编写 3 个工具（`list_knowledge_networks`、`get_object_types`、
`query_object_instance`）的 stub 与 description，凭据以人工签发的 token 替代自动下发，
不做 codegen。

**P0 的第一项任务是打通沙箱到 MCP 端点的连通性**：以沙箱专用服务账号签发的 AppKey，
经 MCP 端点调通一次读工具与一次写工具。会话守卫对 `bkn_context` 的要求（第 6 节）在此一并验证；
该项若不成立，需补充上下文透传设计后再推进其余工作。

随后以 eval-set 跑 10 个真实业务问题，测量：

- 首次 `run_code` 成功率（脚本无需修改即得到正确结果的比例）
- 端到端上下文 token 相对现状的变化
- 端到端延迟变化

**准入门槛：首次成功率不低于 70%，且 token 有可观察的下降。** 低于该水平通常意味着
description 缺失调用顺序说明或示例不够典型，应先修 description 再评估，而非直接推进 P1。

### P1 — 全量实现

codegen 覆盖 21 个工具并接入 CI 校验；执行工厂完成凭据自动下发与网络策略；
`run_code` 正式注册至 toolbox；沙箱镜像烤入 `_tools.py` 与 mcp SDK。

### P2 — 按需优化

`find_tools`（依 3.5 的测量结果决定）、沙箱 session 复用调优、并发执行支持。

## 6. 风险与取舍

**AppKey 权限过大且可长期有效——本期已知并接受。** AppKey 以其所有者身份认证，无 scope
字段、无只读位，`ExpiresAt` 允许为 nil。交由运行不可信代码的沙箱持有，意味着脚本可绕开
`_tools.py` 直接使用该凭据，做该所有者权限范围内的任何操作，含跨知识网络与写操作。

**该风险为本期显式接受的取舍**：P0/P1 采用现有 AppKey 落地，作用域机制后补（第 8 节）。
缓解手段见 3.3——沙箱专用服务账号承担作用域、强制 TTL、定向出口、用量监控。其粒度为
「账号」而非「本次执行」，与叠加的沙箱隔离缺口共同构成本期最大的安全敞口。

**上线判据**：在 AppKey 作用域机制落地前，沙箱服务账号的授权范围不得超出 PoC 或测试环境的
知识网络。面向生产数据启用，须先完成第 8 节。

**MCP 会话守卫可能要求 `bkn_context`。** 已知 `/kn/` 路径的调用会被生命周期守卫拦截，
需要携带有效的 conversation 与 interaction 上下文。沙箱发起的调用是否同样受约束、
若受约束则上下文如何从 agent 侧透传至沙箱，需在 P0 验证并补充设计。

**沙箱隔离现状不足以承载本方案。** 当前 `DISABLE_BWRAP` 默认开启、工作区按整 bucket 挂载、
Pod 以 privileged 运行。在这些条件下让沙箱持有可访问业务数据的凭据，风险不可接受。
**P1 上线前必须先收敛隔离配置**，这是硬前置，不是并行项。

**小任务可能更贵。** 单次调用即可完成的请求，代码化路径要额外承担 description 的常驻开销、
模型编写脚本的输出 token，以及可能的脚本修正轮次。是否保留少量高频工具在顶层，或按任务
复杂度做路由，属于开放问题，建议以 P0 数据回答，不预先设计。

**沙箱冷启动延迟与 MCP 握手叠加。** 执行工厂已有 session pool 覆盖前者；MCP 握手已通过
模块级会话复用降至每次执行一次。两项叠加后的实际延迟需在 P0 实测。

**脚本修正循环失控。** 需在 `run_code` 侧设执行超时与单次执行内的重试上限，避免模型在
沙箱内无限重试。

**mcp SDK 接口漂移。** 客户端 API 在版本间已发生过多处不兼容变更（见 3.2）。生成代码直接
依赖这些接口，一次浮动升级会让全部 stub 同时失效，且失败暴露在运行时。缓解：镜像与 codegen
钉同一版本，升级纳入回归流程。

**工具的条件注册导致 stub 与部署不一致。** `execute_skill` 等工具按开关注册，schema 存在
不代表远端可用。冒烟脚本已对比工具集并显式告警；生产链路需在 `run_code` 启动时做同样比对，
否则模型会调用一个环境中不存在的函数并得到难以归因的错误。

## 7. 验收

- eval-set 上首次 `run_code` 成功率与 token、延迟三项指标达成 P0 门槛
- MCP 面契约无变化，外部客户端回归通过
- codegen 的 CI 校验生效：修改任一 schema 后不重新生成即导致流水线失败
- 沙箱凭据越权被拒：以脚本直接构造请求，访问沙箱服务账号授权范围外的知识网络，返回鉴权失败
- 沙箱 AppKey 均带 `ExpiresAt`，无永不过期的密钥
- MCP 服务端审计日志可还原「某次 execution 以何凭据调用了哪些工具」

## 8. 后续：AppKey 作用域

本期缓解手段的粒度是「账号」，收敛到「本次执行」需要 AppKey 自身支持作用域。建议形态：

| 字段 | 作用 |
|---|---|
| `Scope.KNIDs []string` | 限定可访问的知识网络；空值等同现有行为 |
| `Scope.Write bool` | 关闭时拒绝 `execute_action`、`execute_skill` 等写操作 |
| `ExpiresAt` 非空约束 | 对沙箱用途的密钥强制 TTL |

存量密钥在无 scope 时保持现有行为，按「宽松 → 影子日志 → 翻转」三段式推进，不破坏兼容。
落地后，沙箱凭据可按单次执行签发：作用域限定到本次任务涉及的知识网络，随执行超时失效，
届时 3.3 的残留缺口关闭。
