# Issue 1086 Trace Runtime Log Projection Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make every persisted BKN Trace operation attempt queryable as a runtime log by Trace and Request ID, while preserving complete payload evidence only in the authoritative Trace stores.

**Architecture:** Add a versioned, denormalized `runtime_operation_log` document to the existing Core projection alias. Write it through the existing outbox on operation start and terminal transitions, and recreate it from MariaDB operation facts during an authoritative rebuild. A new OpenSearch log source searches this document with all correlation filters pushed down; Conversation audit remains a separate, non-Trace source.

**Tech Stack:** Go, MariaDB, OpenSearch, existing BKN Trace projection outbox, Go `testing` package.

---

### Task 1: Define the runtime-operation projection document

**Files:**

- Modify: `bkn-trace/agent-observability/src/domain/valueobject/sessionvo/projection.go`
- Modify: `bkn-trace/agent-observability/src/domain/valueobject/sessionvo/projection_test.go`

**Step 1: Write failing projection-document tests**

Add table-driven tests for a constructor receiving a Conversation, OperationCallFact, Receipt, and operation row version. Assert that the result contains owner scope, all correlation IDs, execution metadata, knowledge-network IDs, and a deterministic document identity; assert serialized JSON has no `input`, `output`, `error`, inline payload, or artifact payload body.

**Step 2: Run the focused test to verify it fails**

Run: `go test ./src/domain/valueobject/sessionvo -run TestRuntimeOperationLogProjection -count=1`

Expected: FAIL because the runtime-log projection type/constructor does not exist.

**Step 3: Implement the minimal value object**

Add `RuntimeOperationLogProjectionDocument` and `NewRuntimeOperationLogProjectionDocument`. Include `OperationID`, `Attempt`, `ReceiptID`, `Owner`, correlation identifiers, tool/protocol/module, timing/status/retryable, and `KnowledgeNetworkIDs`. Copy only envelope modes, byte counts, and artifact counts if needed for diagnostics; do not copy envelope bodies.

**Step 4: Run the focused test to verify it passes**

Run: `go test ./src/domain/valueobject/sessionvo -run TestRuntimeOperationLogProjection -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/domain/valueobject/sessionvo/projection.go \
  bkn-trace/agent-observability/src/domain/valueobject/sessionvo/projection_test.go
git commit -m "feat(trace): define runtime operation projection"
```

### Task 2: Emit runtime-log projection mutations on the lifecycle write path

**Files:**

- Modify: `bkn-trace/agent-observability/src/domain/service/sessionsvc/service.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/sessionsvc/service_test.go`

**Step 1: Write failing lifecycle tests**

Extend operation-attempt tests to inspect the projection outbox after an attempt is created and after it is completed/failed. Assert a `runtime_operation_log` mutation exists per `(operation_id, attempt)`, has the operation row version, and changes from pending to terminal without carrying payload bodies.

**Step 2: Run the focused test to verify it fails**

Run: `go test ./src/domain/service/sessionsvc -run 'Test.*RuntimeOperationLogProjection' -count=1`

Expected: FAIL because no runtime-log outbox mutation is emitted.

**Step 3: Implement lifecycle projection emission**

Add a small helper beside `appendProjection` that receives the current Conversation, Operation, OperationCallFact, and Receipt; constructs the document from Task 1; and calls `AppendProjection` with aggregate type `runtime_operation_log`, aggregate ID `<operation_id>:<attempt>`, and the current operation row version. Invoke it after the fact/receipt/operation have been saved on both start and terminal paths, including idempotent retry paths.

**Step 4: Run the focused test to verify it passes**

Run: `go test ./src/domain/service/sessionsvc -run 'Test.*RuntimeOperationLogProjection' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/domain/service/sessionsvc/service.go \
  bkn-trace/agent-observability/src/domain/service/sessionsvc/service_test.go
git commit -m "feat(trace): project runtime operation logs"
```

### Task 3: Rebuild runtime logs from authoritative MariaDB facts

**Files:**

- Modify: `bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/rebuild.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/store_integration_test.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc/service_test.go`

**Step 1: Write failing rebuild tests**

Seed two facts in one Conversation with distinct Trace IDs and receipts. Assert `ScanAuthoritativeProjection` yields runtime-operation-log items with owner scope and correct document identity, and `CountAuthoritativeProjection` includes them. Exercise a rebuild target and assert both runtime documents are validated before alias switch.

