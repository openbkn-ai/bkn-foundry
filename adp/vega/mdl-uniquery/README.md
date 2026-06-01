
# mdl-uniquery

## 项目概述

mdl-uniquery是一个强大的统一查询统计服务，用于提供多维度数据查询、统计分析和可视化功能。该服务支持多种数据源的统一访问，包括指标数据、事件数据、日志数据和追踪数据等。

## 功能特性

- **多模型支持**：提供Metric Model、Event Model、Trace Model、Objective Model等多种数据模型的查询能力
- **统一数据视图**：支持创建和管理数据视图，实现多源数据的统一展示
- **DSL查询**：支持通过领域特定语言进行复杂的数据查询
- **PromQL查询**：提供Prometheus查询语言的支持
- **权限管理**：内置权限控制机制，确保数据安全访问
- **可扩展架构**：采用清晰的分层架构，易于扩展和维护

## 系统架构

系统采用典型的六边形架构设计，主要包含以下层次：

- **驱动适配层(driveradapters)**：处理外部请求，提供HTTP API接口
- **业务逻辑层(logics)**：实现核心业务逻辑和数据处理
- **接口定义层(interfaces)**：定义各组件间的接口契约
- **驱动适配层(drivenadapters)**：与外部系统和数据源交互
- **公共组件(common)**：提供通用工具、中间件和配置

## 目录结构

```
├── api_doc/          # API文档
├── docker/           # Docker相关配置
├── gen/              # 自动生成的代码
├── helm/             # Helm部署配置
├── server/           # 主服务代码
│   ├── common/       # 公共组件
│   ├── config/       # 配置文件
│   ├── drivenadapters/ # 驱动适配器
│   ├── driveradapters/ # 驱动适配器
│   ├── errors/       # 错误定义
│   ├── interfaces/   # 接口定义
│   ├── locale/       # 国际化资源
│   ├── logics/       # 业务逻辑
│   ├── main.go       # 程序入口
│   └── version/      # 版本信息
├── test.http         # HTTP测试文件
```

## 快速开始

### 前提条件

- Go 1.16+ 环境
- 相关依赖服务（根据具体配置）

### 本地开发

1. 克隆代码库
```bash
git clone <repository-url>
cd mdl-uniquery
```

2. 安装依赖
```bash
cd server
go mod download
```

3. 配置环境

修改 `server/config/uniquery-config.yaml` 中的配置项，根据实际环境进行调整。

4. 启动服务
```bash
cd server
go run main.go
```

## 测试

### API测试

使用 `test.http` 文件中的测试用例进行API测试，可以通过Visual Studio Code的REST Client插件或其他HTTP客户端工具执行。

### 单元测试

运行单元测试：
```bash
cd server
go test ./...
```

## 部署

### Docker部署

```bash
docker build -t mdl-uniquery:latest -f docker/Dockerfile .
docker run -p 8080:8080 mdl-uniquery:latest
```

### Kubernetes部署

使用Helm进行部署：
```bash
helm install mdl-uniquery ./helm/mdl-uniquery
```

## 文档

API文档位于 `api_doc/` 目录，包含：
- uniquery-for-metricmodel.html/json
- 视图数据查询.html/json

## 技术栈

- Go语言
- RESTful API
- OpenSearch/Kafka（根据配置）
- Prometheus（可选集成）

## 许可证

[根据项目实际许可证填写]
