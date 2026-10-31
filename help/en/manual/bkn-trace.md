# 🔭 BKN Trace

## 📖 Overview

**BKN Trace** provides **full-chain observability**: ingest OTLP traces, export to search backends, and query spans linked to agent and platform activity via the **agent-observability** service.

Every agent conversation generates a trace capturing the full reasoning chain — from user message through context retrieval, tool invocation, and response generation. Traces are the primary mechanism for understanding *why* an agent produced a particular answer and *where* the supporting data came from.

Typical ingress prefix:

| Prefix | Role |
| --- | --- |
| `/api/agent-observability/v1` | Trace query and observability APIs |

**Related modules:** platform-wide logging and metrics pipelines (feed-ingester integration).

See [resource and Agent / Trace capacity planning](bkn-trace-resources.md) for workload sizing and acceptance boundaries.

For retained 0.2.0 installations, see [Evidence enablement and explanation acceptance](bkn-trace-evidence-enablement.md), including topic-record preservation and explicit regeneration after repair.

## Community and EE API boundary

Community exposes authorized recorded facts and technical Trace reads. Native
enterprise provenance requires the target Trace service to contain and assemble
the EE implementation, pass runtime entitlement, and authorize the current
identity's record scope. All paths below use `/api/agent-observability/v1`:

| Endpoint | Scope |
| --- | --- |
| `GET /interactions/{id}`, `GET /interactions/{id}/operations` | Core recorded facts |
| `GET /interactions/{id}/business-graph` | Authorized projection of Core's recorded assembly revision; not native EE detail or explanations |
| `GET /traces`, `GET /traces/{trace_id}` | Core technical execution reads |
| `GET /business-provenance/conversations`, `GET /business-provenance/interactions` | EE business search |
| `GET /business-provenance/interactions/{id}` | EE native business-resolution detail |
| `GET /business-provenance/interactions/{id}/explanations` | Read-only EE explanation lookup: `ready` or `not_generated` |

Community does not mount these EE routes; this boundary already exists in 0.2.0.
A plain 404 alone cannot identify a missing record, missing license, or gateway
fault: the EE availability gate can return the same plain 404. Core's structured
`resource_not_disclosed` means the requested record cannot be disclosed in the
current scope. A successful Core read does not establish access to an EE route.

Another EE service, a certificate, or an administrator role does not establish
that the target Trace image contains the EE implementation. Clients should keep
authorized basic facts visible and report the enterprise view as unavailable or
undisclosed. Explanation GET does not generate content; generation is a separate,
explicit EE POST, never an implicit panel-open or availability probe. Nonempty
enterprise acceptance needs existing real records and actual authorization;
`completed` or nonempty Operations cannot substitute for a `ready` explanation.

