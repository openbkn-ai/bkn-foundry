# 020 业务溯源查询与分析恢复修复计划

**Goal:** 修复 Foundry #1080 与 EE #74 的摘要读取/筛选缺口；以实际框架错误传播验证并针对性加固 EE #73。排除 #1081。

**Architecture:** 复用现有受授权 artifact-only 读取，按候选会话的首轮交互读取 question/result，再匹配关键词、统计和分页。无新增存储、全局扫描上限、服务、通用重试机制。分析 Agent 保留挂载和范围校验，仅修复有确定性复现依据的工具错误反馈。

**Tech Stack:** Go、MariaDB/OpenSearch projection、Python LangChain/MCP、Studio React。

- [x] 读取实际代码与测试；在当前 020 源码建立干净测试基线。
- [x] 测试先行：关键词只在首轮 artifact 中；页大小稳定、空关键词；超过通用 artifact 上限与排除 Agent；指定会话范围下推；第一轮不可读不得借后轮；读取失败/截断不伪报未记录。
- [x] 实现最小摘要修复；比较路径调用次数、批量预算、权限隔离、后续轮次及已有筛选语义。
- [x] EE #73：实际适配器拒绝传播测试，有限恢复/明确不足结论，Prompt ID 来源；保留技术/授权错误边界。
- [x] 必要的 Studio 摘要暂不可用展示与测试，使用已有 partial/truncated 元数据。
- [x] 运行相关模块 test/vet/build、格式与差异检查；独立子 agent 审核正确性及性能。
- [x] 记录实现和验证差异，向用户交付审阅；不提交、推送或关闭 issue。

已核验运行基线：普通列表问题“最终服务器镜像验收：查询产品库存”可见；关键词搜索 page_size1/20 都返回0；指定 conv_0b3a47ada322db6ec7245a6b91305165 返回摘要空及 projection_scan_cap_reached。

## 本轮验证结论

- Go 模块全量 test、vet、build 通过；摘要、Core projection、内存证据存储回归复核通过。
- Python 全量 299 项通过（实施及主代理独立复核）；真实 MCP adapter/agent 框架工具拒绝可恢复，transport 错误仍传播；预算耗尽保持 content_and_artifact 格式。
- Studio 新增 4 项回归通过，tsc、typed lint、license、format、Vite build 通过。完整目标测试原有 34th call 超时也在 HEAD 重现，未改无关测试。
- 本地 EE 同一部署原包/修复包对照：普通列表 total93不变；关键词 page_size1/20从0恢复6且分页首条一致；conv_0b3a47ada322db6ec7245a6b91305165 从空摘要/partial/truncated恢复首轮问题，partial/truncated=false。
- 没有扩大2000候选上限、没有逐操作查询，没有新增通用重试。实际耗时样本只能证明本次查询可完成，不能证明所有负载无退化。
- 首次验证代理造成502；绕过本机代理后一次原包OpenSearch超时，后续原包复核完成。均未当作业务修复结论。
- 本地 EE 使用未提交的 Foundry Core 工作树构建；没有修改 EE 的正式依赖 pin。Studio 未做浏览器部署验收；分析 Agent 未做真实 LLM 中英文循环复验，因此不宣称这些验收已完成，也不关闭 EE#73。
- #1081 明确排除；未提交、推送、创建 PR、评论或关闭 issue。
