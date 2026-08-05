# MCP 工具 Schema 配置

工具元信息与 JSON Schema 统一在此目录维护，便于扩展与 LLM 理解。

## 文件规范

| 文件 | 说明 |
|------|------|
| `tools_meta.json` | 工具元信息（name、description），新增工具在此添加条目 |
| `{tool_key}.json` | 工具 Schema（含 `input_schema` 与 `output_schema` 两个键） |

## 新增工具步骤

1. 在 `tools_meta.json` 中添加 `{tool_key}: { "name": "...", "description": "..." }`
2. 添加 `{tool_key}.json`，包含 `input_schema` 与 `output_schema` 两个 JSON Schema 对象
3. 在 `app.go` 中注册工具（`loadToolMeta` 与 `loadToolSchemas` 已统一封装，无需修改 `schemas.go`）

## 开关控制的工具

个别工具默认不装配，需要显式开启后才出现在 `tools/list` 与 `GET /mcp/info`：

| 工具 | 环境变量 | 默认 | 说明 |
|------|---------|------|------|
| `execute_skill` | `MCP_EXECUTE_SKILL_ENABLED` | 关 | 把入口命令送进沙箱执行，是工具面唯一的命令执行通道。未开启时与「没编译进来」无异——不能让「未开启」和「不存在」在探测者眼里长得不一样 |

新增此类工具时，`app.go` 的装配与 `info.go` 的目录都要按同一个判定跳过，
两处不一致会让 `/mcp/info` 广播一条调不通的工具。

## 描述参考

描述建议参考 `docs/releases/v5.0.4/tool-usage-guide.md` 中的「工具总览」与「工具参考」章节。
