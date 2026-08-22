# BKN Trace

[English](README.md)

[![许可证](https://img.shields.io/badge/license-OpenBKN-blue.svg)](../LICENSE-OPENBKN.txt)

BKN Trace 是 OpenBKN 的社区版 Trace Core。它记录受管 Agent、SDK 与 MCP 调用的一手执行
事实，并提供受保护的技术 Trace 分析 API 以及与平台日志的精确关联能力。

## 记录的事实

Operation 的一次 attempt 是权威执行单元。已完成或失败的 attempt 可以保留 Conversation、
Interaction、Operation 与 Request 标识，执行时间和状态，调用身份、协议和工具信息，以及调用
边界实际收到的输入、输出或可诊断错误。记录 Trace 不得改变业务工具原本的返回结果。

BKN Trace 只记录一手事实，不从最终回答反推执行信息。未记录的信息必须明确为“未记录”，不能由
读取端或界面猜测补全。

## 公开社区能力

- 接收并持久化受管调用事实及关联 Evidence 事件。
- 提供受访问控制保护的 Trace、Conversation、Interaction、Operation、Evidence 和技术日志读取
  API。
- 返回技术执行详情：记录的输入/输出或错误、Trace 与 Span 关系，以及用于日志下钻的精确关联
  标识。
- 在返回 Trace 或 Evidence 数据前执行 Access Profile 和记录范围校验。
- 为 OpenBKN 客户端保留稳定的社区 Trace 事实模型与公开 API 合同。

## 架构

```text
受管 MCP / SDK 调用
        |
        v
Trace 生产者采集一手执行事实
        |
        v
BKN Trace Core
  - 生命周期与 Operation 调用事实
  - Evidence 与技术关联
  - 访问控制读取 API
        |
        +--> 技术 Trace 分析
        +--> 精确日志下钻
```

Core 是技术事实服务：不从最终回答推导业务语义，也不建立第二套权威 Trace 存储。

## 组件

| 路径 | 职责 |
| --- | --- |
| `agent-observability/` | Go Trace Core 服务、OpenAPI 接口、存储适配器和部署 Chart。 |
| `otelcol-contribute-chart/` | 用于 OTLP 采集及 OpenSearch 导出的 OpenTelemetry Collector Contrib Chart。 |
| `scripts/` | 可重复执行的合同与部署安全检查。 |

本地开发和服务配置请见
[agent-observability/README.md](agent-observability/README.md)；Collector 的部署与验证请见
[otelcol-contribute-chart/README.md](otelcol-contribute-chart/README.md)。

## 验证

在 Foundry 仓库根目录运行 BKN Trace 许可证头检查：

```bash
python3 bkn-trace/agent-observability/scripts/check_license_headers.py
```

在服务目录运行测试：

```bash
GOCACHE=/tmp/openbkn-go-build-cache GOMODCACHE=/tmp/openbkn-go-mod-cache go test ./...
```

## 许可证

BKN Trace 使用 [OpenBKN License](../LICENSE-OPENBKN.txt)。每个由 OpenBKN 编写且可添加注释的
源码与部署文件均带有版权声明及 `SPDX-License-Identifier: LicenseRef-OpenBKN` 标识。
