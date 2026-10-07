# 015 to 020 Historical Conversion Validation

Scope: Foundry #2012 / #2030, one-time offline data and projection conversion.
There is no migration-specific change under `agent-observability/src/`, and no
historical query, API or UI compatibility branch.

## Conversion against the backed-up local instance

The frozen source contains 3734 records: 106 Audit rows and 3628 Evidence outbox
rows. It was reused without changing or deleting source records or archives.

The corrected conversion and terminal-projection run is
`~/.bkn/upgrades/015-to-020-historical/run-20261007T020015-da02b7ce/report.md`.
The subsequent unchanged-input repeat is
`~/.bkn/upgrades/015-to-020-historical/run-20261007T083507-0b1fd698/report.md`.
Both finish with `complete=true`, `state=completed` and no unconverted rows.
The latest repeat completes all four database-free native structure/payload
preflights before the first import and verifies
17899 unique Core records in four dependency-complete transport batches, with
zero new Core records and zero created or updated aggregates.

| Measure | Verified result |
| --- | ---: |
| Source records | 3734 |
| Audit records converted and read back | 106 |
| Evidence source records converted and read back | 3628 |
| Unconverted records | 0 |
| Native Evidence aggregates | 3504 |
| Original observed events retained in those aggregates | 3628 |
| Derived native receipt terminal-projection events | 3628 |
| Converted Core Conversations | 3504 |
| Converted Core Interactions | 3511 |
| Converted Core Operations / Receipts / call facts, each | 3628 |
| Native Core records imported and read back | 17899 |
| Core projection documents, including existing records | 17260 |
| Repeat: already verified Audit records | 106 |
| Repeat: already verified Core records | 17899 |
| Repeat: already verified aggregates | 3504 |
| Repeat: created / updated aggregate documents | 0 / 0 |
| Repeat: conflicting aggregates | 0 |
| Source rows with field defaults, truncation or identifier conversion | 3674 |

Source-row counts and derived native records are different measures. Neither
Core rows nor terminal-projection events inflate the 3734-record input count.

## Necessary native target conversion

Missing HTTP status defaults to 200 for stored successes, 500 for failures and
403 for denials; recognized stored HTTP status names map to their numeric code.
The stored result is preserved. Oversized labels are truncated to native limits;
oversized object identifiers map to stable type/hash identifiers. Originals and
field transformations remain in the private snapshot and per-record report.

The old outbox has 808 distinct Trace IDs reused across multiple requests and
contexts. The tool splits those contexts into 3504 deterministic native Trace
identities, updates all related identities together, and creates native Core
records through the ordinary store. Field defaults include completed observation
when no terminal result was stored, unknown authentication and MCP protocol.
Captured failures remain failed; missing full question/answer remains absent.
Captured observation payload remains available in native call details.

Each native Evidence aggregate retains every original observation and includes
its converted receipt's ordinary `retrieval.completed` projection. This is the
same data representation used by the existing Core projection builder. It fixes
explicit Trace-ID queries, which prefer stored Evidence over Core projections,
without adding any query fallback. Previously written aggregates can be updated
only if their complete body matches a known representation derived from the
same frozen source; updates also require OpenSearch sequence/primary-term CAS.

## Unmodified native query qualification

A local acceptance checker uses the unchanged native query libraries and the
instance's actual MariaDB/OpenSearch targets with Administrator scope. It does
not change the running query service or install a historical reader.

| Query | Verified result |
| --- | ---: |
| Ordinary log search: each converted Audit ID returned once | 106 |
| `audit.admin` filter: the same converted Audit IDs returned once | 106 |
| Native aggregate reads, terminal/root summaries and request-chain reachability | 3504 |
| Evidence events returned, original plus terminal projections | 7256 |
| Explicit Trace-ID terminal/root, conversation and business graph samples | 12 |
| Native conversation list total, including existing data | 3614 |
| Native Trace list total, including existing data | 4268 |

Log records have nonempty actor/object snapshots and actions. Aggregate counts
match native normalization. Native list totals match native database identities
for the same September 1 to October 7 query range. A narrower September 12 range
correctly excludes earlier history; its smaller count is not a migration loss.

## 8081 page verification and remaining release work

