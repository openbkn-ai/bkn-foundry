# 📘 BKN Foundry 文档

BKN Foundry 为**纯后台**平台，请通过 CLI、各语言 SDK 或 HTTP API 操作各子系统。

---

## 🚀 入门

**部署：** 完整安装以 **Linux** 为主；**macOS** 仅可选本机 kind 验证 — 见 [`deploy/dev/README.zh.md`](../../deploy/dev/README.zh.md)（[English](../../deploy/dev/README.md)）。

| 文档 | 说明 |
| --- | --- |
| [安装与部署](install.md) | 环境要求、`deploy.sh` 安装与安装后检查 |
| [快速开始](quick-start.md) | 从部署到首次 BKN 与智能体操作的端到端路径 |
| 📒 [Cookbook](cookbook/README.md) | 场景化、可复制可执行的操作手册 |

---

## 🧩 模块

按子系统组织的参考手册（位于 `./manual/`）。

| 文档 | 说明 |
| --- | --- |
| 📂 [数据源管理](manual/datasource.md) | 数据库连接、表发现、CSV 导入与生命周期 |
| 🧠 [模型管理](manual/model.md) | LLM、Embedding、Reranker 的注册与管理 |
| 🕸️ [BKN 引擎](manual/bkn.md) | 业务知识网络 — 对象类、关系类、行动类与实例 |
| 🗄️ [VEGA 引擎](manual/vega.md) | 数据虚拟化 — 连接、模型、视图与统一查询 |
| 📚 [Context Loader](manual/context-loader.md) | 面向智能体的上下文组装 |
| 🛠️ [Execution Factory](manual/execution-factory.md) | 工具、算子与技能 |
| 🔭 [BKN Trace](manual/bkn-trace.md) | 链路追踪、指标与证据链式可观测 |
| 🔐 [BKN Safe](manual/bkn-safe.md) | 身份、权限、策略与审计（启用时） |
| 🛡️ [平台管理](install.md#-完整安装后的管理员命令openbkn-admin) | `openbkn admin` — 用户/组织/角色/模型/审计（完整安装后） |

---

## 💬 交流群

<img src="../qrcode.png" width="200" alt="BKN Foundry 交流群二维码" />

> 💡 CLI 安装：`npm install -g @openbkn/bkn-sdk`；平台管理已内置在同一 CLI 的 `openbkn admin` 子命令中。更详细的集群运维说明以随产品提供的部署文档为准。