The former trace/request Evidence Chain, Business Graph, and Evidence Node paths
are not part of the currently mounted enterprise read contract. See the
[module README](../../../bkn-trace/agent-observability/README.md#community-与-ee-读取边界2517)
for the complete boundary and diagnostic guidance.

## Technical Trace page availability

For ordinary `GET /traces` pages without content, root-service or root-tool filters, MariaDB continues to select authorized IDs, order, total and cursor. When OpenSearch returns successfully but omits a selected Trace, the service batch-reads only the missing page IDs from canonical receipt metadata (at most 200 IDs, with a three-second fallback deadline). Status, timing and canonical root/error metadata use the authorized Trace lifetime. Missing Span statistics use `span_count_status=unavailable`; their numeric placeholder must not be displayed as a measured zero.

This does not change the page's completeness state merely because Span statistics are unavailable. Existing projection truncation remains explicit. Shared-network discovery, content/root filters and exact-ID detail reads retain their existing paths. OpenSearch request failures, SQL failures and missing canonical records still fail explicitly. Page reads do not write projections, replay dead letters or certify that historical projections have recovered.

## Managed Interaction completion limits

With durable answer handoff enabled, Core commits the answer task and business
completion in one SQL transaction. `execution_status=completed` releases the
conversation for its next turn; `evidence_status=assembling` means evidence is
still being delivered. A lost response is not a completion acknowledgement:
retry the same finish idempotently, without repeating business tools.

Question artifacts at MCP start retain their synchronous, best-effort path.
Their I/O shares the existing one-second Trace pre budget with lifecycle calls;
the per-upload timeout and retries do not each receive a fresh pre budget.
If capture fails after start commits, the response preserves the authoritative
Interaction ID and reports `lifecycle_recorded=true`, evidence `recorded=false`
and `continue_business_without_trace_retry`. An artifact-endpoint outage is
reported as `evidence_store_unavailable`; this identifies the failed capture
path, not a diagnosis of the underlying storage service. Lifecycle Core outages
retain `trace_core_unavailable`. Explicitly unconfigured ingest authentication
is an operator defect and is not retried. No artifact reference is fabricated,
and this question path has no durable background recovery guarantee.

### Interaction finish acknowledgement

Finish preparation retains the one-second observation budget. The authoritative
Interaction terminal POST/replay uses a separate total budget: 5000 ms by default,
configured by `observability.lifecycle.finish_timeout_ms` or
`BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS` (1–30000 ms; invalid values use 5000).
At most three identical POSTs, including backoff, share that budget. Authorized
submission can finish within it after caller disconnect; tool-attempt receipt
finalization and Core's Interaction lease rules do not change.

A finish response confirms success only when Core returns the matching Interaction
ID and requested terminal status. A timeout, exhausted transient failures or invalid
ACK returns MCP `isError=true`, `interaction_finish_unconfirmed`,
`required_action=retry_same_finish`, plus stage, cause and request correlation.
HTTP 200 alone is not an acknowledgement. Retry the original finish with the same
Interaction/outcome/answer; do not repeat business actions. Replaying the same POST
lets Core validate the committed closure, rather than treating a status-only GET as
proof that a different answer was accepted. Domain refusals, including an already
abandoned Interaction, remain errors and are never overwritten.

No durable client-side retry queue is added. Persistent Core failure or process exit
can still leave an unaccepted finish active until lease expiry; the product does not
claim eventual completion without Core acceptance or fabricate a terminal state.
This change does not identify the historical 8081 timeout's storage/CPU root cause.

### Scheduling and time budgets

Each Core instance uses one shared projection-worker slot. It claims one ready
task when that slot is free, consumes ready work continuously, and polls again
after the configured interval only when empty or after a worker error. The
default 1s interval is not a one-task-per-second throughput limit. A slow answer
still delays later evidence tasks in that slot; a deferred retry does not keep
the slot occupied. Other projection handlers retain their existing behavior.
Answer claims commit metadata and the lease without reading the body. The body
is then read under the same answer-task deadline as delivery, fenced by the
current lease. A body-read timeout follows normal counted retries and backoff,
allowing later ready tasks to proceed; exhausted attempts use the existing DLQ.

| Setting | Default | Meaning |
| --- | --- | --- |
| `core.projection.leaseIOTimeout` | `2s` | SQL metadata/lease claim I/O deadline |
| `core.projection.answerTaskIOTimeout` | `10s` | One answer attempt, including SQL body loading, sequential storage calls and SQL settlement |
| `core.projection.answerAckIOTimeout` | `2s` | Independent result/retry/dead-letter recording deadline |
| `core.projection.lockDuration` | `30s` | Outbox task lease; separate from the business Interaction lease |

All durations must be positive. Claim + answer + ACK + a 1s safety margin must
be strictly shorter than the lease; invalid startup configuration is rejected.
The default total is 15s inside a 30s lease. Increasing the lease alone does not
increase throughput and delays recovery after a crash. Timeouts bound I/O;
they are not end-to-end latency guarantees under CPU, database or storage load.
These answer deadlines do not redefine the runtime limits of enterprise or
revision-input handlers sharing the worker.
If another registered handler needs longer execution, its requirements must
also fit the shared lease; this change does not add lease renewal. An explicitly
configured startup projection rebuild completes before normal worker consumption.
The business Interaction's existing lease and renewal rules are unchanged;
background evidence delivery does not keep a completed Interaction active.

### Size and admission

- A complete encoded answer artifact is at most **16 MiB**, including metadata;
  this is not a 16 MiB plain-text allowance. Its owner/task wrapper also consumes
  space. The task limit is 16 MiB + 64 KiB.
  The managed-finish request envelope is bounded to 17 MiB, including when new
  admissions are off so an already committed full-body request can replay.
  Other lifecycle endpoints retain their existing 2 MiB body limit.
- Core checks the encoded task and protocol allowance against
  `max_allowed_packet` on the inserting SQL transaction. A database configured
  for 16 MiB cannot be assumed to accept a full 16 MiB artifact plus its wrapper.
  An insufficient budget returns a non-retryable storage-limit error and rolls
  back finish. The operator must resolve the limit before retrying the original
  finish; the product does not silently truncate answers.
  The current SQL check reserves 4 KiB for protocol/fixed values and separately
  counts task metadata; the effective limit is the smallest applicable budget.
- The durable handoff requires the default prepared-statement path; leave
  `interpolateParams` absent or false in the Core DSN. Enabling durable handoff
  with client-side SQL interpolation is rejected because SQL escaping changes
  the wire-size budget.
- New capture requires a known, permitting server policy. A disabled policy
  uses the existing server-owned closure reason without staging new content;
  an unknown policy is not treated as disabled. Already committed replay and
  accepted background work do not become new capture admissions.
  A disabled handoff gate rejects new answer tasks with an operator configuration
  action; rewriting the answer or automatically retrying cannot enable the feature.

### Assembly, backlog and recovery

The default assembly deadline is **5 minutes**. A periodic check can record
partial evidence after it expires; this is not a promise of complete evidence
within five minutes. Late delivery can create a subsequent evidence revision
without changing the completed business outcome.

Automatic retries default to 10 attempts and a 24h retry-age boundary. Neither
is a retention limit: dead-letter content is retained, and lease reclamations
are not the same as counted delivery failures. Accepted tasks survive process
restarts. Successful delivery releases the outbox answer body only after the
artifact is acknowledged and SQL evidence state is settled; pending/dead/DLQ
content remains recoverable. Delivered answers rely on the artifact store.

There is no global pending-byte quota or certified fixed QPS. Budget unfinished
body storage as arrival rate × average encoded task size × outage duration;
dead-letter copying can approximately double that body storage. Leave room for
existing data, undo, indexes and configured logs. For example, 1 MiB tasks at
one per second accumulate about 3.52 GiB of bodies in one hour without delivery.
Capacity and waiting time also depend on other work sharing SQL and the worker.
Shared CPU, SQL and storage pressure can also affect list/detail response times.
Per-task/claim byte limits do not bound process-wide memory or request concurrency.

Both durable-answer gates default off. Upgrade all Core workers before enabling
Core and then Context Loader. Before reverting Core binaries, reconcile accepted
work and retain compatible workers and database packet settings. Never repeat
completed business work to fill an evidence gap.

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

For result evidence gaps, `current_record_integrity.missing[]` includes an optional
`drop_reason` copied from the recorded publish outcome. `publisher_unavailable`
means the publisher could not accept the result event; `queue_full` means its queue
was full. The call can still be `completed` while its result evidence is missing.
An omitted `drop_reason` means no applicable publish failure was recorded.

When the ontology-query evidence publisher is explicitly enabled, invalid startup
configuration stops the service and identifies the failing setting. Temporary
policy, heartbeat or ACK failures use the existing background retries; logs identify
the producer and failed step, suppress repeated identical errors, and report recovery.

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
| `tool` | Tool invocation via Execution Factory |
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
