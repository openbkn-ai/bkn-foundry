# BKN 参考架构

[English](bkn-architecture.md) | 中文

![BKN 参考架构](images/bkn-architecture.zh.svg)

## 接入层

- **用户**：终端使用者，经界面与平台交互。
- **应用 / Agent**：程序化调用方，经接口或技能接入。
- **BKN Studio**：用户交互入口，面向人的可视化操作界面。
- **BKN Skill**：平台级技能层，封装 SDK 能力供应用 / Agent 复用。
- **BKN SDK / CLI**：统一接入接口，一个项目即可完成接入与管理。

## BKN Engine（业务知识网络引擎）

- **Context Loader**：检索能力，含 **Retrieval**（召回）与 **Ranker**（排序）。
- **BKN**：业务知识网络，以**数据 / 逻辑 / 风险 / 行动**四要素描述业务。
- 概念经**映射**下达到执行层。

## 执行层与数据

- **VEGA**：数据虚拟化，屏蔽底层数据源差异。
- **Exec Factory**：执行工厂，调度工具、MCP 与 Skills。
- **多源 & 多模态数据**：底层数据源，供执行层接入。

## 横向能力（覆盖 BKN Engine 及以下）

- **BKN Safe（权限管控）**：统一身份、权限与策略入口，按业务对象 / 动作做安全管控与审计。
- **BKN Trace（证据链）**：追踪 BKN 的调用链路（意图 → 知识节点 → 数据源 → 映射 / 算子），可追溯、可解释。
