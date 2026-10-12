# #2061 系统审计部署与验收

**Goal:** 在保留安全默认关闭的前提下，为需要系统审计的部署提供正式配置和可重复的入账查询验收。

**Architecture:** 最新主线 #2068 已有未配置诊断、Secret 只读 preflight 和升级 values 保留；不重复实现这些功能。本次提供不含凭据的 opt-in profile，沿用原有 Kafka、SASL、Core MariaDB、LogAppendTime 启动校验；部署后用原权限查询真实事件。不得自动授予角色、创建 ACL、变更鉴权或伪装系统审计记录。

- [x] 正式 profile 的默认关闭/不完整配置拒绝/完整配置渲染/凭据引用合同。
- [x] 提供只读验收脚本，验证 source 状态和明确 event_id，空列表不能证明采集成功。
- [x] 隔离真实 Kafka/MariaDB/source-built Core：关闭诊断、启用空查询、真实受管配置变更审计生产→消费→查询、普通用户拒绝。
- [x] 保留现有安装升级和 Secret preflight 回归，非法配置和 Kafka 不可达 fail-closed 验证。
- [x] 文档、独立评审、独立 PR，报告真实验收边界。

实际验收补充发现：Core 控制审计生产端未提供 actor.display_name_snapshot 和 target.name，schema 入账允许但020公开日志合同过滤。最小修复在既有 Safe /me 身份读取时保留 name（不参与权限/指纹），生产时写入该名称及服务端已知配置/操作名称；不改变 query guard，缺名称不得纯ID兜底，历史行不回填。端点 lease 和 Safe 是隔离测试前置条件 fixture，Kafka、MariaDB、Core producer/consumer 与公开查询均为真实实现；不声称目标生产部署、实际Collector收敛或完整平台IAM验收。

独立评审补充：从可信 profile 成对使用真实 ActorID/name，独立保留 EffectiveSubjectID，避免委托场景混用有效主体ID和实际操作者名称。名称不进入 fingerprint 或任何授权规则；增加不等身份与名称变更不改变授权范围回归。

## PR 评审补充

- [x] Safe 允许省略显示名；对已校验的同一操作者按 Safe 既有审计合同取 `name → account` 展示快照。二者皆空仍记录 coverage gap，不以 ID 兜底。回归覆盖空白名、登录名回退、身份不匹配拒绝，以及展示属性变化不改变权限或 fingerprint。
