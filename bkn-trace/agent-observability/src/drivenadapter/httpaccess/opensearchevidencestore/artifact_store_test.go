package opensearchevidencestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

func TestOpenSearchArtifactStoreIsIdempotentAndRejectsConflict(t *testing.T) {
	backend := newArtifactBackend()
	store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
	artifact := normalizedOpenSearchArtifact(t)

	created, err := store.StoreArtifact(context.Background(), artifact)
	if err != nil || !created {
		t.Fatalf("first store must create: created=%v err=%v", created, err)
	}
	created, err = store.StoreArtifact(context.Background(), artifact)
	if err != nil || created {
		t.Fatalf("identical replay must be idempotent: created=%v err=%v", created, err)
	}

	conflict := artifact
	conflict.Content = map[string]any{"text": "different"}
	conflict.ContentHash = ""
	conflict, validationErrors := evidencevo.NormalizeArtifact(conflict)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize conflict: %+v", validationErrors)
	}
	created, err = store.StoreArtifact(context.Background(), conflict)
	if created || !errors.Is(err, iartifactstore.ErrArtifactIDConflict) {
		t.Fatalf("same ID with different artifact must conflict: created=%v err=%v", created, err)
	}
	if !strings.Contains(backend.mapping, `"content_json":{"type":"keyword","index":false,"doc_values":false}`) {
		t.Fatalf("canonical artifact content must be stored as a non-indexed string: %s", backend.mapping)
	}
}

func TestOpenSearchArtifactStoreRoundTripsScalarArrayAndObjectContentThroughCanonicalJSON(t *testing.T) {
	tests := []struct {
		name    string
		content any
	}{
		{name: "scalar", content: "最终结果"},
		{name: "array", content: []any{"对象:A", map[string]any{"quantity": float64(2)}}},
		{name: "object", content: map[string]any{"answer": "可追溯", "count": float64(3)}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newArtifactBackend()
			store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
			artifact := normalizedOpenSearchArtifact(t)
			artifact.ArtifactID = fmt.Sprintf("artifact_shape_%d", index)
			artifact.Content = tt.content
			artifact.ContentHash = ""
			artifact, validationErrors := evidencevo.NormalizeArtifact(artifact)
			if len(validationErrors) != 0 {
				t.Fatalf("normalize artifact: %+v", validationErrors)
			}

			if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
				t.Fatalf("store artifact: %v", err)
			}
			var document map[string]any
			if err := json.Unmarshal(backend.docs[artifact.ArtifactID], &document); err != nil {
				t.Fatalf("decode stored document: %v", err)
			}
			if _, exists := document["content"]; exists {
				t.Fatalf("polymorphic content must not be stored as an OpenSearch object field: %+v", document)
			}
			contentJSON, ok := document["content_json"].(string)
			if !ok || !json.Valid([]byte(contentJSON)) {
				t.Fatalf("content_json must contain canonical JSON: %+v", document)
			}

			scope := evidencevo.QueryScope{
				TenantID: artifact.TenantID, BusinessDomain: artifact.BusinessDomain,
				AccountID: artifact.AccountID, AccountType: artifact.AccountType,
			}
			restored, found, err := store.GetArtifact(context.Background(), artifact.ArtifactID, scope)
			if err != nil || !found {
				t.Fatalf("get artifact: found=%v err=%v", found, err)
			}
			if !reflect.DeepEqual(restored.Content, artifact.Content) {
				t.Fatalf("content shape changed: want=%#v got=%#v", artifact.Content, restored.Content)
			}
		})
	}
}

func TestDecodeArtifactDocumentCanonicalizesPersistedTimestampOffsets(t *testing.T) {
	artifact := normalizedOpenSearchArtifact(t)
	document, err := toArtifactDocument(artifact)
	if err != nil {
		t.Fatal(err)
	}
	document.ObservedAt = "2026-07-26T10:00:00.123456789+08:00"
	document.AsOf = "2026-07-25T21:00:00-05:00"
	body, _ := json.Marshal(document)

	decoded, err := decodeArtifactDocument(body)

	if err != nil {
		t.Fatal(err)
	}
	if decoded.ObservedAt != "2026-07-26T02:00:00.123456789Z" || decoded.AsOf != "2026-07-26T02:00:00Z" {
		t.Fatalf("persisted artifact timestamps must be canonical before in-memory sorting: %+v", decoded)
	}
}

func TestOpenSearchArtifactStoreFailsClosedByOwnership(t *testing.T) {
	backend := newArtifactBackend()
	store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
	artifact := normalizedOpenSearchArtifact(t)
	if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	exact := evidencevo.QueryScope{
		TenantID: artifact.TenantID, BusinessDomain: artifact.BusinessDomain,
		AccountID: artifact.AccountID, AccountType: artifact.AccountType,
	}

	got, found, err := store.GetArtifact(context.Background(), artifact.ArtifactID, exact)
	if err != nil || !found || got.ContentHash != artifact.ContentHash {
		t.Fatalf("exact owner must read artifact: found=%v err=%v artifact=%+v", found, err, got)
	}
	denied := exact
	denied.BusinessDomain = "other-domain"
	if leaked, found, err := store.GetArtifact(context.Background(), artifact.ArtifactID, denied); err != nil || found || leaked.ArtifactID != "" {
		t.Fatalf("mismatched ownership must look absent: found=%v err=%v artifact=%+v", found, err, leaked)
	}
}

