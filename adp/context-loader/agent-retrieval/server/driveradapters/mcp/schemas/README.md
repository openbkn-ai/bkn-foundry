# MCP tool schema configuration

Tool metadata and JSON schemas are maintained in this directory to make extension and LLM understanding easier.

## File specification

| File | Description |
|------|------|
| `tools_meta.json` | Tool meta information (name, description), add entries here for new tools |
| `{tool_key}.json` | Tool schema, including the `input_schema` and `output_schema` keys |

## Steps to add a tool

1. Add `{tool_key}: { "name": "...", "description": "..." }` in `tools_meta.json`
2. Add `{tool_key}.json`, containing two JSON Schema objects `input_schema` and `output_schema`
3. Register the tool in `app.go` (`loadToolMeta` and `loadToolSchemas` are already centralized, so `schemas.go` does not need to be changed)

## Switch-controlled tools

Some tools are not installed by default and need to be explicitly enabled before they appear in `tools/list` and `GET /mcp/info`:

| Tool | Environment variable | Default | Description |
|------|---------|------|------|
| `execute_skill` | `EXECUTE_SKILL_ENABLED` | Off | Sends the entry command into the sandbox for execution. This is the **master switch** for skill execution: it determines both whether the MCP tool is assembled and whether the `/kn/execute_skill` route is registered. When disabled, the deployment should look the same as if the capability were not compiled in; probes must not be able to distinguish "disabled" from "does not exist". The legacy name `MCP_EXECUTE_SKILL_ENABLED` is still recognized |

When adding this type of tool, the assembly logic in `app.go` and the catalog logic in `info.go` must skip it using the same predicate.
If the two places diverge, `/mcp/info` will advertise a tool that cannot be called.

## Description reference

For descriptions, refer to the "Tool Overview" and "Tool Reference" sections in `docs/releases/v5.0.4/tool-usage-guide.md`.
