# BKN Trace API 文档

- [OpenAPI JSON](./swagger/swagger.json)
- [OpenAPI YAML（统一发布入口）](../../../docs/api/agent-observability/agent-observability.yaml)

接口文档由代码注解生成。JSON 与运行时生成代码保留在模块内，供服务内置 Swagger 使用；对外发布的 YAML 统一存放在仓库根目录 `docs/api/`，并由同一生成命令同步和校验，禁止人工维护副本。

BKN Trace 的需求、设计与实施计划统一维护在 `bkn-docs/docs/foundry/bkn-trace/`。
