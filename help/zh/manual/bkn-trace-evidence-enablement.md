# 保留 0.2.0 环境的 Evidence 启用与原生归因验收

本文对应 [#2573](https://github.com/openbkn-ai/bkn-foundry/issues/2573)。保留原 Core
版本及匹配的 EE 装配，不要求升级平台、重建插件或另建解释系统。EE 路由是否可用与
Evidence 是否送达需要分别判断。

## 先分类，再修改配置

独立 agent-observability Chart 有意默认关闭 `kafkaConsumers.evidence.enabled`。
Audit 和 Evidence 是独立消费者，审计正常不能证明 Evidence 正常。官方完整安装器会
启用 Evidence，并在 Core 部署前预检 topic。因此，保留环境中 consumer 关闭时，应核对
实际部署 values 和安装器历史；仅凭独立 Chart 默认值不能判定最新安装器有缺陷。

启动错误 `message.timestamp.type is not LogAppendTime` 证明有效 topic 配置不满足启动
合同，但不证明其具体值或配置来源。管理端元数据查询超时后，这两项仍为未知。不得
绕过该 guard。

使用匹配发布版本的[官方安装流程](https://github.com/openbkn-ai/openbkn-deploy/blob/main/README.zh.md#trace-admission-内部控制协议迁移)，
记录 Core、EE、Chart 和安装器的准确修订。不要用 `--latest` 代替保留版本调查。
变更前私下留存 Helm 用户／有效 values 和镜像摘要；有效 values 可能含敏感信息。
把审阅后的配置写入后续安装实际使用的 values／配置源，不只修改 Deployment 环境变量。

## 配置与准入合同

| 配置／依赖 | 必须满足的条件 |
| --- | --- |
| `core.store`、`core.autoMigrate`、`core.mariadb` | 持久 MariaDB、`autoMigrate=true` 和已有数据库 Secret；先核对匹配版本的迁移账本。 |
| `evidence.store`、`core.projection.enabled` | 持久 Artifact 存储及完整安装的 Core 投影配置，复用已有存储和认证。 |
| `kafkaConsumers.evidence.enabled` | 在审阅后的部署 values 中显式为 `true`。 |
| topic／消费组 | `openbkn.evidence.v1`／`bkn-trace-evidence-ledger-v1`，保留原消费位置。 |
| broker、SASL、Secret | 已授权的连接配置和 Secret／键引用；本流程不要求新增账号、密码、ACL 或内联凭据。 |
| topic 时间合同 | 可读的有效 `message.timestamp.type=LogAppendTime`，并另行处理保留消息的时间来源。 |
| 实时准入 | 持久化且启用的 capture-policy revision、已注册 producer instance、匹配的进程／stream 身份、合法序号和关闭水位；topic 检查不能代替准入。 |
| 生产者与 Artifact | 匹配的发布／内部控制协议，policy、configuration、heartbeat、ACK 可达，Artifact 认证投递正常。 |

已有 capture-policy 单例始终是权威。升级时 `core.capturePolicyInitialState` 只引导
不存在的单例，不覆盖已有策略。无法证明旧有效状态时，保留安全的 disabled 状态，
由有权限的操作者通过既有控制入口显式启用，不能猜测旧状态后直接开启。

安装器仅在 topic 不存在时按要求创建；已有不兼容 topic 会阻止继续安装，不会被自动
修改。应用任何 rollout 前，确认保留发布版本使用的安装器确实具备这一行为。

## 保留消息的保护与分类

改变 topic 或启用原先关闭的 consumer 前，留存并验证 Kafka 备份、相关 MariaDB Ledger／
准入状态和部署配置。[官方备份流程](https://github.com/openbkn-ai/openbkn-deploy/blob/main/BACKUP.zh.md#kafka-在线备份及向已部署环境恢复)
保留 key、value、headers、源分区／offset 和消费位置。需要跨组件一致快照时，单独
安排有界维护窗口，并纳入独立／外部写入方。

记录分区范围与保留消息逐条 timestamp type 的证据。topic **当前**配置不能证明旧消息
的类型；修改配置不会改写旧消息。重投递／恢复也不能把旧生产者时间追认为原始 broker
追加时间。分区目录体积不等于消费积压量。

保留消息为 CreateTime，或其时间来源无法证明时，停止自动启用，先审阅保留／迁移
方案。保留原消息及坐标；只有受支持、可核验的源记录才能使用已有历史迁移 manifest／
准入机制。未知或无法重建的记录继续如实报告 coverage gap。本文不授权删除重建 topic、
跳过／重置 offset、修改保留期、绕过校验，或把旧消息伪装成实时采集。topic 检查成功、
consumer 启动成功均不等于旧数据已被接受。

## 用一次真实只读问答验收

经审阅的部署变更完成后，单独检查 Evidence consumer。`/health/ready` 返回 HTTP 200
不足以验收：生命周期可用性有意独立于消费者。检查 `consumers.evidence` 状态和真实的
持久送达／拒绝证据；响应中缺少该项不能证明 consumer 正在运行。

选择一个已授权、小规模、答案包含可归因事实的真实只读问题，保留第一次执行结果。
独立读回同一 Interaction、Operations 与匹配 Receipts，并逐项核对：

1. 已记录的 `agent.interaction.started` 引用 question artifact 及其 content hash；
   引用在同一 Interaction 中可解析，捕获正文通过哈希校验。详情页有文字不能代替该关系。
2. 必需的结果完整性事件和 Artifact 齐全，匹配 Receipts 达到 Evidence durable。
   业务 `completed` 与证据 pending／partial 是不同事实。
3. EE 原生详情有真实问题、答案和 Operations；记录捕获可用后，显式 POST 生成一次解释，
   核对非空事实 claims 及真实 claim-to-evidence 归因。只有证据节点和边不能算归因成功。
4. 解释外层 `status=ready` 表示已有快照，还须核对 `generationStatus`、claims 和 notices。
   `analysis_pending` 不是归因成功。GET 只读快照，Core 后续变化不会触发自动重新生成。
   保留早先 pending 快照，修复后显式重新生成同一 Interaction，不重跑业务问题掩盖失败。

准确修订、时间、身份、durable ACK、拒绝原因与前后解释状态应私下留存；公共 issue 不附
业务正文、凭据和私有部署信息。上述验收通过前，#2573 的环境处置保持开放，文档或单测
通过不能替代该验收。

## 回滚

rollout 失败时恢复原审阅的 Helm revision／配置及匹配服务装配。保留数据库行、Kafka
消息、offset 和最初失败证据。topic／数据迁移必须有独立审阅的回滚方案，Helm 回滚
不会撤销它们。
