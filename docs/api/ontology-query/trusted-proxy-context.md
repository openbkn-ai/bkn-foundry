# Ontology Query trusted proxy context

This contract is cluster-internal. Public ontology-query requests never accept a
proxy identity, proxy version, downstream target, or operation from the client.
Ontology Query derives them from the current published `main` model after the
real caller's knowledge-network permission check succeeds.

## Effective principal

Restricted downstream requests use the managed proxy app as the effective
principal:

```text
x-account-id: <proxy app id>
x-account-type: app
```

The real caller is carried separately for audit only:

```text
x-bkn-caller-id: <real caller id>
x-bkn-caller-type: <real caller type>
x-bkn-kn-id: <knowledge network id>
x-bkn-child-type: object_type | relation_type | metric | action_type | logic_property
x-bkn-child-id: <published child id>
x-bkn-proxy-version: <positive mapping version>
x-bkn-target-type: resource | tool_box | mcp
x-bkn-target-id: <published target id>
x-bkn-operation: view_detail | query_data | execute
x-bkn-execution-id: <optional action execution id>
traceparent: <W3C trace context>
```

The allowed combinations are:

| Child | Target | Operation |
| --- | --- | --- |
| `object_type` | `resource` | `view_detail` or `query_data` |
| `relation_type`, `metric` | `resource` | `query_data` |
| `logic_property` | `tool_box` | `execute` |
| `action_type` | `tool_box` or `mcp` | `execute` |

## Internal dependencies

- Proxy and current-binding resolution: `POST /api/bkn-backend/in/v1/knowledge-networks/{kn_id}/proxy-account/resolve`
- Vega schema read: `GET /api/vega-backend/in/v1/proxy/resources/{resource_id}/schema`
- Vega data read: `POST /api/vega-backend/in/v1/proxy/resources/{resource_id}/data`
- Tool-backed logic property: `POST /api/agent-operator-integration/internal-v1/tool-box/{box_id}/proxy/{tool_id}`
- Tool action: `POST /api/agent-operator-integration/internal-v1/tool-box/{box_id}/proxy/{tool_id}`
- MCP action: `POST /api/agent-operator-integration/internal-v1/mcp/proxy/{mcp_id}/tool/call`

The BKN resolver rebuilds the current published binding set and returns a
mapping only when that exact model version is synchronized. It must be available
to trusted ontology-query traffic after the caller's business PEP and must not
require the ordinary business caller to hold knowledge-network `authorize`.
Vega and the execution integration remain the final PEPs for the proxy app's
current account state and target operation.

Action execution records persist the proxy subject, mapping/model versions, and
downstream permission snapshot for internal rechecks and audit. Public execution
and action-log responses redact those internal fields; internal endpoints retain
the complete snapshot for trusted execution workloads.

Missing or incomplete caller context, missing/disabled proxy mappings, non-ready
permission synchronization, model-version mismatch, invalid binding tuples, or
downstream permission dependency failures are fail-closed. No path may retry as
the caller or as a service administrator.
