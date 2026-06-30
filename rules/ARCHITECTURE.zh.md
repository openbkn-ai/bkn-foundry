# BKN Foundry 架构设计规范（ARCHITECTURE）

中文 | [English](ARCHITECTURE.md)

本文件为 BKN Foundry 的架构设计规范。**日常研发只需要阅读第 1～2 章**；附录仅用于查术语与复制示例。

## 1. 架构规范

### 1.1 分层与依赖

- **Foundry（无 UI）**：Foundry 不包含 UI/Web Console/Portal/BFF；对外仅提供 **API/SDK** 与管理 API。
- **产品依赖**：产品通过 Foundry 的 Public API 调用 Foundry；禁止反向依赖（Foundry 不得依赖产品）。
- **组件可选性**：能力组件默认可选，必须支持启用/禁用；组件禁用时调用方需优雅降级（见第 2 章检查清单）。

```mermaid
flowchart LR
  Product[产品] --> Foundry[Foundry]
```

### 1.2 后端服务（禁止页面专属 BFF）

- **允许**：确有领域模型/事务/规则/数据归属的产品域服务。
- **禁止**：
  - 仅为单一页面/视图做字段拼装/转发/映射的后端（页面专属 BFF）
  - 为每个微应用拆分后端微服务
- **调用路径**：调用方直连 **Foundry Public API**，或通过统一 API Gateway 承载鉴权透传与必要协议适配；Gateway **禁止**演化为“每页面/每模块一个 BFF”。

新增后端服务前必须回答：

- 是否有持久化领域数据与一致性/事务要求？
- 是否有必须在服务端执行且无法复用平台能力的权限/合规策略？
- 是否有长期演进的领域模型（不是临时拼接）？
- 是否被多个产品复用且不属于 Foundry 能力？

若都否 → 不新增服务。

### 1.3 API 规则（Foundry Public API 必须向下兼容）

- **API 分层**：Public / Internal / Experimental
  - 跨组件依赖只允许 Public；Internal/Experimental 不得跨组件引用
- **Foundry 向下兼容**：
  - Foundry 的 Public API **必须向下兼容**（同一 major 内禁止破坏性变更）
  - 任何被产品调用的 Foundry 接口，一律按 Public API 管理
- **版本策略（HTTP）**：只使用 URL major（`/api/v1` → `/api/v2`）
  - 同一 major 仅允许：新增字段（可选/默认语义）、新增 endpoint、扩展枚举（客户端容忍未知值）
  - 破坏性变更：只能新增 `/api/v2`，并提供 deprecation window（例如 2 个 release 或 90 天）
- **契约规范**：
  - HTTP：OpenAPI 3.1（统一错误模型 + 分页/过滤/排序）
  - Skill：Claude Skills（tool/function calling），需声明权限/租户/审计与输入/输出 schema

- **兼容性定义（必须满足）**：
  - **输入兼容（Request/Input）**：老客户端/老调用方缺字段、旧字段值仍可处理；不得把可选字段改为必填。
  - **输出兼容（Response/Output）**：允许新增字段；不得删除/重命名既有字段；调用方必须忽略未知字段。
  - **行为兼容（Behavior）**：同名接口语义稳定；不得“同名不同义”。

- **Skill 也必须兼容（Claude Skills）**：
  - 只要某个 Skill 被产品使用，就按 **Public** 管理，并遵守向下兼容。
  - **name 稳定**：`name` 一经发布不得修改（改名视为新 Skill）。
  - **schema 兼容**：
    - `input_schema` 只允许新增可选字段/新增枚举值（调用方容忍未知值）；不得删除字段、不得把可选改必填。
    - `output_schema` 只允许新增字段；不得删除/重命名既有字段。
  - **破坏性变更**：只能通过新 `version`（必要时新 `name`）并提供 deprecation window。
- **变更要求**：API 变更必须附带 ADR（架构决策记录）+ OpenAPI diff（breaking 检测）+ contract test（关键 endpoint）

### 1.4 服务数量预算（强制）

- **Foundry**：后端微服务数量 **< 5**

统计口径：

- 计入：可独立部署/伸缩、拥有独立运行时与发布节奏的后端服务（Foundry 内部服务）
- 不计入：数据库/缓存/消息中间件等基础设施；仅用于本地开发的 mock

执行方式（最小要求）：

- 新增/拆分后端服务时，同步更新“服务清单”（仓库现状为准），并在 PR 描述中给出 Foundry 服务计数
- CI 至少包含一个统计检查点（超预算需显式豁免）

豁免条件（必须记录）：

- 仅允许短期豁免，并同步提交收敛计划与时间表

## 2. 强制检查清单（必读）

- **依赖方向**：产品调用 Foundry；禁止反向依赖
- **Foundry 无 UI**：Foundry repo 不出现 React/Vue/静态资源/页面路由/Web Console
- **可选组件**：禁用可选组件后系统仍可启动；调用方优雅降级
- **API**：OpenAPI 更新 + breaking 检测通过 + deprecation/迁移说明 + contract test
- **后端新增**：不得为页面专属 BFF；若新增服务必须符合 1.2 的判定问题
- **后端**：不得为每个微应用或每个页面新增后端微服务；新增后端必须是产品域服务
- **预算**：Foundry < 5；服务清单与计数同步更新

---

## 附录：术语与示例（按需）

### A.1 术语表（扩展）

- **页面专属后端 / 页面专属 BFF**：仅服务某一个页面/路由/视图的后端服务；主要做字段拼装/转发/权限过滤以支撑该页面。

### A.2 OpenAPI 示例（最小片段）

```yaml
openapi: 3.1.0
info:
  title: Knowledge Query Public API
  version: 1.2.0
paths:
  /api/v1/knowledge/queries:
    get:
      summary: List queries
      parameters:
        - in: query
          name: page
          schema: { type: integer, minimum: 1, default: 1 }
      responses:
        "200":
          description: OK
        "401":
          description: Unauthorized
        "500":
          description: Internal Server Error
```

### A.3 Skill 示例（Claude Skills / tool calling）

```yaml
---
name: knowledge.query
version: 1.0.0
stability: public
description: "Query the knowledge network and return structured results."
---

# X SKILL
```
