# BKN Reference Architecture

English | [中文](bkn-architecture.zh.md)

![BKN reference architecture](images/bkn-architecture.svg)

## Access layer

- **User** — the end user, interacting with the platform through its UI.
- **App / Agent** — programmatic callers, integrating through APIs or skills.
- **BKN Studio** — the user-facing entry point: a visual console for humans.
- **BKN Skill** — the platform-level skill layer, wrapping SDK capabilities for reuse by apps and agents.
- **BKN SDK / CLI** — the unified access interface: one project covers both integration and administration.

## BKN Engine (Business Knowledge Network Engine)

- **Context Loader** — retrieval capability, composed of **Retrieval** (recall) and **Ranker** (ordering).
- **BKN** — the Business Knowledge Network, describing the business through four elements: **Data / Logic / Risk / Action**.
- Concepts reach the execution layer through **mapping**.

## Execution layer & data

- **VEGA** — data virtualization, hiding differences between underlying data sources.
- **Exec Factory** — the execution factory, orchestrating tools, MCP, and Skills.
- **Multi-source & multi-modal data** — the underlying data sources consumed by the execution layer.

## Cross-cutting capabilities (covering BKN Engine and below)

- **BKN Safe (access control)** — the unified entry point for identity, permissions, and policy; enforces security controls and auditing per business object / action.
- **BKN Trace (evidence chain)** — traces BKN call chains (intent → knowledge node → data source → mapping / operator); traceable and explainable.
