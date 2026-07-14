# 🔭 BKN Trace

## 📖 Overview

**BKN Trace** provides **full-chain observability**: ingest OTLP traces, export to search backends, and query spans linked to agent and platform activity via the **agent-observability** service.

Every agent conversation generates a trace capturing the full reasoning chain — from user message through context retrieval, tool invocation, and response generation. Traces are the primary mechanism for understanding *why* an agent produced a particular answer and *where* the supporting data came from.

Typical ingress prefix:

| Prefix | Role |
| --- | --- |
| `/api/agent-observability/v1` | Trace query and observability APIs |

**Related modules:** platform-wide logging and metrics pipelines (feed-ingester integration).

## 💻 CLI

`openbkn agent trace` takes a **conversation id** (from chat output or `openbkn agent sessions <agent_key>`).

### Retrieving Traces

```bash
# Full trace for a conversation
openbkn agent trace <conversation_id>

# Pretty-printed with indentation and color
openbkn agent trace <conversation_id> --pretty

# Compact single-line JSON output (for piping to jq)
openbkn agent trace <conversation_id> --compact
```

### Trace Data Structure

A trace returned by `openbkn agent trace` contains the following top-level structure:

```json
{
  "trace_id": "tr-abc123",
  "conversation_id": "conv-xyz789",
  "agent_id": "agt-001",
  "started_at": "2026-04-14T10:30:00Z",
  "duration_ms": 4520,
  "status": "completed",
  "spans": [
    {
      "span_id": "sp-001",
      "parent_span_id": null,
      "name": "agent.turn",
      "type": "agent",
      "started_at": "2026-04-14T10:30:00Z",
      "duration_ms": 4520,
      "status": "ok",
      "attributes": {
        "user_message": "What were Q1 revenues?",
        "agent_id": "agt-001"
      },
      "children": ["sp-002", "sp-003", "sp-004"]
    },
    {
      "span_id": "sp-002",
      "parent_span_id": "sp-001",
      "name": "context.retrieval",
      "type": "retrieval",
      "duration_ms": 820,
      "status": "ok",
      "attributes": {
        "kn_id": "kn-ecommerce",
        "query": "Q1 revenues",
        "results_count": 12,
        "sources": ["ot-orders", "ot-invoices"]
      }
    },
    {
      "span_id": "sp-003",
      "parent_span_id": "sp-001",
      "name": "tool.execute",
      "type": "tool",
      "duration_ms": 1200,
      "status": "ok",
      "attributes": {
        "tool_id": "op-sql-query",
        "input": {"sql": "SELECT SUM(amount) FROM orders WHERE quarter='Q1'"},
        "output": {"total": 1250000}
      }
    },
    {
      "span_id": "sp-004",
      "parent_span_id": "sp-001",
      "name": "llm.generate",
      "type": "llm",
      "duration_ms": 2100,
      "status": "ok",
      "attributes": {
        "model": "gpt-4",
        "prompt_tokens": 1850,
        "completion_tokens": 320,
        "temperature": 0.1
      }
    }
  ]
}
```

### Evidence Chain Analysis

Traces enable **evidence chain analysis** — tracing every claim in an agent's response back to its data source:

```bash
# 1. Pull the trace for a conversation (conversation_id from chat output or `agent sessions`)
openbkn agent trace conv-xyz789 --pretty

# 2. Walk the evidence chain:
#    agent.turn → context.retrieval (kn-ecommerce, 12 results from ot-orders)
#              → tool.execute (SQL query returned total=1,250,000)
#              → llm.generate (synthesized response from retrieved context + tool output)
```

### Span Types

| Span Type | Description |
| --- | --- |
| `agent` | Top-level agent turn (root span) |
| `retrieval` | Context retrieval from BKN / knowledge network |
| `tool` | Tool or operator invocation via Execution Factory |
| `llm` | LLM inference call (prompt → completion) |
| `memory` | Memory read/write operations |
| `policy` | Policy evaluation (permission checks, guardrails) |

### Interpreting Spans

Key attributes to look for in each span type:

**Retrieval spans:** `kn_id`, `query`, `results_count`, `sources` — tells you which knowledge network was queried, what the search query was, and which object types contributed results.

**Tool spans:** `tool_id`, `input`, `output`, `status` — shows exactly what was sent to each tool and what came back. If `status` is `error`, the `error` attribute contains the failure reason.

**LLM spans:** `model`, `prompt_tokens`, `completion_tokens`, `temperature` — useful for cost analysis and debugging generation quality. High token counts may indicate context bloat.

---

## 📘 TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// Fetch all spans for a conversation
const spans = await bkn.trace.spans('conv-xyz789');

// Walk spans
for (const span of spans) {
  const indent = span.parentSpanId ? '  ' : '';
  console.log(
    `${indent}${span.name} (${span.type}) — ${span.durationMs}ms — ${span.status}`,
  );

  if (span.type === 'retrieval') {
    console.log(`    query: ${span.attributes.query}`);
    console.log(`    results: ${span.attributes.resultsCount}`);
    console.log(`    sources: ${span.attributes.sources}`);
  } else if (span.type === 'tool') {
    console.log(`    tool: ${span.attributes.toolId}`);
    console.log(`    input:`, span.attributes.input);
    console.log(`    output:`, span.attributes.output);
  } else if (span.type === 'llm') {
    console.log(`    model: ${span.attributes.model}`);
    console.log(
      `    tokens: ${span.attributes.promptTokens}+${span.attributes.completionTokens}`,
    );
  }
}

// Evidence chain analysis
const retrievalSpans = spans.filter((s) => s.type === 'retrieval');
const toolSpans = spans.filter((s) => s.type === 'tool');

console.log('\n--- Evidence Sources ---');
retrievalSpans.forEach((rs) => {
  console.log(`Knowledge Network: ${rs.attributes.knId}`);
  console.log(`  Object Types: ${rs.attributes.sources}`);
});
toolSpans.forEach((ts) => {
  console.log(`Tool: ${ts.attributes.toolId}`);
  console.log(`  Output:`, ts.attributes.output);
});
```

---

## 🌐 curl

```bash
# Get trace for a conversation
curl -sk "https://<access-address>/api/agent-observability/v1/traces/conv-xyz789" \
  -H "Authorization: Bearer $(openbkn token)"

# Search traces by agent ID and time range
curl -sk -X POST "https://<access-address>/api/agent-observability/v1/traces/search" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "agt-001",
    "since": "2026-04-01T00:00:00Z",
    "until": "2026-04-14T23:59:59Z",
    "limit": 20
  }'

# Get spans for a specific trace
curl -sk "https://<access-address>/api/agent-observability/v1/traces/tr-abc123/spans" \
  -H "Authorization: Bearer $(openbkn token)"

# Get a single span's details
curl -sk "https://<access-address>/api/agent-observability/v1/traces/tr-abc123/spans/sp-003" \
  -H "Authorization: Bearer $(openbkn token)"

# Search traces by status (find failed agent turns)
curl -sk -X POST "https://<access-address>/api/agent-observability/v1/traces/search" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "error",
    "since": "2026-04-13T00:00:00Z",
    "limit": 10
  }'

# Health check
curl -sk "https://<access-address>/api/agent-observability/v1/health" \
  -H "Authorization: Bearer $(openbkn token)"
```