The existing Trace detail page successfully reads historical Trace
`0019705187e8a7e733143149ea6d6eb4`: `completed`, tool
`bkn.schema.object_type.get`, request
`req_01a0a498-2187-7c0f-9afb-42a1e3a154bd`, and a completed MCP operation with the
captured business-reference payload. The displayed historical time is September
15, 2026. Missing technical Spans and full question/answer are shown as unavailable.

The ordinary log page displays September history when the September 1 to
October 1 range is selected; filtering Execution Factory shows 48 records.
The audit workbench with the same month displays 107 management records
(106 converted source Audit records plus one pre-existing native record),
including the same September 27 toolbox updates and September 12 operations.
Native Audit comparison above verifies exact IDs;
the page's overall count also includes ordinary Core conversation projections
and pre-existing logs, so it is not the 106-row source Audit count.

## Merged-image record loading checks — October 7, 2026

Foundry #2030 merged at 74a9ade79aed. EE #230 was approved at its exact
head, passed its required checks and merged at 6f935e2b3251. Its ordinary
main ARM64 image is deployed at 8081:

swr.cn-east-3.myhuaweicloud.com/openbkn-ai-ee/agent-observability-ee:0.2.0-main.20261007170552.sha6f935e2

Image digest:
sha256:1f2b852cb81585f3705899ab746499e2d0456a9f90acc112ce9c81b556b62763

The EE packaging change pins Core to merged Foundry and includes the existing
offline command; it adds no historical runtime reader. The deployed service is
Ready with no restarts. The actual engineer entrypoint completed with exit 0:

~/.bkn/upgrades/015-to-020-historical/run-20261007T091801-72628e89/report.md

It verified all 3734 input rows with zero unconverted rows, all 17899 Core
records and 3504 aggregates. The unchanged-input rerun created or updated none.
The unchanged native query checker passed again after this final deployment:
106 Audit IDs in ordinary logs and audit.admin, 3504 aggregates, 7256 events and
12 business graph samples.

With September 1 through October 1 selected in the existing page controls:

- Logs list 48 Execution Factory records; the next page loads September 2 rows.
  The earlier transient OAuth-unavailable response did not recur in this final
  deployment check.
- Audit lists 107 management records (106 converted plus one existing).
  The September 27 20:05:37 toolbox update matches the log page on actor,
  object, result, POST and request req_9adfa453-7926-4db5-bb65-a38668b479b7.
  Its source snapshot has an object identifier but no toolbox name.
- Trace e0bb6468563156cb7e282edec34a6897 opens normally: completed,
  ontology-query, bkn.object.query, September 18 10:30:52, request
  req_630e55f1-2ddd-433c-95dd-f97c8a13fc0d. The call details retain
  query_hash, the inventory object reference and row_count 500.
- Enterprise business provenance now loads the September ontology-query
  conversations. Conversation conv_req_630e55f1-2ddd-433c-95dd-f97c8a13fc0d
  opens its timeline, showing one successful call and 500 returned rows.
  It has the same operation as Trace:
  op_58048934a279bea69c8e36e829d1145efc1f692fcebdbac5.

Missing full question/answer, Spans or duration remain unavailable. No new Agent
interpretation was generated for the historical evidence-chain tab. The sampled
query Trace has no matching management log; conversion does not invent that link.

A separate existing log-pagination display issue was observed: page two changes
the displayed total from 48 to 28 while loading its records. Full source-ID
verification still passes. This acceptance does not change online pagination.

Screenshots and the private final Markdown report are retained under the local
backup's acceptance-20261007 directory. No source/backup/archive was deleted.

## Development verification

- 135 Python tests pass, including missing status, oversized fields, reused
  Trace/request/operation identities, native terminal projection, exact prior
  aggregate/CAS protection and publication failure followed by fresh readback.
  Added regressions cover preflight-before-write, valid owner-context splits,
  native action-type network scope, dependency-complete size-bounded batches,
  and a rejected later native batch preventing all imports. Go command tests
  verify database-free preflight and rejection of invalid native payloads.
- `go test ./...`, `go vet ./...` and `go build ./cmd/...` pass for
  agent-observability.
