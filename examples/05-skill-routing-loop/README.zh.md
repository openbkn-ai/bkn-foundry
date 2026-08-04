# 05 · Skill Routing Loop — 业务知识网络驱动的 Skill 路由

> [English](./README.md)

> 3 个物料触发同样的库存告警，`find_skills` 把每个物料路由到不同的处置 Skill——
> 每条路由都能在业务知识网络里找到依据，全程没有 Agent、没有 LLM。

## 故事

续作 03 那位采购工程师：她现在看到每张告警单上已经写好了处置方案。3 个物料、
3 条不同路径，**没改一行 prompt**。BKN 里的 `applicable_skill` 关系是 Skill 路由的
**唯一真相源**——对每个物料,`find_skills` 返回的就是 KN 绑定给它的那个 Skill,
脚本直接执行该 Skill。没有推理、没有 LLM、没有 Agent:路由本身就是数据。

## 这个 example 展示什么

`find_skills` 是**确定性的 Skill 路由**:给定一个物料实例,它通过
`applicable_skill` 召回 KN 绑定给该物料的 Skill。5 个组件协同跑通一个可验收闭环:

| 组件 | 职责 |
|---|---|
| **execution-factory** | 注册 / 版本化 3 个 Skill 包 |
| **业务知识网络（BKN）** | 通过 `applicable_skill` 关系把 Skill 绑到物料 |
| **Vega** | 把 BKN ObjectType 映射到 MySQL 表（读多写少） |
| **context-loader (`find_skills`)** | 把每个物料实例路由到它绑定的 Skill |
| **run.sh 验收器** | 断言 `find_skills` 选路 → 执行该 Skill → 检查调用日志 |

同一个 `find_skills` 能力也通过 agent-operator-integration 注册成了 MCP server,
任何 MCP 客户端——Agent、工作流、或这个脚本——都能消费同样的路由。这里 run.sh
直接走它的 REST 路由（`POST /api/agent-retrieval/v1/kn/find_skills`），所以 demo
不需要任何 LLM。

## 前置条件

- `openbkn` CLI（`npm install -g @openbkn/bkn-sdk`，Node ≥ 22）
- 启用了 execution-factory + Vega + context-loader 的 BKN Foundry 平台
  （先 `openbkn auth login <平台地址> [--insecure]`）
- **平台能访问到**的 MySQL（不是你笔记本上的），且账号有 CREATE/INSERT/SELECT/UPDATE 权限
- `python3`（依赖 Flask + mysql-connector-python，
  `pip install -r tool_backend/requirements.txt`）

简单自检平台组件是否就绪：

```bash
openbkn auth whoami                                      # 是否登录
openbkn call /api/agent-operator-integration/v1/mcp/     # execution-factory 可达？
openbkn call /api/vega-backend/v1/catalogs                # Vega 可达？
```

## 快速开始

```bash
cd examples/05-skill-routing-loop
cp env.sample .env
vim .env                                    # 填 PLATFORM_HOST、DB_*
pip install -r tool_backend/requirements.txt
./run.sh                                    # 端到端约 3 分钟
./run.sh --bonus                            # 跑 Bonus 段，并做可校验验收
```

> **并发注意：** 请不要同时运行两个 `./run.sh` 实例。脚本使用固定的 `KN_ID`
> （`ex05_skill_routing`）以及固定的 Skill 名（`standard_replenish` /
> `substitute_swap` / `supplier_expedite`）；第二个实例会在 Skill 注册阶段
> 直接撞上，并且任一实例的清理逻辑会把另一个实例的 KN 一起删掉。

## 你会看到什么

| 物料 | KN 证据 | find_skills 路由到 | 原因 |
|---|---|---|---|
| MAT-001 | 绑定 `substitute_swap`；SUB-001A/B 有库存 | substitute_swap | Python 算法打分挑替代料 → 调 MES |
| MAT-002 | 绑定 `supplier_expedite`；SUP-2 capability=expedite | supplier_expedite | 供应商能加急 → POST 供应商门户 |
| MAT-003 | 只绑定 `standard_replenish` | standard_replenish | 默认路径 → 走 ERP 下单 |

