# Trace summary capacity verification

Run these opt-in checks only against dedicated local test instances. The workload
creates and drops a randomly named database and, when configured, randomly named
OpenSearch indices. It never selects an existing application database/index.
The DB administrator must have create/drop privileges. Use an isolated account.

```sh
export BKN_TRACE_CAPACITY_ADMIN_DSN='root@tcp(127.0.0.1:13350)/?parseTime=true'
export BKN_TRACE_CAPACITY_OPENSEARCH_URL='http://127.0.0.1:19250'
export BKN_TRACE_CAPACITY_REPORT='/tmp/summary-capacity.json'
make test-summary-opensearch-capacity
```

`make test-summary-capacity` requires only MariaDB. It runs identity/cursor and
operation HTTP checks, and clearly reports OpenSearch as unmeasured when its URL
is absent. The OpenSearch target requires both settings. Its endpoint must be a
dedicated loopback HTTP instance; this harness does not configure production auth.
No container lifecycle is hidden in the test: start/stop dependencies separately.

## Coverage

The ordinary `make test-integration` CI job also runs a five-identity service
window regression using `BKN_TRACE_TEST_MARIADB_DSN`. It checks pre-cursor totals,
empty-page count fallback, and page equivalence to the legacy query, with unique
fixture IDs and cleanup. It requires neither capacity settings nor OpenSearch,
and applies no latency threshold.

- 2,202 identities, including 2,100 service matches: empty first source modules,
  tied timestamps, retries, duplicate receipts, a descendant service, and foreign
  owners. All 2,100 matches traverse 11 HTTP cursor pages without truncation.
- 23 boundary comparisons against a frozen pre-change query reference: total,
  ordering, cursor, exhausted/deep offsets, default/explicit limits, time ranges,
  missing services and ownership. These run without requiring OpenSearch.
- 84/112 operations with 256 KiB each for input/output/error: real HTTP summary
  size/field checks, default/explicit full compatibility and matching 401/404
  authorization behavior. The summary SELECT excludes all large payload columns.
- With OpenSearch: production handler, service, receipt/evidence/artifact and span
  statistics adapters over real HTTP. Synthetic receipts/spans are seeded directly
  using the production receipt mapping. Projection identities, returned trace
  counts, span counts, root service and actual outgoing request bodies are checked
  or recorded. Empty evidence/artifact indices still execute real searches.

Each latency case uses 3 warmups and 40 serial samples. The four service/limit
combinations are interleaved and reversed on alternate rounds. Reports include
HTTP/identity/projection timings, real OpenSearch request counts and SQL EXPLAIN.
The separately timed legacy count/page SQL is explicitly labeled as a reference;
production service queries use the window plan in `legacy_boundary_parity`.
Percentiles from separate phases must not be added. The test fails correctness
checks; latency budget flags are reported separately because shared-host timings
are unsuitable as an unconditional CI gate.

## Recorded result

[Sanitized measurements](summary-capacity-results.json) retain all three stable
pre-change and paired A/B results, plus the final production-path sample arrays.
The fixture used MariaDB 11.4.13 and OpenSearch 2.19.4 on ARM64, with 512 MiB DB
and 2 GiB OpenSearch memory limits, 512 MiB JVM heap, and no CPU limit.

The original service/no-service P95 ratios were 2.211, 1.716 and 2.037; the
criterion was not consistently met. Paired A/B service P95 changed from
41.872/41.685/41.773 ms to 26.680/24.513/21.856 ms. Candidate/control ratios were
1.384/1.288/1.210, all below 2. Final actual-production-path P95 was 26.974 ms
versus 22.059 ms control (1.223x), below the 1.5 s absolute budget. Every case
made four real OpenSearch requests per HTTP request, restricted to selected IDs.

84/112 operation HTTP summaries were 22,537/30,266 bytes, below 1 MiB. Payloads
remain available through full reads. Summary size still grows with operation
count and metadata size; these samples do not establish an unlimited size bound.

## Limits

Measurements use one client on a shared developer host, not production capacity
sizing. Trusted identity headers are injected by the harness. OAuth/ingress,
Kafka ingestion, EE aggregation, browser rendering, concurrent writes and much
larger datasets are outside this workload. Nonempty service count/page now share
one statement snapshot; the old path used two autocommit snapshots, and the
empty-page count fallback still uses two statements. No-service behavior, schema,
indexes, cache, read models and candidate limits are unchanged.
