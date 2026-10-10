# Bound opaque Evidence envelope mappings

**Goal:** Let valid producer envelopes project without consuming a dynamic field mapping for every arbitrary payload key.

**Design:** The Ledger remains authoritative. Core projection stores the complete JSON envelope in `_source`; existing top-level query fields and legacy envelope mappings remain unchanged. Add `envelope: {type: object, dynamic: false}` to both the new-version mapping and compatible existing-alias bootstrap patch. No new service, index rebuild, field-limit increase, authorization change, or historical repair.

**Alternatives:** Raising the field limit postpones arbitrary growth; flattening or removing envelope content loses the exact source needed by projection validation. Disabling the object wholesale would also disable legacy mapped fields. None is necessary.

**Limits:** Existing typed-field conflicts and already dead outbox rows remain unchanged. Fresh ingestion must be verified through the existing 8081 instance after release; unit HTTP fixtures do not prove live OpenSearch behavior.

## Execution

- [x] Observe three meaningful mapping regressions fail on baseline: PrepareVersion, new bootstrap, existing alias bootstrap.
- [x] Assert full opaque payload survives projection and `_source` validation without flattening or redaction.
- [x] Add only the two compatible envelope mapping definitions; retain alias sequence and existing query mappings.
- [x] Focused regression package, full Core `go build ./...`, `go test ./...`, `go vet ./...`, and golangci-lint pass. Changed projection package passes `go test -race ... -count=1`.
- [ ] Full-module race: existing Swagger parallel tests race inside `swag.Spec.ReadDoc`; reproduced on unchanged baseline. This independent failure is not covered up or repaired in the mapping change.
- [x] Independent review found no blocking issues.
- [ ] Official release and fresh 8081 real-consumer projection acceptance.

Additional acceptance reproduction on the existing 8081 OpenSearch (one unique
owned temporary index, removed after the test): copied the existing 980-field
mapping and only our new acceptance event payload. Before the mapping update,
indexing returned HTTP 400 `Limit of total fields [1000]`. Applying the exact
compatibility mapping allowed HTTP 200 indexing, retained exact `_source` and all
legacy envelope field mappings, and left the field count at 980. No existing
envelope child object explicitly enabled dynamic mapping. Independent review
found no blocking issues. Production alias/data/outbox were not changed by this
reproduction; fresh deployed acceptance remains required.

## Legacy mapping compatibility follow-up

- [x] Red: mixed alias targets and failed/empty mapping reads demonstrate blind object mapping is unsafe.
- [x] Read concrete target mappings first; stable per-index additive patch preserves scalar/nested types and applies conversation audit fields, with a safe diagnostic.
- [x] Object/unmapped targets retain the opaque object mapping boundary; malformed mapping reads cannot trigger a write.
- [x] Follow-up focused tests, full Core `go test ./... -count=1`, build, vet, golangci-lint (0 issues), and both affected packages under race pass.

The object mapping supports opaque object envelopes, not every arbitrary JSON value allowed in the authoritative Ledger. Existing scalar/nested mappings are preserved rather than migrated. Existing child objects explicitly overriding `dynamic:true` are not recursively rewritten. No old data or dead outbox rows are changed.

## Disabled object compatibility and real OpenSearch regression

OpenSearch ObjectMapper's mapping-update merge compares enabled values even when the incoming enabled value is only the default. A legacy `enabled:false` object therefore cannot safely receive the default-enabled object patch. Preserve it with the same audit-only patch as other incompatible legacy definitions; it already avoids dynamic field growth.

- [x] Disabled legacy object joins mixed-alias red regression; patch preserves its enabled flag.
- [x] Add real integration regression for scalar, disabled object and normal object mappings; only randomly named test-owned indexes are created/deleted in the existing OpenSearch instance. Normal-object fixture demonstrates field-limit rejection before bootstrap and source-preserving projection after bootstrap, without raising its limit.
- [x] Focused regressions, full Core lint/build and integration-tag compile/vet pass.
- [x] Coordinator ran `TestOpenSearchBootstrapPreservesLegacyEnvelopeMappings` against the existing OpenSearch via localhost:19200: scalar, disabled object and normal object all PASS (0.876s), including repeated bootstrap, actual projection, complete source validation and cleanup of every test-owned index. Compile with an empty endpoint remains distinct from this real integration proof.

[Official ObjectMapper defaults/merge semantics](https://github.com/opensearch-project/OpenSearch/blob/2.19/server/src/main/java/org/opensearch/index/mapper/ObjectMapper.java#L738).
