// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func conceptIDs(objectTypes []*interfaces.KnSearchObjectType) []string {
	out := make([]string, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		out = append(out, objectType.ConceptID)
	}
	return out
}

func containsConcept(objectTypes []*interfaces.KnSearchObjectType, id string) bool {
	for _, objectType := range objectTypes {
		if objectType.ConceptID == id {
			return true
		}
	}
	return false
}

// A pinned object type that scores worst of all must still come back. Filtering after the ranking
// and the TopK cut would drop it, and the caller would read "no such data" for an object type it
// named explicitly.
func TestConceptRetrieval_ScopeKeepsObjectTypeBelowTopK(t *testing.T) {
	detail := createMockNetworkDetail(20, 0, 0)
	for i := range detail.ObjectTypes {
		detail.ObjectTypes[i].Score = float64(20 - i)
	}
	// obj_19 is last by score: outside TopK=5 on every ranking path.
	cfg := DefaultConceptRetrievalConfig()
	cfg.EnableCoarseRecall = boolPtr(false)
	cfg.TopK = 5
	cfg.ObjectTypes = []string{"obj_19"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: &mockBknBackend{networkDetail: detail}}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalStrings(conceptIDs(res.ObjectTypes), []string{"obj_19"}) {
		t.Fatalf("expected only the pinned object type, got %v", conceptIDs(res.ObjectTypes))
	}
	if len(res.UnmatchedObjectTypes) != 0 {
		t.Fatalf("unexpected unmatched ids: %v", res.UnmatchedObjectTypes)
	}
}

// Object selection pulls in the endpoints of the ranked relations to keep the schema self-consistent.
// An excluded object type must not sneak back in through that door.
func TestConceptRetrieval_ScopeExcludeSurvivesRelationEndpointRefill(t *testing.T) {
	detail := createMockNetworkDetail(4, 4, 0)
	cfg := DefaultConceptRetrievalConfig()
	cfg.EnableCoarseRecall = boolPtr(false)
	cfg.TopK = 10
	cfg.ExcludeObjectTypes = []string{"obj_1"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: &mockBknBackend{networkDetail: detail}}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsConcept(res.ObjectTypes, "obj_1") {
		t.Fatalf("excluded object type came back as a relation endpoint: %v", conceptIDs(res.ObjectTypes))
	}
	if len(res.ObjectTypes) == 0 {
		t.Fatal("exclusion emptied the whole pool")
	}
}

func TestConceptRetrieval_ScopeReportsUnmatchedIDs(t *testing.T) {
	detail := createMockNetworkDetail(5, 0, 0)
	cfg := DefaultConceptRetrievalConfig()
	cfg.EnableCoarseRecall = boolPtr(false)
	cfg.ObjectTypes = []string{"物料", "obj_2"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: &mockBknBackend{networkDetail: detail}}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalStrings(conceptIDs(res.ObjectTypes), []string{"obj_2"}) {
		t.Fatalf("expected only obj_2, got %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"物料"}) {
		t.Fatalf("expected the name-shaped id to be reported, got %v", res.UnmatchedObjectTypes)
	}
}

// The concept-group path fetches object types from BKN's typed search instead of the network
// export, and has to apply the same scope.
func TestConceptRetrievalByGroups_AppliesScope(t *testing.T) {
	backend := &mockBknBackend{
		objectTypesResp: &interfaces.ObjectTypeConcepts{Entries: []*interfaces.ObjectType{
			{ID: "obj_0", Name: "对象类型_0"},
			{ID: "obj_1", Name: "对象类型_1"},
			{ID: "obj_2", Name: "对象类型_2"},
		}},
		relationTypesResp: &interfaces.RelationTypeConcepts{},
		actionTypesResp:   &interfaces.ActionTypeConcepts{},
	}
	cfg := DefaultConceptRetrievalConfig()
	cfg.ConceptGroups = []string{"group-1"}
	cfg.ObjectTypes = []string{"obj_0", "obj_2", "obj_404"}
	cfg.ExcludeObjectTypes = []string{"obj_2"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalStrings(conceptIDs(res.ObjectTypes), []string{"obj_0"}) {
		t.Fatalf("group path ignored the scope: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"obj_404"}) {
		t.Fatalf("group path lost the unmatched report: %v", res.UnmatchedObjectTypes)
	}
}

// An allow list that matches nothing has to say so. The generic "no searchable object types"
// message reads as a fact about the knowledge network and sends the caller looking elsewhere.
func TestSearch_ScopeMatchedNothingExplainsWhy(t *testing.T) {
	detail := createMockNetworkDetail(5, 0, 0)
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: &mockBknBackend{networkDetail: detail}}

	req := &interfaces.KnSearchLocalRequest{
		KnID:  "129",
		Query: "q",
		RetrievalConfig: &interfaces.KnSearchRetrievalConfig{
			ConceptRetrieval: &interfaces.KnSearchConceptRetrievalConfig{
				EnableCoarseRecall: boolPtr(false),
				ObjectTypes:        []string{"物料"},
			},
		},
	}
	resp, err := svc.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Nodes) != 0 || len(resp.ObjectTypes) != 0 {
		t.Fatalf("expected an empty result, got nodes=%d object_types=%d", len(resp.Nodes), len(resp.ObjectTypes))
	}
	if !strings.Contains(resp.Message, "物料") {
		t.Fatalf("message does not name the unmatched id: %q", resp.Message)
	}
	if !strings.Contains(resp.Message, "object_types") {
		t.Fatalf("message does not point at the parameter: %q", resp.Message)
	}
	// Guards the catalog wiring: a missing key falls back to the generic internal-error text and
	// Sprintf then tacks the id on as "%!(EXTRA string=...)", which would still pass the checks above.
	if strings.Contains(resp.Message, "%!") {
		t.Fatalf("message was not rendered from the locale catalog: %q", resp.Message)
	}
}