- `make license-check` passes, including all four added Go files.
- CI-matching `golangci-lint` 2.12.2 reports zero issues.
- `git diff --check` passes; the net runtime-source migration diff is empty.

Engineer instructions: [upgrade guide](../../deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md).


## Supplementary Agent content and relationship validation (2026-10-07)

The original SQL export covered Audit and observation outboxes, but omitted
Agent thread/checkpoint/task sources. The offline upgrade now captures those
sources before an operator-supplied cutover timestamp. The product runtime and
frontend remain unchanged.

September input: 106 Audit, 3,628 observations, 27 Agent threads and 119 tasks
(3,880 source records). Supporting inputs are 69 checkpoint versions, 47 message
blobs and four Agent definitions. Versions are not counted as user rounds.

The retained histories contain 22 distinct user messages in 22 threads. Five
threads have no retained messages. Tasks have no retained parent-thread IDs.
Conversion produces 146 Agent conversations/interactions, 151 operations,
151 receipts, 151 call facts and 260 interaction-level original-text Artifacts.
The five additional calls are two `search_schema`, two `list_skills` and one
`get_kn_detail`. Two have original tool results; three lack retained results.
Missing results remain visible as native partial/failed records with a reason,
not discarded source rows or invented answers. Message-level tool-call IDs join
only their corresponding ToolMessage within the same original user round.

Final checkpoint message order defines user rounds. Starts use first message
observations; ends use the last content-change observation, excluding unchanged
checkpoint repetitions. Regression checks cover inserted tools, revised answers
and exclusion of post-cutover messages.

Native relation checks distinguish conversation, interaction, operation and
Span. A two-round regression fixture remains one conversation with two
interactions; tool calls add operations, not user rounds. Original outboxes
contain 808 reused Trace IDs across multiple request contexts, converted to
3,504 native request-context aggregates. This is an explicit grouping loss:
source-to-target mappings preserve original Trace/request/Span IDs. It must not
be described as the source containing only one request per Trace.

All 3,628 observation rows carry identical nonempty inner/outer Span IDs. The
converter also supports rows retaining the Span ID only in the inner event.
The live SS4O technical index has no September Spans by startTime or timestamp;
three September sample Spans belong to a separate test index. No technical Span
tree is generated from observations or Agent messages. Agent sources contain
no original Trace-ID matches with these outbox records, so they are not joined
by time or Agent name.

Local run `run-20261007T113710-b281a676/report.md` completed with 3,880 verified
source records and zero unconverted records. Original 17,899 Core rows and
3,504 aggregates were unchanged. The Agent supplement verified 745 Core rows
and 260 Artifacts; four aggregates were updated by an exact prior-conversion
comparison to include the five recovered tool calls. Core projection rebuild
verified 17,854 documents. A preceding qualification imported the 15 new child
operation/receipt/call-fact rows; its aggregate conflict was resolved by that
exact baseline, not an unrestricted overwrite.

Unchanged native queries verified all 146 Agent interactions using the same
technical access view as the enterprise page. At 8081:

- Conversation `396f3837-e00e-44bf-ac87-1267d26e0d2c` displays the full original
  question/result, original Agent name, one round and 3.2 seconds.
- Conversation `a1295718-216e-4079-ba1b-24df5149d92e` displays its original question,
  one interaction and three calls: `agent.run`, `search_schema`, `get_kn_detail`.
  Missing final answer/tool results match the retained source messages.
- Its native Trace is `c6f65dfdf09c17810945abdc9a34fb48`; interaction is
  `int_d152302893b398fcf3f7d9c5309d35f0d204a59196043f57`. Both child operations
  point to `op_8bdd9526b128a6c1575adc87112efccd2203b3258c1c75a2` and share that
  interaction and Trace, rather than creating two additional rounds.

Qualification used a locally built offline executable. The running official
image remains EE `sha6f935e2`. Final release acceptance still requires approved
Foundry/EE package updates, official-image deployment, and repeat native/page
verification. Issue #2012 remains open until then.

Repeat Agent qualification verified 745 Core rows, 260 Artifacts and 146
aggregates with zero created/updated/conflicting records.

Checks: 148 Python tests, full Go service tests, Go lint (zero issues), native source/store readback,
`git diff --check`; no net changes under the online service `src/` relative to
merged #2030.
