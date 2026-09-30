# 日志与 Trace Review 规则

本规则适用于 `bkn-foundry` 中新增或修改业务接口、Agent、Context Loader、Kafka producer/consumer、日志 producer 和 Trace producer 的 PR。它是模块 review 的必查项，不要求新增角色、权限或安全体系。

## 日志五条硬规则

1. **只记录真实状态变更**：新增、修改、删除，以及权限、配置、授权等管理变更；一项用户动作只产一条业务日志。
2. **不记录读取行为**：进入页面、打开详情、列表、搜索、筛选、刷新、查询日志/Trace、状态轮询、健康检查、内部重试。
3. **不记录内部过程**：成功/失败的鉴权判定、资源过滤、token 校验都不是业务日志。
4. **每条必须有可识别操作者**：从登录会话或 token 映射到用户；无法解析主体则拒绝该日志进入流，禁止用 `anonymous` 兜底。
5. **操作对象必须是生产时写入的业务快照**：ID 只能作辅助引用；关系变更必须写业务语义，例如“将用户 A 调整至部门 OpenBKN公司”。

Review 时逐条回答：

- 这是不是已提交的状态变更？如果不是，producer 不应写日志。
- 一次用户动作是否恰好一条业务日志？是否存在 middleware、重试或鉴权路径重复写入？
- 操作者是否来自登录会话或 token 映射？主体缺失时是否在 producer 入口拒绝？
- 操作、对象、操作者是否能脱离 HTTP method/path/UUID 被业务用户读懂？
- 对象名称/关系语义是否在生产时写入快照，而不是查询端猜测？

## Trace 硬规则

**bkn-trace 只记录 Agent（包括第三方 Agent、bkn-agent）通过 Context Loader 的 MCP 接口发起并完成的业务调用。**

因此：

- 允许进入 Trace 的入口是 Context Loader MCP 的业务工具调用；必须能关联调用主体和业务上下文。
- 通用 HTTP、页面读取、平台内部调用、MCP `initialize`/`tools/list`/信息发现、健康检查、日志/Trace/审计查询和授权判定不得创建 Trace。
- Context Loader 生命周期调用只建立/结束同一次 MCP 业务调用的上下文，不单列用户可见 Trace。
- 不得用普通 OTLP span、HTTP middleware 或日志字段补造越界 Trace。

## Kafka / Outbox Review

- 纳入 020 的 producer 使用 Kafka 通道，不新增运行时 Outbox、轮询 worker 或双写路径。
- Kafka 发送/消费失败不阻断业务；来源状态必须能说明正常、降级或不可用。
- 消费端按既有事件 ID 幂等；不得借通道改写业务语义、主体或对象快照。
- 只有存在真实生产入口的事件才能登记和验收；不为“以后可能接入”的模块预留事件。

## 必须提供的 Review 证据

涉及日志或 Trace 的 PR 必须附：

1. 成功业务动作的测试与结果；
2. 读取、刷新、401/403、校验失败、业务拒绝和主体缺失的负向测试，证明没有日志/Trace；
3. 对象名称、关系语义、操作者解析的断言；
4. Kafka producer/consumer 或 MCP 接口的端到端证据；
5. 本地 ARM64 镜像部署到 Kind 后的 Studio 验收结果。

没有以上证据，review 不得以“技术请求成功”替代业务语义验收。
