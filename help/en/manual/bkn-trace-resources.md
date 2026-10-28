# OpenBKN resource and Agent / Trace capacity planning

Plan resources together with a workload model and acceptance targets. Registered agents,
active agents, retained traces, and projection backlog describe different dimensions.

## Full-platform installation requirements

[Full-platform installation requirements](https://github.com/openbkn-ai/bkn-foundry/blob/main/help/en/install.md)

## Convert agent activity into incoming work

- N: active agents that continue to issue business calls.
- f: business turns per agent per second.
- k: tool calls per turn; measure retries and fan-out separately.
- E: measured projection tasks per turn, including lifecycle, receipts, answer persistence,
  and asynchronous follow-up tasks.

```text
business turns/s = N × f
tool calls/s = N × f × k
incoming projection tasks/s = N × f × measured E + other background arrivals
```

E already includes the effects of the tools in a turn; do not multiply it by k again.
Calibrate it with representative first/reused conversations, successes, failures, retries,
evidence, and answer sizes. Tool-call counts do not always equal Trace list entries;
verify Trace ID and receipt grouping.

A capacity statement must include call frequency, content distribution, workload entry
point, and test duration. Agent count alone does not specify the workload.

## Resource budgets and deployment settings

| Component | Record | Observe during acceptance |
| --- | --- | --- |
| agent-observability | Replicas, CPU requests/limits, memory budget | Throttling, RSS, request latency, projection throughput |
| MariaDB | CPU/memory, buffer pool, connection pools, persistent storage | Physical reads, fsync, locks/deadlocks, pool waits, complete claim latency |
| OpenSearch | CPU/memory, heap, indexes/replicas, persistent storage | Heap/GC, indexing and refresh latency, query latency, disk usage |
| Dependencies | Evidence/Kafka, object storage, ontology queries, shared services | Their queues, errors, throughput, and resource contention |

Configuration sources: [Trace Chart](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/charts/agent-observability/values.yaml)
and [Core database pools](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/src/boot/bootstrap.go).
Defaults are deployment starting values, not certified capacity tiers. A code-fixed pool
cannot be changed through an unsupported Helm/environment setting. Extra replicas also
add database connections and workers, so validate them together.

Leave memory outside the OpenSearch heap for native allocations and file caches; see
[OpenSearch configuration guidance](https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/).
Size the MariaDB buffer pool against the database's actual memory budget and working set,
leaving room for connections, other caches, and the operating system. Dedicated-host
ratios must not be applied independently to every container on a shared host; see
[MariaDB memory guidance](https://mariadb.com/docs/server/ha-and-performance/mariadb-memory-allocation).

Budget LLM, embedding/reranking, ontology, and data-query compute, accelerators, and indexes
separately. A local Trace test or an unrestricted host process does not establish the
capacity of a complete production installation.
Specific experiment configurations, candidate resource values, and measured agent tiers
are retained in the [module README](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/README.md#issue-2585-开发环境测量记录)
(Chinese). Those observations are not product concurrency guarantees.

## Retention and storage

```text
daily business turns = N × f × 86400 × active duty cycle
daily tasks ≈ daily business turns × measured E
raw answer bytes/day ≈ daily turns × mean answer bytes
persistent storage ≈ measured daily growth × hot-retention days × replication factor
                     + backups/logs/maintenance space
```

Tasks do not equal permanent OpenSearch documents. Projections update existing documents,
answer acknowledgements can release large outbox bodies, and other history/facts may
remain. Pending counts or a synthetic row size cannot establish production bytes per Trace.

Measure SQL data/indexes, OpenSearch store, answers, and logs for a representative number
of business turns before selecting retention, archival, and cleanup policies.

## Backlog recovery and acceptance

```text
long-term stability: consumption μ stays above arrivals λ
approximate catch-up time: B / (μ - λ), only if μ > λ and rates are reasonably stable
```

Successful claims against a large backlog validate that dataset's claim path. With ID
ordering, old due tasks precede new ones; claim budgets and new-Trace search visibility
must be accepted separately.

- Apply production resource limits, expected history, representative contents, and
  simultaneous production/consumption.
- Separate request, complete claim, delivery ACK, and search-visibility latency/errors.
  Count timeouts separately instead of reporting only successful samples.
- Run steady periods that cover flushes, GC, retries, and burst recovery. Check backlog
  slope and whether the oldest pending age recovers.
- Reserve recovery headroom: equal arrival and consumption rates cannot absorb bursts.
- Exercise lease recovery, failure compensation, idempotency, and fencing. Retest multiple
  workers/replicas instead of assuming linear scaling.
- Completing a test does not establish that every load stage meets an SLO. Agree on
  freshness, error-rate, and recovery targets first.

Index migration, startup, and recovery constraints are documented in the
[module README](https://github.com/openbkn-ai/bkn-foundry/blob/main/bkn-trace/agent-observability/README.md#v040-升级与恢复约束)
(Chinese). Do not hide regressions by relaxing claim budgets or bypassing the migration ledger.
