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
- [ ] Independent review, release, and fresh 8081 real-consumer projection acceptance (coordinated separately; no deployment in this change).

Additional acceptance reproduction on the existing 8081 OpenSearch (one unique\nowned temporary index, removed after the test): copied the existing 980-field\nmapping and only our new acceptance event payload. Before the mapping update,\nindexing returned HTTP 400 `Limit of total fields [1000]`. Applying the exact\ncompatibility mapping allowed HTTP 200 indexing, retained exact `_source` and all\nlegacy envelope field mappings, and left the field count at 980. No existing\nenvelope child object explicitly enabled dynamic mapping. Independent review\nfound no blocking issues. Production alias/data/outbox were not changed by this\nreproduction; fresh deployed acceptance remains required.
