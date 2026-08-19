# 团队研发管理流程

中文 | [English](WORKFLOW.md)

本文档定义了 BKN Foundry 团队的研发协作规范，覆盖 **人与 Agent 的协作边界**、Issue 管理、Feature 追踪，以及团队通知流程。每个规则都有明确的操作步骤和文件路径，确保人和 Agent 都可落地执行。

---

## 📋 目录

- [人 + Agent 协作模型](#-人--agent-协作模型)
- [Issue 管理](#-issue-管理)
- [Agent 工作流](#-agent-工作流)
- [Feature 追踪：Issue → Branch → PR](#-feature-追踪issue--branch--pr)
- [Issue 正文规范](#-issue-正文规范)
- [PR 与合并流程](#-pr-与合并流程)
- [邮件通知流程](#-邮件通知流程)

---

## 🤝 人 + Agent 协作模型

本流程同时面向**人**和 **Agent**（Claude Code / Codex 等，经 GitHub MCP 或 `gh` CLI 接入）。两者共用同一套 Issue / 分支 / PR 规则，区别只在边界。

### 五条原则

1. **所有协作都在 GitHub 上。** 需求、设计讨论、代码、PR、评审、CI、看板、文档全在 GitHub；线下 / IM 聊出的结论必须回贴到对应 Issue / PR 才算数。
2. **能交给 Agent 的，就交给 Agent。** 默认让 Agent 干，人省出精力做判断和把关。
3. **有风险 / 难回退的操作，Agent 可做，但必须人工确认后才执行**（确认闸，见 [Agent 工作流](#-agent-工作流)）。
4. **合并前两道硬闸：CI 全绿 + 代码评审人工 approve**，由 GitHub Branch Protection 强制（见 [PR 与合并流程](#-pr-与合并流程)）。
5. **人是责任主体，Agent 是放大器。** 出问题回到对应的人（模块 Owner）。

### 模块 Owner 与自动路由

每个服务 / 模块在 [`.github/CODEOWNERS`](../.github/CODEOWNERS) 里有 Owner。基于它实现两层自动化：

- **Issue 自动分派**：`issues.labeled` 事件触发 Action，按「服务 label → Owner」自动设 Assignee（见 [Issue 分配规范](#issue-分配规范)）。
- **PR 自动评审**：CODEOWNERS 原生 —— PR 改到某模块路径，自动请求该 Owner Review；配 Branch Protection「Require review from Code Owners」即 Owner 必须 approve。
- **单 Owner 即可**：每模块一个 Owner 就够，不强制配双人。当 Owner 本人是 PR 作者时，走 Owner 的 bypass 合并（见下「合并双闸」）或由其他 maintainer 评审，不会卡死。

**无集中 Triage 值日**：每个 Owner 负责 Triage 自己模块的 Issue。

---

## 🗂 Issue 管理

### Issue 类型与 Label

| 类型 | Label | 设计是否写进 Issue | 说明 |
| --- | --- | --- | --- |
| Bug 报告 | `type: bug` | 否（复杂 bug 可选） | 功能异常、错误行为 |
| Feature 申请 | `type: feature` | **必须** | 新功能或功能增强 |
| 任务 | `type: task` | 推荐 | 工程任务、调研、重构 |
| 文档 | `type: docs` | 否 | 文档缺失或改进 |

### 优先级标签

| 优先级 | Label | 响应时效 |
| --- | --- | --- |
| 紧急 | `priority: critical` | 24 小时内响应，当前 Sprint 必须完成 |
| 高 | `priority: high` | 当前 Sprint 必须完成 |
| 中 | `priority: medium` | 近 1–2 个 Sprint 内完成 |
| 低 | `priority: low` | 未来排期，无强制时限 |

### 协作标签（人 / Agent）

| Label | 用途 |
| --- | --- |
| `agent-ready` | 验收标准齐全、已 `ac-approved`、可独立完成，可交给 Agent |
| `ac-approved` | 验收标准经人工审批通过（`agent-ready` 的前置）|
| `needs-human` | Agent 卡住 / 跑歪后退回，需人接手 |
| `awaiting-confirmation` | Agent 已摆出风险操作方案，等 Owner 确认 |
| `owner-confirmed` | Owner 已确认风险操作，可执行（仅 Owner 可加）|
| `by-agent` | Agent 提交的 PR / Issue，用于度量 |

> 服务 / 模块 label（如 `vega`、`bkn-safe`、`context-loader`）用于按 CODEOWNERS 自动路由给 Owner（见 [`route-issue.yml`](../.github/workflows/automation-route-issue.yml)）。

### Issue 生命周期

```text
Open → Triaged → In Progress → In Review → Validating → Done
         │                                                │
         └────────────── Closed (Won't Fix) ────────────┘
```

| 状态 | 操作说明 |
| --- | --- |
| `Open` | 已创建，待分配 |
| `Triaged` | 已评估优先级、已分配 Assignee 和 Milestone |
| `In Progress` | 已创建分支、设计已写进 Issue，正在开发（**需在 Issue 中更新追踪信息**，见下节） |
| `In Review` | PR 已提交，待 Code Review |
| `Validating` | PR 已合并，部署到目标环境验证中；无需环境验证的任务跳过此列 |
| `Done` | 验证通过（或无需验证的任务 PR 已合并），Issue 自动或手动关闭 |
| `Closed (Won't Fix)` | 决定不做，评论中说明原因 |

> **Validating 与自动关闭的交互**：PR 描述带 `Closes #`，合并即自动关闭 Issue；若看板启用「Issue closed → Done」自动流转，任务会直接进 Done。需环境验证的任务在合并后手动拖回 `Validating`，验证通过再拖 `Done`；验证不通过则 reopen Issue 并拖回 `In Progress`。

### Issue 分配规范

- **自动路由**：新 Issue 打上服务 label 后，由 Action（`.github/workflows/automation-route-issue.yml`）按 CODEOWNERS 自动分派给模块 Owner，确保不漏接。
- **分散 Triage（无值日）**：模块 Owner 负责自己模块的 Issue，创建后 **2 个工作日** 内完成 Triage（定 Priority、Milestone，决定自己做 / 标 `agent-ready` / 放出认领）。
- **自助领取**：成员从 Triaged 且无 Assignee 的 Issue 中自行 self-assign（自己模块优先），拖到 In Progress = 上锁。
- 跨模块 Issue 需 `@` 对应模块 Owner 并说明期望。
- 超过 30 天无进展的 Issue 需重新评估或关闭。

---

## 🤖 Agent 工作流

### 何时交给 Agent

**验收标准翻转**：鼓励 Agent 先读 Issue、**起草验收标准 + 测试计划**贴评论；Owner 审阅认可后打 `ac-approved`。人从「作者」变「审批者」，吞吐更高。

满足三条才标 `agent-ready`：① 验收标准齐全（含测试要求）；② 已 `ac-approved`（人工审批通过）；③ 可独立完成。写不出验收标准、或未审批的 Issue 不能交给 Agent。

### Agent 循环

```text
1. 查 label=agent-ready、状态=Triaged 可领取、无 Assignee 的 Issue
2. 认领：设 Assignee（上锁）→ 拖 In Progress → 评论「开始」
3. 读验收标准 → 从 Issue「Create a branch」拉分支 → 写码 + 跑/补测试
4. 开 PR（描述写 Closes #issue）→ 自动转 In Review
5. 遇风险操作：先摆「确认三件套」，等人确认后再执行
6. 停。等 CI 绿 + 模块 Owner approve 合并；CI 挂了先自己修
```

Agent 必须在以下节点在 Issue 评论回写，便于人异步审计：认领开工、开 PR、请求确认、卡住退回。

### 确认闸：风险操作三件套

凡有副作用 / 难回退 / 影响生产的操作（部署、删改数据、schema 迁移、prod 配置、权限密钥、依赖大版本升级、跨服务 breaking change），Agent **不直接执行**，先在 Issue 评论摆清三样：

1. **要做什么** —— 具体命令 / diff
2. **影响范围** —— 动到哪些数据 / 环境 / 服务
3. **回退方式** —— 出错怎么撤回

**结构化批准（别靠口头「确认」）**：Agent 摆完三件套后打 `awaiting-confirmation`；**仅模块 Owner** 把它换成 `owner-confirmed` 才算放行。Agent 见到 `owner-confirmed` 才执行；没有 = 不做。

**部署用更强的原生闸**：部署 workflow 放进 GitHub **Environment + required reviewers = Owner**，Agent 触发后 GitHub 暂停等人批 —— 可审计、不依赖文字解析。

### Agent 绝对不做

- approve / 合并 PR（代码评审必须人工）
- 绕过 / 跳过 CI
- 未经确认执行上述风险操作

### 卡住退回

Agent 卡住 / 跑歪 / 测试连续修不好 → 评论说明卡点 → 拖回 Triaged → 清自己 Assignee → 打 `needs-human`，由模块 Owner 重排。

### 接入

GitHub MCP 或 `gh` CLI 均可，不强制工具。同一 Issue 同一时间只允许一个 Agent 接（靠 Assignee 上锁）。

---

## 🔗 Feature 追踪：Issue → Branch → PR

这是本规范的核心。每一个 Feature（`type: feature`）从 Issue 到代码合并，必须完整经过以下步骤，并在 **Issue 评论** 中维护追踪信息。

### 完整流程

```text
1. 创建 Issue
      │
      ▼
2. Triage：分配 Assignee、Priority、Milestone
      │
      ▼
3. 把设计与取舍写进 Issue 正文
      │
      ▼
4. 创建分支（feature/{issue-id}-{desc}）
      │
      ▼
5. 在 Issue 中更新追踪评论（branch）
      │
      ▼
6. 开发；设计有变动同步更新 Issue 正文
      │
      ▼
7. 提交 PR（关联 Issue）
      │
      ▼
8. Code Review
      │
      ▼
9. 合并 → 更新 Issue 追踪评论
```

### 步骤 3：把设计写进 Issue

设计与背后的取舍写在 Issue 正文里，不落成本仓库里的文件。设计定型的过程中直接改
Issue 正文——推导过程与产生它的讨论留在一处，也没有第二份副本需要同步。

开发开始前 Issue 正文该写哪些内容，见 [Issue 正文规范](#-issue-正文规范)。

### 步骤 4：创建分支

**分支命名规则：**

```
feature/{issue-id}-{short-desc}
```

分支名中的 `{issue-id}` 必须与 Issue 编号一致：

```bash
# 示例
git checkout -b feature/123-add-oauth-support
git checkout -b feature/456-batch-import-nodes
```

### 步骤 5：在 Issue 中更新追踪信息

开始开发后，必须在 Issue 中发一条评论，记录追踪信息（**每个 Issue 只需一条，后续更新此评论**）：

```markdown
## 📌 开发追踪

| 项目 | 内容 |
| --- | --- |
| **分支** | `feature/123-add-oauth-support` |
| **状态** | In Progress |
| **负责人** | @username |
| **预计完成** | YYYY-MM-DD |
```

> **说明**：PR 合并后将此评论的状态更新为 `Done`（需环境验证的任务先 `Validating`，验证通过再 `Done`），并补充 PR 链接。

---

## 📄 Issue 正文规范

Feature 的设计写在 Issue 正文里。没有 frontmatter，也没有需要维护的文档状态：Issue
自身的 Label 与看板列已经承载了状态，再加一个状态字段只会和它们越走越远。

### 建议章节

按需取舍，不适用的删掉——改一行的 Issue 不需要迁移方案。

````markdown
## 背景与目标

描述功能背景、用户痛点、本次开发要达成的目标。

## 方案设计

### 总体思路

简述整体实现方案。

### 交互设计（如涉及）

涉及 UI / 前端交互的功能，**必须附上设计稿链接（Figma 等）**，并简要说明关键交互。

- 设计稿：<链接>

### 接口变更（如有）

```http
POST /api/v1/{endpoint}
Content-Type: application/json

{
  "field": "value"
}
```

响应：

```json
{
  "id": "xxx",
  "status": "ok"
}
```

### 数据库变更（如有）

说明新增或修改的表/字段，并给出迁移脚本路径。

### 关键流程

用文字或伪代码描述核心逻辑流程。

## 验收标准

- [ ] 功能点 1
- [ ] 功能点 2
- [ ] 测试覆盖率达标

## 测试策略

说明需要补充的单元测试、集成测试或 AT 用例。

## 影响分析

- **向后兼容**：是/否，说明理由
- **依赖变更**：是/否，说明理由
- **性能影响**：说明（无则省略）

## 参考资料

- 相关 Issue/PR 链接
- 相关文档链接
````

### Bug 复杂分析（可选）

复杂 Bug（涉及多模块、需要根因分析）的分析同样写在 Issue 里：问题描述、根因分析、
修复方案、验证方式。

### 重大架构决策

变更涉及架构决策时，按以下几节写进 Issue，让推导过程留在产生它的讨论旁边：

- **背景** —— 促成这个决策的背景与约束
- **决策** —— 决定采用什么
- **原因** —— 为什么
- **后果** —— 收益，以及接受了哪些代价
- **替代方案** —— 权衡过但没有选的方案，以及为什么没选

---

## 🔀 PR 与合并流程

### 合并双闸（Branch Protection 强制）

合并到 `main` 必须同时满足两道硬闸，由 GitHub Branch Protection / Ruleset 物理强制，不靠自觉：

1. **CI 全绿** —— Required status checks 通过（GitHub Actions 跑测试）。
2. **人工 approve** —— 至少 1 个非作者 approve，且勾 **Require review from Code Owners**，即模块 Owner 必须 approve。

Agent 不能 approve、不能合并、不能绕过 CI。`main` 分支建议配置：

- Require a pull request before merging
- Require status checks to pass（勾 CI 的 job）
- Require approvals ≥ 1 + Require review from someone other than the author + Require review from Code Owners
- Require branches up to date before merging
- **Bypass 名单**：模块 Owner / maintainer 列入「允许绕过」（Branch Protection 的 bypass actors / Ruleset bypass list）

> **Owner 有 bypass 权限**：应急或可信场景下，Owner 可直推 / 绕闸合并 —— 这是信任通道，应少用，且事后在对应 Issue / PR 说明原因。**Agent 永远不在 bypass 名单内**；双闸对 Agent 和常规贡献者始终强制。

### PR 描述模板

PR 描述必须包含以下内容，以完成 Issue → Branch → PR 的关联：

```markdown
## 变更描述

简要描述本次变更内容。

## 关联信息

| 项目 | 内容 |
| --- | --- |
| **Issue** | Closes #123 |
| **分支** | `feature/123-add-oauth-support` |

## 变更类型

- [ ] Bug 修复
- [ ] 新功能
- [ ] 文档更新
- [ ] 重构

## 测试说明

描述如何验证本次变更（测试命令、手动验证步骤等）。

## 合并前检查清单

- [ ] Issue 正文与实际实现一致
- [ ] CHANGELOG.md 已更新（在 `[Unreleased]` 节）
- [ ] API 文档已更新（如有 API 变更）
- [ ] 测试已添加/更新，本地测试通过
- [ ] 无破坏性变更，或已在 CHANGELOG 中标注
```

### Code Review 文档检查项

Reviewer 在 Code Review 时需确认：

- [ ] 涉及 UI / 交互的变更：设计稿链接已附，且实现与设计稿一致
- [ ] Issue 正文与实际实现一致
- [ ] API 文档已同步更新
- [ ] CHANGELOG.md 已记录面向用户的变更

### 合并后操作

PR 合并后由合并者或 Assignee 完成：

1. 更新 Issue 追踪评论，将状态改为 `Done`（需环境验证的任务先 `Validating`，验证通过再 `Done`）并补充 PR 链接

---

## 📧 邮件通知流程

### 通知触发时机

| 事件 | 收件人 | 触发方式 |
| --- | --- | --- |
| 新正式版本发布 | 全体成员 + 外部订阅者 | CI 自动 / 手动 |
| RC 版本发布 | 内部测试团队 | CI 自动 / 手动 |
| 代码冻结开始 | 全体研发成员 | 手动（Release Manager） |
| Critical Bug 修复 | 受影响模块负责人 + 测试团队 | 手动 |
| Sprint 开始 | 本 Sprint 参与成员 | 手动（项目负责人） |

### 邮件模板

#### Release 发布通知

```
主题：[BKN Foundry] vX.Y.Z 正式发布

BKN Foundry vX.Y.Z 已正式发布！

## 主要变更

### ✨ 新增
- 功能描述（#Issue编号）

### 🐛 修复
- 修复描述（#Issue编号）

### ⚠️ Breaking Changes（如有）
- 描述变更及迁移方式

## 下载
- GitHub Releases: <链接>
- Images: `ghcr.io/openbkn-ai/<service>:vX.Y.Z` (per service)

## 完整变更日志
<CHANGELOG 链接>
```

#### RC 版本通知

```
主题：[BKN Foundry] vX.Y.Z-rc.N 测试版发布，邀请测试反馈

BKN Foundry vX.Y.Z-rc.N 已发布，请测试团队进行验证。

## 测试范围
- 变更列表（关联 Issue 链接）

## 已知问题
- （如有）

## 反馈方式
在 GitHub Issue 中提交问题，标注 milestone: vX.Y.Z

## 测试截止
YYYY-MM-DD
```

#### 代码冻结通知

```
主题：[BKN Foundry] vX.Y.Z 代码冻结通知

vX.Y.Z release 分支已创建，代码冻结自 YYYY-MM-DD 开始。

## 冻结规则
- ✅ 允许：Bug 修复、文档更新、版本号更新
- ❌ 禁止：新功能、重构、依赖升级

## 冻结期间合并申请
如需合并非 Bug 修复内容，请回复此邮件并说明原因，由 Release Manager 审批。

## 预计正式发布
YYYY-MM-DD
```

### 发送规范

| 规范项 | 要求 |
| --- | --- |
| 发送方式 | 团队邮件列表，重要 Release 可由 CI 自动触发 |
| 主题前缀 | 统一使用 `[BKN Foundry]`，便于过滤和归档 |
| 语言 | 中文为主，重要 Release 可附英文版 |
| 抄送 | **所有邮件必须抄送测试负责人**；Release 通知同时抄送项目负责人 |
| 附件 | 禁止附件，统一使用链接引用文档或制品 |

---

## 📚 相关资源

- [贡献指南](CONTRIBUTING.zh.md)
- [发布规范](RELEASE.zh.md)
- [测试规范](TESTING.zh.md)
- [架构规范](ARCHITECTURE.zh.md)

---

*最后更新：2026-08-19*