func TestOpenSearchArtifactStorePersistsRecordScopeForNetworkBuilder(t *testing.T) {
	backend := newArtifactBackend()
	store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
	artifact := normalizedOpenSearchArtifact(t)
	artifact.AccountID = "other-user"
	artifact.AccountType = "user"
	artifact.EffectiveSubjectID = "other-user"
	artifact.ApplicationPrincipalID = "app-a"
	artifact.BusinessRefs = []string{"object:kn-a:forecast", "property:kn-b:forecast:qty"}
	artifact, validationErrors := evidencevo.NormalizeArtifact(artifact)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	scope := evidencevo.QueryScope{View: evidencevo.AccessViewBusiness, AccessProfile: &evidencevo.AccessProfile{
		TenantID: artifact.TenantID, BusinessDomain: artifact.BusinessDomain,
		EffectiveSubjectID: "builder-a", Roles: []string{"network_builder"},
		ManagedKnowledgeNetworkIDs: []string{"kn-a", "kn-b"}, AccountActive: true, TenantActive: true,
	}}

	restored, found, err := store.GetArtifact(context.Background(), artifact.ArtifactID, scope)

	if err != nil || !found {
		t.Fatalf("network builder must read a fully managed record: found=%v err=%v", found, err)
	}
	if restored.EffectiveSubjectID != "other-user" || restored.ApplicationPrincipalID != "app-a" ||
		!reflect.DeepEqual(restored.KnowledgeNetworkIDs, []string{"kn-a", "kn-b"}) {
		t.Fatalf("record scope was not preserved: %+v", restored)
	}
	for _, field := range []string{"effective_subject_id", "application_principal_id", "knowledge_network_ids"} {
		if !strings.Contains(backend.mapping, `"`+field+`":{"type":"keyword"}`) {
			t.Fatalf("artifact index must map %s: %s", field, backend.mapping)
		}
	}
}

func TestOpenSearchArtifactListUsesOwnershipFiltersAndReturnFilter(t *testing.T) {
	backend := newArtifactBackend()
	store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
	owned := normalizedOpenSearchArtifact(t)
	if _, err := store.StoreArtifact(context.Background(), owned); err != nil {
		t.Fatal(err)
	}
	other := owned
	other.ArtifactID = "artifact_os_other"
	other.AccountID = "other-account"
	other, validationErrors := evidencevo.NormalizeArtifact(other)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize other artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	scope := evidencevo.QueryScope{
		TenantID: owned.TenantID, BusinessDomain: owned.BusinessDomain,
		AccountID: owned.AccountID, AccountType: owned.AccountType,
	}

	result, err := store.ListArtifactsByRequestID(context.Background(), owned.RequestID, iartifactstore.QueryOptions{
		Scope: scope,
		Limit: iartifactstore.MaxArtifactQueryLimit,
	})

	if err != nil || len(result.Entries) != 1 || result.Entries[0].ArtifactID != owned.ArtifactID || result.Truncated {
		t.Fatalf("expected only owned artifact: artifacts=%+v err=%v", result, err)
	}
	for _, field := range []string{"bkn.tenant.id", "business_domain", "bkn.account.id", "bkn.account.type", "bkn.request.id"} {
		if !strings.Contains(backend.lastSearch, field) {
			t.Fatalf("artifact search must filter %s: %s", field, backend.lastSearch)
		}
	}
}

type artifactBackend struct {
	mu         sync.Mutex
	docs       map[string][]byte
	mapping    string
	lastSearch string
}

func newArtifactBackend() *artifactBackend {
	return &artifactBackend{docs: map[string][]byte{}}
}

func (b *artifactBackend) roundTrip(r *http.Request) (*http.Response, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test-artifacts" {
		body, _ := io.ReadAll(r.Body)
		b.mapping = string(body)
		return jsonResponse(`{"acknowledged":true}`), nil
	}
	const documentPrefix = "/bkn-trace-evidence-test-artifacts/_doc/"
	if strings.HasPrefix(r.URL.Path, documentPrefix) {
		id := strings.TrimPrefix(r.URL.Path, documentPrefix)
		body, found := b.docs[id]
		switch r.Method {
		case http.MethodGet:
			if !found {
				return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
			}
			return jsonResponse(`{"_seq_no":0,"_primary_term":1,"_source":` + string(body) + `}`), nil
		case http.MethodPut:
			if found && r.URL.Query().Get("op_type") == "create" {
				return statusJSONResponse(http.StatusConflict, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
			}
			stored, _ := io.ReadAll(r.Body)
			b.docs[id] = stored
			return jsonResponse(`{"result":"created"}`), nil
		}
	}
	if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test-artifacts/_search" {
		query, _ := io.ReadAll(r.Body)
		b.lastSearch = string(query)
		hits := make([]map[string]any, 0, len(b.docs))
		for id, body := range b.docs {
			var artifact evidencevo.EvidenceArtifact
			_ = json.Unmarshal(body, &artifact)
			if !strings.Contains(b.lastSearch, artifact.RequestID) {
				continue
			}
			hits = append(hits, map[string]any{"_id": id, "_source": artifact, "sort": []any{artifact.ObservedAt, artifact.ArtifactID}})
		}
		response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
		return jsonResponse(string(response)), nil
	}
	return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
}

func normalizedOpenSearchArtifact(t *testing.T) evidencevo.EvidenceArtifact {
	t.Helper()
	artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "artifact_os_001", ArtifactType: evidencevo.ArtifactTypeResult,
		RequestID: "req_os_artifact", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:00Z", Content: map[string]any{"text": "result"},
		TenantID: "tenant_os", BusinessDomain: "bd_os", AccountID: "acct_os", AccountType: "app",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	return artifact
}
