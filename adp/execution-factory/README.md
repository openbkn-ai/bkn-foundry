# Execution Factory

[中文](README-zh.md) | English

Execution Factory is part of the BKN Foundry ecosystem. If you like this project, please give the BKN Foundry project a ⭐!

BKN Foundry is an open-source ecosystem for building, publishing, and running decision-intelligence AI applications. It uses ontology as the core method for business knowledge networks, with BKN Foundry as the core platform, aiming to provide flexible, agile, and reliable enterprise-level decision intelligence to further unleash the productivity of every member.

The BKN Foundry platform includes key subsystems such as ADP, Decision Agent, and AI Store.

## 📚 Quick Links

- 🤝 [Contributing Guide](../../rules/CONTRIBUTING.md) - Guidelines for contributing to the project
- 📄 [License](LICENSE) - Apache License 2.0
- 🐛 [Report Bug](https://github.com/kweaver-ai/operator-hub/issues) - Report issues or bugs
- 💡 [Feature Request](https://github.com/kweaver-ai/operator-hub/issues) - Propose new features

## Execution Factory Definition

Execution Factory is an open-source platform for managing and executing AI operators and tools, designed to bridge Large Language Models (LLMs) with real-world capabilities. By supporting the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/), it provides a standardized mechanism to register, manage, and execute various operators and tools, empowering developers to build powerful AI Agent applications rapidly.

## Core Components

Execution Factory ships as a single service, `operator-integration`, which exposes two API faces.

### Operator Integration (`operator-integration`)
The core integration service platform responsible for the full lifecycle management of operators and tools.
- **Operator Management**: Supports registration, versioning, publishing, and deprecation of operators.
- **Toolbox**: Enables grouping multiple tools into toolboxes for unified management and invocation.
- **MCP Support**: Acts as an MCP Server, providing standardized tool invocation interfaces for LLMs.
- **Multi-Protocol Adaptation**: Supports various communication protocols like HTTP and SSE.
- **Access Control**: Built-in policy-based access control mechanism.

#### API faces

| Prefix | Purpose |
| --- | --- |
| `/api/agent-operator-integration/v1` | Public API for operators, toolboxes, MCP servers and skills. |
| `/api/agent-operator-integration/internal-v1` | Service-to-service API. Cluster-internal; not for browser clients. |
| `/api/capabilities-lab/v1` | Capability face: flattens tools, MCP servers and skills into a single `capability` model. Previously a separate `capabilities-lab` service, merged in as a route group — the paths are unchanged. |

## Features

- **Standardized Interface**: Based on the MCP protocol, decoupling models from tools.
- **Flexible Extensibility**: Supports operators written in multiple programming languages (e.g., Go, Python).
- **Observability**: Integrated with OpenTelemetry for end-to-end tracing.
- **High Performance**: Built with Go, offering high concurrency processing capabilities.

## Quick Start

### Prerequisites
- Go 1.24+
- MySQL / MariaDB / Dameng DB
- Redis

### Build and Run

#### Run Operator Integration
```bash
cd operator-integration
# Install dependencies
go mod tidy
# Build
go build -o operator-integration server/main.go
# Run (requires appropriate configuration files)
./operator-integration
```

## Contribution

Pull Requests and Issues are welcome!

## License

[Apache-2.0](LICENSE)