脚本对每个物料调用 `find_skills`，打印路由到的 `skill_id → name`，断言它符合
预期路由，随后直接执行该 Skill 打到本机 mock 业务后端，并检查
`.tool_backend.log` 里出现：

```text
[mes/swap]
[supplier/expedite]
[procurement]
```

看到 `✓ mock backend observed MES, supplier, and ERP calls` 说明三条业务动作
都已经打到 mock 后端。

如果你希望 `builtin_skill_execute_script` 在平台执行沙箱里也真正打到 mock 后端，
把 `.env` 里的 `TOOL_BACKEND_PUBLIC_URL` 设置成平台/沙箱可访问的地址
（例如一台内网可达机器的 `http://<host>:8765`）。默认
`http://127.0.0.1:8765` 只保证本机验收器可访问；平台沙箱里的
`127.0.0.1` 不是你的笔记本。

## Bonus — 改业务数据 → 路由跟着变

`./run.sh --bonus` 会调 mock 业务系统的 admin 端点，把 MAT-002 的绑定 Skill
从 `supplier_expedite` 改成新注册的 `standard_replenish` Skill ID（直接 UPDATE
`materials.bound_skill_id`，由 `applicable_skill` 的 direct-mapping FK 决定边），
随后重新路由 MAT-002。下一次 `find_skills` 拿到的就是新候选集，路由自动切到
`standard_replenish`——**没改 prompt、没重新部署任何服务**。

> **为什么不需要重建：** 这里所有对象类都绑定 Vega **资源**，且本示例不给这些资源建本地
> 索引，因此本体查询每次都读源库当前数据，MySQL 的 UPDATE 对下一次 `find_skills` 立即可见。
> 知识网络层面也没有可执行的构建，该接口已下线。
>
> 反过来也要注意：一旦给资源建了索引（`openbkn vega dataset build`，示例 01/02 就是这么做的），
> 该资源的读取会切到构建快照。在本示例里这么做恰好会破坏这一幕——改绑要等到下次重建才可见。

## 原理细节

完整设计文档：[`docs/superpowers/specs/2026-04-27-skill-routing-loop-example-design.md`](../../docs/superpowers/specs/2026-04-27-skill-routing-loop-example-design.md)

包括：
- BKN schema 和 `applicable_skill` 的 direct-mapping FK
- 为什么 MCP server 注册时必须带 `X-Kn-ID` header
- 为什么脚本先注册 Skill，再用真实 Skill ID 渲染 CSV
- MCP / Skill 清理的三态机协议

## Troubleshooting

如果 `find_skills` 对某个物料返回空（或返回了错误的 Skill），说明 BKN 里的
`skills.skill_id` 或 `materials.bound_skill_id` 没有对齐到 execution-factory
注册返回的真实 Skill ID。当前脚本会先注册 Skill，再用真实 ID 渲染 CSV；正常
情况下不应再出现这个问题。

## Cleanup

脚本默认**保留**平台资源（KN、MCP、Skills、Catalog），退出时打印各资源 ID。
需要自动删除时用 `CLEANUP=1 ./run.sh`（成功 / 失败都清理）。

本地 mock backend 进程同样会在退出时停掉；设 `DEBUG_KEEP=1` 可让它继续跑，
保留整条路由链路便于调试。`CLEANUP=1` 优先于 `DEBUG_KEEP=1`：显式清理时一定会停掉进程。

在上一次跑剩下的资源上重跑没问题:KN ID 是写死的（`ex05_skill_routing`），`bkn push`
以 overwrite 模式导入，同 ID 原地更新。Skill 和 MCP 的名字都带运行时间戳 —— 已被
**已发布** Skill 占用的名字在发布时会被拒绝 —— 所以每次运行注册自己的一套，不会和
上一次撞名。列表堆得太多时手动删掉旧的，或者跑一次 `CLEANUP=1`。