**Step 2: Run the focused test to verify it fails**

Run: `go test ./src/drivenadapter/dbaccess/mariadb/sessionstore ./src/domain/service/projectionrebuildsvc -run 'Test.*RuntimeOperationLog' -count=1`

Expected: FAIL because the authoritative scanner and count omit operation facts.

**Step 3: Implement the authoritative scanner**

Add `scanRuntimeOperationLogProjection` to join operation facts with their Conversation and Receipt, construct the Task-1 document, and emit `runtime_operation_log` projection items ordered by the compound operation-attempt identity. Add the aggregate type to the deterministic scanner order and include its count in `CountAuthoritativeProjection`. Do not add a database migration: all inputs already exist in Core tables.

**Step 4: Run the focused test to verify it passes**

Run: `go test ./src/drivenadapter/dbaccess/mariadb/sessionstore ./src/domain/service/projectionrebuildsvc -run 'Test.*RuntimeOperationLog' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/rebuild.go \
  bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/store_integration_test.go \
  bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc/service_test.go
git commit -m "feat(trace): rebuild runtime operation logs"
```

### Task 4: Add OpenSearch mappings for runtime correlation queries

**Files:**

- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection/sink.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection/sink_test.go`

**Step 1: Write failing mapping tests**

Parse the versioned index mapping and assert keyword-capable mappings for runtime document discriminator, operation ID, attempt, receipt ID, Trace ID, Request ID, Span ID, Conversation ID, Interaction ID, timestamps, owner scope, and knowledge-network IDs.

**Step 2: Run the focused test to verify it fails**

Run: `go test ./src/drivenadapter/httpaccess/opensearchprojection -run TestPrepareVersionDefinesMappingsRequiredByRuntimeOperationLogQuery -count=1`

Expected: FAIL because the runtime fields are not explicitly mapped.

**Step 3: Implement the mapping extension**

Extend the existing alias-version mapping rather than creating a second index. Use keyword subfields for exact correlation and scope filters, `date` for runtime timestamps, and a document discriminator such as `runtime_log_id`/`runtime_log_kind` to isolate runtime documents from Receipt and Conversation projections.

**Step 4: Run the focused test to verify it passes**

Run: `go test ./src/drivenadapter/httpaccess/opensearchprojection -run TestPrepareVersionDefinesMappingsRequiredByRuntimeOperationLogQuery -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection/sink.go \
  bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection/sink_test.go
git commit -m "feat(trace): index runtime log correlations"
```

### Task 5: Implement the Trace runtime-log source

**Files:**

- Create: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchruntimeaudit/source.go`
- Create: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchruntimeaudit/source_test.go`
- Modify: `bkn-trace/agent-observability/src/boot/bootstrap.go`

**Step 1: Write failing adapter tests**

Use a fake OpenSearch client with a runtime document. Assert `Search` projects an `operation.executed` `LogRecord` carrying all correlation fields and source scope. Decode the query body and assert exact OpenSearch filters for Trace ID, Request ID, Conversation ID, Interaction ID, Operation ID, Span ID, owner scope, and time range. Verify `Count` is the OpenSearch filtered total and `CountAccuracy` is exact.

**Step 2: Run the focused test to verify it fails**

Run: `go test ./src/drivenadapter/httpaccess/opensearchruntimeaudit -run TestSearch -count=1`

Expected: FAIL because the package does not exist.

**Step 3: Implement the source and wire it**

Implement a source with a distinct source ID (for cursor isolation) and `runtime.business` metadata. Require the runtime-document discriminator, push all supported filters down, set the event name to a registered runtime operation event, and create a compact summary from module/tool/status only. Wire it into `logSources` when Core projection is enabled.

Update operation-audit source selection/registered-event validation so this runtime event is eligible for an associated Trace drilldown without broadening unrelated log visibility.

**Step 4: Run the focused adapter and boot tests**

Run: `go test ./src/drivenadapter/httpaccess/opensearchruntimeaudit ./src/boot -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchruntimeaudit \
  bkn-trace/agent-observability/src/boot/bootstrap.go
git commit -m "feat(trace): expose runtime operation log source"
```

### Task 6: Make correlation filtering and counts truthful across sources

**Files:**

- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchconversationaudit/source.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchconversationaudit/source_test.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/logsvc/service.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/logsvc/service_test.go`

**Step 1: Write failing count-contract tests**

