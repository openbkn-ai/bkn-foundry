# OpenBKN 资源配置与 Agent / Trace 容量参考

这份指导区分三类信息：**仓库安装基线**、**本次已测结果**、**待验收的资源起点**。硬件大小、可容纳的历史 Trace 数和活跃 Agent 并发不是同一个指标。

## 1. 完整平台的安装资源起点

[完整平台安装要求](https://github.com/openbkn-ai/bkn-foundry/blob/main/help/zh/install.md#-环境要求)

## 2. 已测资源与部署默认值

| 项目 | 本次实测 | 仓库默认/差异 |
|---|---|---|
| Docker VM |6 vCPU、约15.6GiB，共享已有开发负载|不是独占生产服务器|
| Trace/负载驱动 |宿主Go进程，无Pod CPU硬限制|Chart默认requests 100m/128Mi，limits 500m/512Mi，1副本|
| MariaDB |11.4.13，buffer pool 128MiB|不能把128MiB当作生产推荐值|
| Core连接池 |最大32连接、Go默认最多2 idle|生产代码是16 open / 4 idle，不是同一配置|
| OpenSearch |2.19.4单节点，heap 512MiB，容器1.5GiB|仅为测试配置|
| 投影worker |1个、BatchSize=1、领取2s、lease30s|未做多worker线性扩展验证|

来源：[Trace Chart](https://github.com/openbkn-ai/bkn-foundry/blob/4cd63ffd43836c7991dfb8860e7f27665160af69/bkn-trace/agent-observability/charts/agent-observability/values.yaml#L271)、[Core连接池代码](https://github.com/openbkn-ai/bkn-foundry/blob/4cd63ffd43836c7991dfb8860e7f27665160af69/bkn-trace/agent-observability/src/boot/bootstrap.go#L769)。

**不能把本次“50 Agent档位稳定”直接套到默认500m CPU的Pod上，也不能假设把副本数翻倍就能把容量翻倍。** 多副本会同时增加SQL写入、连接数和worker数量，需要联合压测。

## 3. Agent、工具调用、Trace 与任务的换算

先明确业务模型：

- N：同时活跃、持续产生调用的Agent数，不是注册Agent总数或登录用户数。
- f：每个Agent每秒完成的业务轮次；每10秒一轮即0.1。
- k：每轮工具调用数量、重试次数和扇出。
- E：每轮实际产生的任务总数，包括生命周期、工具回执、答案持久化以及后续更新。

```
业务轮次/s = N × f
工具调用/s = N × f × k
outbox任务流入/s = N × f × 实测E + 其他后台任务流入
```

本轮固定k=1，输入/结果约1KiB，答案16KiB，复用会话的**E=10**，首次新建会话E=12。多个工具、证据链、失败重试、历史溯源等场景会改变E，不应机械地给每个工具都乘10。
本模型每轮只有一个业务Trace；一般业务的Trace ID/receipt分组规则仍需单独统计，工具调用数不总等于Trace列表条目数。

例：50个Agent每10秒一轮，约5轮/s、50任务/s；若改成每秒一轮，同样50个Agent就变成约500任务/s。这比“系统里有50个Agent”更有容量意义。

## 4. 本轮可参考的容量边界

| 活跃Agent等价 | 每Agent频率 | 总调用/s | 本轮表现 |
|---|---|---:|---|
|10|每10秒一轮|1|5分钟队列可消化，无调用/领取错误|
|20|每10秒一轮|2|5分钟无持续积压，无调用/领取错误|
|50|每10秒一轮|5|1500轮全部成功，任务流入/交付基本平衡，净增6条pending|
|100|每10秒一轮|10|净增约6133条pending，最早等待约64秒，并出现调用错误|
|200|每10秒一轮|20|明显过载，积压和调用失败增长|
|400|每10秒一轮|40目标|明显过载，实际发起率低于目标，并出现发生器丢弃|

这是30分钟阶梯实验、每档5分钟；不是每档30分钟独立稳态认证。50档的搜索可见性抽样p95约2.19s、p99约4.21s；没有约定业务SLO，所以不能单凭队列平衡宣布满足所有产品体验要求。

当前可把**20～50活跃Agent的该模型负载**作为下一轮生产规格验收区间：先在20档做长稳态，再逐步验证50档；不能据此直接承诺生产最多50个Agent，更不能拿注册Agent总量做容量宣传。

本测试只覆盖真实Session服务、SQL、投影worker及OpenSearch/答案持久化，未覆盖完整MCP/HTTP、鉴权、LLM、真实业务工具、完整Evidence Kafka流、租约回收器及失败补偿。
高负载错误会留下active interaction，后续调用拒绝具有累计效应，因此200/400档的错误率也不是生产错误率预测。

## 5. Trace子系统的待验收资源起点

下面是**下一轮压测候选配置，不是已测通过的容量等级**，也不代表已应用到任何环境。完整平台仍需合并其他服务资源预算。

| 组件 | 建议作为验收起点 | 后续依据 |
|---|---|---|
|agent-observability|1副本；requests 1CPU/512Mi；limits 2CPU/1Gi|观察CPU throttling、RSS、请求延迟和worker吞吐|
|MariaDB专用预算|2vCPU/4Gi内存，buffer pool先2Gi，SSD持久卷|观察实际工作集、buffer pool物理读、fsync延迟、锁等待；需要时再验证4vCPU/8Gi及更大缓存|
|OpenSearch单节点验收|2vCPU/4Gi内存，heap 2Gi，SSD持久卷|观察heap/GC、索引与refresh延迟、磁盘空间与查询延迟|
|Core连接池|先保留当前生产16 open / 4 idle作为验收基线|这是代码固定值；不能编造一个不存在的Helm/env配置来调整。跨副本总连接数需与DB共同规划|
|worker与预算|先保留BatchSize=1、领取2s、lease30s|先修查询/锁冲突，再验证并发或小批量，不用加大超时掩盖退化|

可用现有Chart支持的局部覆盖示例（仅表达验收起点，**未执行**）：

```yaml
replicaCount: 1
resources:
  requests:
    cpu: "1"
    memory: 512Mi
  limits:
    cpu: "2"
    memory: 1Gi
core:
  store: mariadb
  projection:
    enabled: true
    interval: 1s
    leaseIOTimeout: 2s
    lockDuration: 30s
```

这不是完整可安装values文件：数据库Secret、OpenSearch端点/认证、存储、Evidence与答案能力仍需采用现有部署配置；不要覆盖已有有效配置或把测试环境无认证设置照搬到生产。
有序领取依赖v040索引，升级前必须完成索引迁移验证。资源增加不能替代此次有序领取修复，也不能替代新发现的生命周期锁冲突修复。

OpenSearch官方建议heap约为可用内存的一半，剩余内存仍要留给非heap与文件缓存；不是把heap全部给满。[官方配置说明](https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/)
MariaDB官方对**专用数据库主机**的InnoDB缓存建议约70%～80%内存，但必须考虑连接、其他缓存与系统开销；共享主机/容器不能把整台宿主内存按此比例给每个实例。上表2Gi/4Gi是更保守的待验收起点。[官方内存说明](https://mariadb.com/docs/server/ha-and-performance/mariadb-memory-allocation)

## 6. Trace留存与磁盘规划

并发只是瞬时压力，磁盘取决于全天调用量、保留时间、内容大小和索引/副本：

```
每日业务轮次 = 活跃Agent数 × 每秒轮次 × 86400 × 活跃占空比
每日任务数 ≈ 每日业务轮次 × 实测E
答案原始字节/天 ≈ 每日轮次 × 平均答案字节
持久存储预算 ≈ 实测新增字节/天 × 热留存天数 × 数据副本系数 + 备份/日志/维护空间
```

按50个Agent全天每10秒一轮：432000轮/天，约432万任务/天；16KiB答案仅原始内容就约6.6GiB/天，尚未计算输入/结果、SQL事实、索引、日志、复制和备份。
**432万任务不等于432万个永久OpenSearch文档。** 投影可能更新同一文档，答案ACK后会清空outbox大正文，但其他历史任务/业务事实仍可能保留。不能按pending条数或合成fixture单行大小直接推算生产每Trace存储。

应在代表性完整链路中测“新增10000轮的SQL数据/索引、OpenSearch store、答案和日志增量”，再决定热留存与归档。没有留存和清理策略，单纯扩大磁盘也不能长期解决增长；本轮没有删除生产历史数据。

## 7. backlog恢复与验收标准

```
长期稳定：消费速率 μ 必须持续高于流入 λ
近似追平时间：B / (μ - λ)，仅当 μ > λ 且速率近似稳定
```

100万pending能在2秒内领取，只说明领取访问路径在该数据集可工作，不代表新Trace会立即可见。保留ID顺序时，已有几十万到期任务会排在新任务前面。
在实际生产限额上至少检查：

- 每档至少30分钟稳态与突发恢复；本次是总30分钟、每档5分钟。
- backlog长期斜率不为正，最早pending年龄能够回落；同时测流入与交付，避免只看pending某个瞬时值。
- 完整领取预算2s及错误率，业务死锁/重试耗尽，查询可见性p95/p99（需事先约定SLO）。
- 容量要留恢复余量；λ刚好等于μ时，即使平均不增长，也没有能力消化突发。
- 多worker/多副本须重测锁竞争、连接、幂等、fencing和故障恢复，不按副本数线性外推。

当前下一优先级：独立复现并修正active-interaction查询/更新的锁冲突，再在上述生产资源起点、生产16/4连接池、完整请求入口与租约回收流程下复验。500万静态积压更适合作为后续故障恢复与存储规模项目，不能替代这些验收。

相关问题与验证记录：[Issue #2585](https://github.com/openbkn-ai/bkn-foundry/issues/2585)。测试与复现入口见 agent-observability README。
