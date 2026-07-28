package evidencesvc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
)

func TestIngestArtifactStoresNormalizedContentAndIsIdempotent(t *testing.T) {
	store := evidencestore.New()
	service := NewWithArtifactStore(store, store)
	body := validArtifactBody(t, "artifact_question_service", map[string]any{"text": "用户问题"})

	first, validationErrors, err := service.IngestArtifact(context.Background(), body)
	if err != nil || len(validationErrors) != 0 || !first.Created {
		t.Fatalf("first ingest must create artifact: response=%+v validation=%+v err=%v", first, validationErrors, err)
	}
	second, validationErrors, err := service.IngestArtifact(context.Background(), body)
	if err != nil || len(validationErrors) != 0 || second.Created {
		t.Fatalf("second ingest must be idempotent: response=%+v validation=%+v err=%v", second, validationErrors, err)
	}
	if first.ContentHash == "" || first.ContentHash != second.ContentHash {
		t.Fatalf("expected stable normalized hash: first=%+v second=%+v", first, second)
	}
}

func TestIngestArtifactReturnsConflictForSameIDWithDifferentContent(t *testing.T) {
	store := evidencestore.New()
	service := NewWithArtifactStore(store, store)
	if _, validationErrors, err := service.IngestArtifact(context.Background(), validArtifactBody(t, "artifact_conflict_service", map[string]any{"text": "one"})); err != nil || len(validationErrors) != 0 {
		t.Fatalf("seed artifact: validation=%+v err=%v", validationErrors, err)
	}

	_, validationErrors, err := service.IngestArtifact(context.Background(), validArtifactBody(t, "artifact_conflict_service", map[string]any{"text": "two"}))

	if err != nil || !hasErrorCode(validationErrors, "BKN_TRACE_ARTIFACT_ID_CONFLICT") {
		t.Fatalf("expected stable conflict validation error, got validation=%+v err=%v", validationErrors, err)
	}
}

func TestGetArtifactReturnsOnlyArtifactVisibleToScope(t *testing.T) {
	store := evidencestore.New()
	service := NewWithArtifactStore(store, store)
	if _, validationErrors, err := service.IngestArtifact(context.Background(), validArtifactBody(t, "artifact_query_service", map[string]any{"text": "result"})); err != nil || len(validationErrors) != 0 {
		t.Fatalf("seed artifact: validation=%+v err=%v", validationErrors, err)
	}

	artifact, found, err := service.GetArtifact(context.Background(), "artifact_query_service", evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if err != nil || !found || artifact.Content == nil {
		t.Fatalf("owner must read content: artifact=%+v found=%v err=%v", artifact, found, err)
	}
	_, found, err = service.GetArtifact(context.Background(), "artifact_query_service", evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "other", AccountType: "app",
	})
	if err != nil || found {
		t.Fatalf("cross-owner query must look absent: found=%v err=%v", found, err)
	}
	_, found, err = service.GetArtifact(context.Background(), "artifact_query_service", evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "other-domain", AccountID: "acct_demo", AccountType: "app",
	})
	if err != nil || found {
		t.Fatalf("cross-business-domain query must look absent: found=%v err=%v", found, err)
	}
	_, found, err = service.GetArtifact(context.Background(), "artifact_query_service", evidencevo.QueryScope{
		AccountID: "acct_demo", AccountType: "app",
	})
	if err != nil || found {
		t.Fatalf("query without tenant or business domain must fail closed: found=%v err=%v", found, err)
	}
}

func TestGetArtifactReturnsOwnedContentWhenBusinessResolverIsUnavailable(t *testing.T) {
	store := evidencestore.New()
	artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "artifact_unresolved_ref_service", ArtifactType: evidencevo.ArtifactTypeDataResult,
		RequestID: "req_unresolved_ref", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		BusinessRefs: []string{"object:kn_sales:order"},
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:00Z", Content: map[string]any{"count": 12},
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}

	got, found, err := NewWithArtifactStore(store, store).GetArtifact(context.Background(), artifact.ArtifactID, evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})

	if err != nil || !found || got.Content == nil {
		t.Fatalf("owned artifact content must remain readable when resolver is unavailable: artifact=%+v found=%v err=%v", got, found, err)
	}
}

func TestGetArtifactReturnsOwnedContentWhenBusinessResolverCannotResolveRef(t *testing.T) {
	store := evidencestore.New()
	artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "artifact_missing_resolution_service", ArtifactType: evidencevo.ArtifactTypeDataResult,
		RequestID: "req_missing_resolution", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		BusinessRefs: []string{"object:kn_sales:order"},
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:00Z", Content: map[string]any{"count": 12},
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	resolver := &fakeBusinessResolver{}

	got, found, err := NewWithBusinessResolver(store, resolver).GetArtifact(context.Background(), artifact.ArtifactID, evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})

	if err != nil || !found || got.Content == nil {
		t.Fatalf("owned artifact content must remain readable when refs are unresolved: artifact=%+v found=%v err=%v", got, found, err)
	}
	if len(resolver.requests) != 1 {
		t.Fatalf("resolver should still be called for audit context: %+v", resolver.requests)
	}
}