Add a Trace-ID query over a Conversation-only page and assert it contributes exact zero, never raw Conversation count. Add a service test where local record rejection occurs and assert `Count` equals visible candidates with `CountExact=false`; add a fully pushed runtime page and assert exact total survives pagination.

**Step 2: Run the focused tests to verify they fail**

Run: `go test ./src/drivenadapter/httpaccess/opensearchconversationaudit ./src/domain/service/logsvc -run 'Test.*(Trace|Count)' -count=1`

Expected: FAIL because Conversation projection is queried broadly and the service can retain a raw count after filtering.

**Step 3: Implement the truth-preserving contract**

Have the Conversation source return an exact empty page for Trace/Span/Interaction/Operation filters it cannot represent, rather than returning a broad page for local rejection. Update `logsvc` so any rejected source records invalidate its raw count and produce a visible lower bound with partial accuracy. Preserve exact totals when the source declares an exact, fully filtered result.

**Step 4: Run the focused tests to verify they pass**

Run: `go test ./src/drivenadapter/httpaccess/opensearchconversationaudit ./src/domain/service/logsvc -run 'Test.*(Trace|Count)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchconversationaudit \
  bkn-trace/agent-observability/src/domain/service/logsvc
git commit -m "fix(trace): align log correlation counts"
```

### Task 7: Lock the public contract and run module verification

**Files:**

- Modify: `bkn-trace/agent-observability/src/driveradapter/api/httphandler/log_handler_test.go`
- Modify: `bkn-trace/agent-observability/src/driveradapter/api/rdto/log.go` (only if a registered event/schema field requires it)
- Modify: `bkn-trace/agent-observability/docs/swagger/swagger.json` and generated contract tests if the public schema changes

**Step 1: Write failing end-to-end handler tests**

Construct a Trace runtime source plus Conversation source and call `GET /api/observability/v1/logs?trace_id=<id>`. Assert at least one runtime record, matching `trace_id`, `request_id`, `conversation_id`, `interaction_id`, and `operation_id`; assert another Trace from the same Conversation is absent; assert `count.value` matches final records and the response does not expose input/output/error bodies.

**Step 2: Run the handler test to verify it fails**

Run: `go test ./src/driveradapter/api/httphandler -run TestListLogsByTraceIDReturnsRuntimeOperationOnly -count=1`

Expected: FAIL until Tasks 1-6 are complete and wired.

**Step 3: Update public registration and documentation minimally**

Register the runtime event and only update generated OpenAPI/Swagger artifacts if the handler's externally visible contract changed. Keep endpoint paths and existing Conversation event shapes backward compatible.

**Step 4: Run full module verification**

Run: `go test ./...`

Expected: PASS for the `bkn-trace/agent-observability` module.

Run: `go vet ./...`

Expected: PASS with no diagnostics.

**Step 5: Commit**

```bash
git add bkn-trace/agent-observability/src/driveradapter/api \
  bkn-trace/agent-observability/docs/swagger
git commit -m "test(trace): cover runtime log drilldown"
```

### Task 8: Validate an alias rebuild on an integration deployment

**Files:**

- Modify only if needed: `bkn-trace/agent-observability/scripts/test_bkn_trace_e2e_lite_probe.py`

**Step 1: Prepare an isolated integration dataset**

Create one Conversation with two operation attempts whose Trace IDs and Request IDs differ, complete both attempts, and retain the original input/output evidence.

**Step 2: Trigger a versioned Core projection rebuild**

Run the configured deployment workflow with a new `BKN_TRACE_CORE_PROJECTION_REBUILD_VERSION`; wait for validation and alias switch. Do not delete the prior index until rollback retention policy allows it.

**Step 3: Run post-rebuild acceptance queries**

Query `/api/observability/v1/logs` once per Trace ID and Request ID. Confirm each response contains only the expected operation attempts, correlations match Trace detail, count matches records, and opening Trace detail still exposes full authoritative payload evidence.

**Step 4: Record outcome**

Attach the exact Trace IDs, response counts, and rebuild version to Issue #1086. If capacity differs materially from estimate, record index document count and byte growth before enabling rebuild in production.

**Step 5: Commit any repeatable probe change**

```bash
git add bkn-trace/agent-observability/scripts/test_bkn_trace_e2e_lite_probe.py
git commit -m "test(trace): verify rebuilt runtime log drilldown"
```
