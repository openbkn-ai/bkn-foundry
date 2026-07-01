---
issue: "#86"
branch: "feature/86-mcp-i18n"
module: "context-loader"
status: "draft"
author: "@kerrykuang2023"
created: "2026-07-01"
pr: ""
---

# Feature #86: MCP instructions and tool description i18n

## Background And Goals

Agent Retrieval exposes MCP instructions, tool metadata, and JSON Schema
descriptions to LLM clients. These strings are part of the model-facing
product surface: they guide tool choice, parameter filling, and result
interpretation.

The current implementation registers MCP server instructions and tools at
service startup. Tool names, descriptions, and schema descriptions are embedded
as static Chinese text. This blocks English agents and creates the same product
risk in the experimental Execution Factory capability library, where HTTP/API,
MCP, Skill, Function, and ADP package fields also need business-oriented
explanations.

This change implements a first, low-risk step:

- keep the current Chinese behavior as the default;
- add deployment-level locale selection for MCP strings;
- add English bundles for server instructions, tool metadata, and schema
  descriptions;
- document how Execution Factory Lab should reuse the same bundle-and-overlay
  model for capability creation and editing guidance.

## Design

### Locale Selection

MCP tools are registered once when `NewMCPHandler` creates the server. Because
`WithInstructions` and `AddTool` are static in the current MCP server lifecycle,
this implementation uses deployment-level locale selection rather than
per-request language switching.

Locale resolution checks the following environment variables in order:

1. `MCP_LOCALE`
2. `X_LOCALE`
3. `LANGUAGE`
4. `LC_ALL`
5. `LANG`

Supported values:

- `zh`, `zh-CN`, `zh_CN`, `zh-Hans` -> `zh-CN`
- `en`, `en-US`, `en_US` -> `en-US`

Unknown values fall back to `zh-CN`.

### Bundle Structure

The default Chinese bundle remains the existing embedded source of truth:

- `serverInstructions` in `app.go`
- `schemas/tools_meta.json`
- `schemas/*.json`

English localization is additive:

```text
server/driveradapters/mcp/schemas/locales/en-US/
  instructions.txt
  tools_meta.json
  schema_descriptions.json
```

`schema_descriptions.json` is an overlay keyed by tool id and dotted JSON path.
Only the `description` strings are replaced; the actual schema shape stays in
one canonical file per tool. This avoids duplicating large JSON Schemas per
locale and reduces drift when parameters change.

### Execution Factory Lab Reuse

Execution Factory Lab should use the same concepts for capability guidance:

- keep one canonical capability schema/configuration model;
- keep user-facing labels, helper text, placeholders, and examples in locale
  bundles;
- support product-facing terms such as Capability, Tool, MCP, Skill, Function,
  ADP package, and Execution Unit through a shared glossary;
- use lightweight creation dialogs and full editing pages, both drawing field
  guidance from the same localized bundle.

This matters because the experimental capability library is where users choose
between HTTP/API, MCP, Skill, Function, and package import. The UI should not
hardcode Chinese explanations or expose raw technical field names without
business guidance.

## API Changes

No HTTP API changes.

## Compatibility

Backward compatible. Existing deployments continue to return the current
Chinese MCP strings unless an English locale is explicitly configured.

## Acceptance Criteria

- [x] MCP server instructions can be loaded from an English locale bundle.
- [x] MCP tool names and descriptions can be loaded from an English locale bundle.
- [x] MCP JSON Schema descriptions can be overlaid from an English locale bundle.
- [x] Unknown locale values fall back to the current default Chinese behavior.
- [x] Execution Factory Lab reuse guidance is documented.
- [x] Per-request MCP locale negotiation is explicitly deferred.
- [x] REST/OpenAPI YAML localization is explicitly deferred unless a separate
      documentation generation pipeline is introduced.

## Testing Strategy

- Unit test locale bundle loading and schema description overlay.
- Run the MCP package test with the module's Go toolchain version.

## Future Work

- Add per-request locale negotiation if MCP clients consistently provide
  `Accept-Language` or an explicit locale header during initialize/list-tools.
- Add a shared capability glossary bundle for Execution Factory Lab and Studio.
- Decide whether REST/OpenAPI documentation should be generated per locale or
  published as a single default language per deployment.