func TestGetArtifactRequiresResolverAuthorizationForSourceAndBusinessRefs(t *testing.T) {
	store := evidencestore.New()
	artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "artifact_resolver_guard", ArtifactType: evidencevo.ArtifactTypeDataResult,
		RequestID: "req_resolver_guard", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SourceRef: "resource:orders", BusinessRefs: []string{"object:kn_sales:order"},
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:00Z", Content: map[string]any{"count": 12},
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	scope := evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	}

	deniedResolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{
		{RefID: "resource:orders", Visibility: "visible"},
		{RefID: "object:kn_sales:order", Visibility: "unauthorized"},
	}}
	_, found, err := NewWithBusinessResolver(store, deniedResolver).GetArtifact(context.Background(), artifact.ArtifactID, scope)
	if err != nil || found {
		t.Fatalf("unauthorized business ref must hide artifact content: found=%v err=%v", found, err)
	}
	if len(deniedResolver.requests) != 1 || deniedResolver.requests[0].Scope.AccountID != "acct_demo" ||
		deniedResolver.requests[0].Scope.BusinessDomain != "bd_demo" {
		t.Fatalf("resolver must receive trusted account and requested domain: %+v", deniedResolver.requests)
	}

	visibleResolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{
		{RefID: "resource:orders", Visibility: "visible"},
		{RefID: "object:kn_sales:order", Visibility: "visible"},
	}}
	got, found, err := NewWithBusinessResolver(store, visibleResolver).GetArtifact(context.Background(), artifact.ArtifactID, scope)
	if err != nil || !found || got.Content == nil {
		t.Fatalf("fully authorized refs must expose artifact: artifact=%+v found=%v err=%v", got, found, err)
	}
}

func TestIngestArtifactRejectsOwnershipDriftFromExistingTrace(t *testing.T) {
	store := evidencestore.New()
	trace := evidencevo.NormalizedTrace{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", RequestID: "req_existing",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: evidencevo.ContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "evt_existing", EventType: "claim.created", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			RequestID: "req_existing", Payload: map[string]any{"claim_id": "claim_existing"},
		}},
	}
	if err := store.StoreEvidence(context.Background(), evidencevo.WithEvents(trace, trace.Events)); err != nil {
		t.Fatal(err)
	}
	service := NewWithArtifactStore(store, store)
	body := validArtifactBody(t, "artifact_drift", map[string]any{"text": "question"})

	_, validationErrors, err := service.IngestArtifact(context.Background(), body)

	if err != nil || !hasErrorCode(validationErrors, "BKN_TRACE_OWNERSHIP_CONFLICT") {
		t.Fatalf("artifact must not claim an existing trace with another request: validation=%+v err=%v", validationErrors, err)
	}
}

func TestIngestArtifactRejectsTypeThatDoesNotMatchCommittedEventRole(t *testing.T) {
	store := evidencestore.New()
	trace := evidencevo.NormalizedTrace{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", RequestID: "req_artifact_service",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "evt_question_role", EventType: "agent.interaction.started",
			SchemaVersion: evidencevo.ArtifactContractVersion,
			TraceID:       "4bf92f3577b34da6a3ce929d0e0e4736", RequestID: "req_artifact_service",
			Payload: map[string]any{"question_artifact_ref": "artifact:artifact_wrong_role"},
		}},
	}
	if err := store.StoreEvidence(context.Background(), evidencevo.WithEvents(trace, trace.Events)); err != nil {
		t.Fatal(err)
	}
	body := validArtifactBody(t, "artifact_wrong_role", map[string]any{"text": "不是问题类型"})
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload["artifact_type"] = string(evidencevo.ArtifactTypeResult)
	body, _ = json.Marshal(payload)

	_, validationErrors, err := NewWithArtifactStore(store, store).IngestArtifact(context.Background(), body)

	if err != nil || !hasErrorCode(validationErrors, "BKN_TRACE_ARTIFACT_TYPE_MISMATCH") {
		t.Fatalf("role/type mismatch must be rejected: validation=%+v err=%v", validationErrors, err)
	}
}

func validArtifactBody(t *testing.T, artifactID string, content any) []byte {
	t.Helper()
	body, err := json.Marshal(evidencevo.EvidenceArtifact{
		ArtifactID: artifactID, ArtifactType: evidencevo.ArtifactTypeQuestion,
		RequestID: "req_artifact_service", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		InteractionID: "interaction_001", OperationID: "operation_001",
		SourceRef:   "interaction:interaction_001",
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:00Z", Content: content,
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func hasErrorCode(errors evidencevo.ValidationErrors, code string) bool {
	for _, item := range errors {
		if item.Code == code {
			return true
		}
	}
	return false
}
