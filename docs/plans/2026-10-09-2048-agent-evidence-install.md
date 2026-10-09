# #2048 Agent Evidence 安装与启动修复

**Goal:** 全新安装默认启动既有 Evidence publisher，显式关闭与升级配置优先，发布故障不阻断 Agent。

**Architecture:** 复用 Kafka Publisher、8081 内部控制和现有 Secret 引用。安装默认值、已安装配置、当前 values/CLI 按顺序覆盖；仅携带 Evidence publisher 的受支持字段，不复用旧 Chart 全量配置。启用时只读检查现有 Kafka Secret 的两个 key，不创建凭据、权限或鉴权机制。

**Tech Stack:** Helm、Bash、Python、pytest。

## 设计取舍

- 仅翻转 Chart 默认值不能保护升级与显式关闭。
- 推荐在现有安装函数内增加针对 bkn-agent 的配置保留和只读前置检查，沿用已有 Audit 安装模式。
- 不建设通用部署控制器，不处理 #2056 的预算或 #2061 的消费者。
- 不恢复 #2047 已移除的内部 OAuth/签名要求。Artifact HTTP 凭据保持独立。

## 实施与验收

- [x] Chart 回归：新安装默认开启，显式 false 不渲染 Kafka/准入变量；无鉴权/签名变量。
- [x] 安装回归：默认值 < 已安装配置 < 当前 values/CLI；禁用不读取 Kafka Secret；缺失/空 key 阻止安装，且不记录 Secret 内容。
- [x] 运行回归：配置或控制初始化失败不阻止 Agent 启动；正常启动与显式关闭保持原合同。
- [x] 最小实现并更新产品安装/升级说明。
- [x] Helm lint、安装脚本回归、Agent Evidence 回归、静态检查。
- [x] 验收记录明确 Kafka/Ledger 实测范围，独立 PR 对应 #2048；不以该 PR 关闭其他 Issue。

验收记录：`docs/validation/2026-10-09-2048-agent-evidence-install.json`。Agent 全量303测试通过；独立评审发现的自定义控制地址兼容问题已修正。真实源代码链路验证 Publisher → SASL Kafka → MariaDB Ledger → OpenSearch 投影，consumer lag=0；不等同于生产 Helm rollout 或真实 LLM/工具执行验收。
