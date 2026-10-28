# OpenBKN 资源配置与 Agent / Trace 容量参考

资源规划应同时说明工作负载、部署资源和验收目标。注册 Agent 数、活跃 Agent 数、
历史 Trace 条数和投影积压量分别描述不同维度，不能相互替代。

## 完整平台安装要求

[完整平台安装要求](https://github.com/openbkn-ai/bkn-foundry/blob/main/help/zh/install.md#-环境要求)

## 将 Agent 工作负载换算为任务流入

- N：持续产生业务调用的活跃 Agent 数。
- f：每个 Agent 每秒的业务轮次。
- k：每轮工具调用数量；重试和扇出需要单独统计。
- E：每轮实际产生的投影任务数，包含生命周期、工具回执、答案持久化和异步追加任务。

```text
业务轮次/s = N × f
工具调用/s = N × f × k
投影任务流入/s = N × f × 实测 E + 其他后台任务流入
```

E 是每轮任务数，已包含该轮工具调用的影响，不能再额外乘 k。应使用代表性完整链路
校准 E，覆盖首次与复用会话、成功与失败、重试、证据及答案大小等分布。
工具调用数也不总等于 Trace 列表条目数，需要核对 Trace ID 与 receipt 的分组规则。

同样的活跃 Agent 数，提高调用频率、工具扇出或重试比例都会增加压力。
任何并发容量结论都应附带这些参数以及测试入口和时长。

## 资源预算与部署配置

| 组件 | 需要记录的配置 | 验收时观察的指标 |
| --- | --- | --- |
| agent-observability | 副本数、Pod CPU requests/limits、内存预算 | CPU throttling、RSS、请求延迟、投影吞吐 |
| MariaDB | CPU/内存、buffer pool、连接池、持久卷 | 物理读、fsync、锁等待/死锁、连接等待、完整领取耗时 |
| OpenSearch | CPU/内存、heap、索引/副本、持久卷 | heap/GC、索引与 refresh 延迟、查询延迟、磁盘使用 |
| 依赖服务 | Evidence/Kafka、对象存储、本体查询及其他共享服务 | 各自的队列、错误、吞吐和资源竞争 |

配置入口见 [Trace Chart](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/charts/agent-observability/values.yaml)
和 [Core 数据库连接池](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/src/boot/bootstrap.go)。
默认值只代表部署初值，不是经过认证的容量等级。连接池若为代码固定值，不能用不存在的
Helm/env 参数调整；新增应用副本也会增加数据库连接和 worker，需要一起测量。

OpenSearch heap 应留出非 heap 和文件缓存空间，具体比例与约束见
[官方配置说明](https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/)。
MariaDB buffer pool 应按数据库实际可用内存和工作集规划，保留连接、其他缓存及系统开销；
专用数据库主机的比例不能直接套到共享宿主上的每个容器，见
[官方内存说明](https://mariadb.com/docs/server/ha-and-performance/mariadb-memory-allocation)。

LLM、embedding/rerank、本体或数据查询的计算、显存和索引成本应单独预算。
局部 Trace 压测、未限额的宿主进程或开发集群结果，不能直接作为完整生产部署容量。
具体实验配置、候选资源数值和压测结果见
[模块 README 的测量记录](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/README.md#issue-2585-开发环境测量记录)，
其中的 Agent 档位不构成产品并发承诺。

## Trace 留存与磁盘规划

```text
每日业务轮次 = N × f × 86400 × 活跃占空比
每日任务数 ≈ 每日业务轮次 × 实测 E
答案原始字节/天 ≈ 每日轮次 × 平均答案字节
持久存储预算 ≈ 实测新增字节/天 × 热留存天数 × 数据副本系数 + 备份/日志/维护空间
```

任务数不等于永久 OpenSearch 文档数：投影会更新已有文档，答案确认后可释放 outbox
大正文，而其他历史任务和业务事实仍可能保留。不能只用 pending 数或合成夹具的单行
大小推算生产每 Trace 的存储成本。

应测量一定业务轮次下 SQL 数据/索引、OpenSearch store、答案和日志的实际增量，
再确定热留存、归档和清理策略。容量扩展不能替代留存治理。

## backlog 恢复与验收

```text
长期稳定：消费速率 μ 持续高于流入 λ
近似追平时间：B / (μ - λ)，仅当 μ > λ 且速率近似稳定
```

大积压下能够领取任务，只说明该数据集的领取路径可工作。保留 ID 顺序时，已有的到期
任务会排在新任务前面；领取预算与新 Trace 的查询可见延迟必须分别验收。

- 在生产资源限额、预期历史规模和代表性内容分布下，同时施加生产与消费负载。
- 分别记录调用响应、完整领取、任务交付 ACK 和查询可见性的延迟分位数及错误率；
  超时样本单独计数，不能只报告成功样本。
- 稳定段应覆盖刷盘、GC、重试和突发恢复，检查 backlog 斜率及最早 pending 年龄是否回落。
- 为恢复突发保留余量；流入与消费刚好相等时，没有追平能力。
- 验证租约回收、失败补偿、幂等和 fencing；多 worker/副本需要重测，不按副本数线性外推。
- 测试完成不等于所有负载档满足 SLO，应先约定新鲜度、错误率和恢复时间目标。

索引迁移、启动和恢复约束见
[模块 README](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/README.md#v040-升级与恢复约束)。
不得通过放宽领取预算或绕过迁移账本校验掩盖问题。
