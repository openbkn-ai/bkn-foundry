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

// Endpoint completion fetches every object type a relation points at and is not already in the
// pool. Scoping before it would have it fetch the excluded object types straight back in, and
// object selection refills relation endpoints without a limit, so they would reach the response.
func TestConceptRetrievalByGroups_ExcludedEndpointIsNotRefetched(t *testing.T) {
	backend := &mockBknBackend{
		objectTypesResp: &interfaces.ObjectTypeConcepts{Entries: []*interfaces.ObjectType{
			{ID: "obj_0", Name: "对象类型_0"},
			{ID: "audit_log", Name: "审计日志"},
		}},
		relationTypesResp: &interfaces.RelationTypeConcepts{Entries: []*interfaces.RelationType{
			{ID: "rel_0", Name: "写入", SourceObjectTypeID: "obj_0", TargetObjectTypeID: "audit_log"},
		}},
		actionTypesResp: &interfaces.ActionTypeConcepts{Entries: []*interfaces.ActionType{
			{ID: "act_0", Name: "归档", ObjectTypeID: "audit_log"},
		}},
		// What the pre-fix code would have pulled back in.
		objectDetailResp: []*interfaces.ObjectType{{ID: "audit_log", Name: "审计日志"}},
	}
	cfg := DefaultConceptRetrievalConfig()
	cfg.ConceptGroups = []string{"g1"}
	cfg.ExcludeObjectTypes = []string{"audit_log"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsConcept(res.ObjectTypes, "audit_log") {
		t.Fatalf("excluded object type came back through endpoint completion: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(conceptIDs(res.ObjectTypes), []string{"obj_0"}) {
		t.Fatalf("expected only obj_0, got %v", conceptIDs(res.ObjectTypes))
	}
	if backend.objectDetailCalls != 0 {
		t.Fatalf("nothing was missing from the pool, yet completion fetched (%d calls)", backend.objectDetailCalls)
	}
	if len(res.RelationTypes) != 0 {
		t.Fatalf("relation pointing at the excluded endpoint survived: %d", len(res.RelationTypes))
	}
	if len(res.ActionTypes) != 0 {
		t.Fatalf("action bound to the excluded object type survived: %d", len(res.ActionTypes))
	}
}

// Same invariant on the whole-network path: what the scope removes must not stay referenced by the
// schema half of the response, which kn_search callers read.
func TestConceptRetrieval_ScopeDropsDanglingRelationsAndActions(t *testing.T) {
	detail := createMockNetworkDetail(4, 4, 4)
	cfg := DefaultConceptRetrievalConfig()
	cfg.EnableCoarseRecall = boolPtr(false)
	cfg.TopK = 10
	cfg.ObjectTypes = []string{"obj_0"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: &mockBknBackend{networkDetail: detail}}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalStrings(conceptIDs(res.ObjectTypes), []string{"obj_0"}) {
		t.Fatalf("expected only obj_0, got %v", conceptIDs(res.ObjectTypes))
	}
	for _, relation := range res.RelationTypes {
		if relation.SourceObjectTypeID != "obj_0" || relation.TargetObjectTypeID != "obj_0" {
			t.Fatalf("relation %s points outside the scope: %s -> %s",
				relation.ConceptID, relation.SourceObjectTypeID, relation.TargetObjectTypeID)
		}
	}
	for _, action := range res.ActionTypes {
		if action.ObjectTypeID != "obj_0" {
			t.Fatalf("action %s is bound to out-of-scope object type %s", action.ID, action.ObjectTypeID)
		}
	}
}

// A query with no scope must behave exactly as before: same relations, same actions.
func TestConceptRetrieval_NoScopeLeavesConceptsUntouched(t *testing.T) {
	detail := createMockNetworkDetail(4, 4, 4)
	cfg := DefaultConceptRetrievalConfig()
	cfg.EnableCoarseRecall = boolPtr(false)
	cfg.TopK = 10

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: &mockBknBackend{networkDetail: detail}}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.RelationTypes) != 4 || len(res.ActionTypes) != 4 {
		t.Fatalf("unscoped query lost concepts: relations=%d actions=%d", len(res.RelationTypes), len(res.ActionTypes))
	}
}

// The cross-group shape of the same defect: the excluded object type is not in the group at all,
// so BKN's typed search never returns it and only the relation reaches out to it. Endpoint
// completion would fetch it and append it to the pool.
func TestConceptRetrievalByGroups_ExcludedEndpointOutsideGroupStaysOut(t *testing.T) {
	backend := &mockBknBackend{
		objectTypesResp: &interfaces.ObjectTypeConcepts{Entries: []*interfaces.ObjectType{
			{ID: "obj_0", Name: "对象类型_0"},
		}},
		relationTypesResp: &interfaces.RelationTypeConcepts{Entries: []*interfaces.RelationType{
			{ID: "rel_0", Name: "写入", SourceObjectTypeID: "obj_0", TargetObjectTypeID: "audit_log"},
		}},
		actionTypesResp:  &interfaces.ActionTypeConcepts{},
		objectDetailResp: []*interfaces.ObjectType{{ID: "audit_log", Name: "审计日志"}},
	}
	cfg := DefaultConceptRetrievalConfig()
	cfg.ConceptGroups = []string{"g1"}
	cfg.ExcludeObjectTypes = []string{"audit_log"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsConcept(res.ObjectTypes, "audit_log") {
		t.Fatalf("excluded object type was completed back into the pool: %v", conceptIDs(res.ObjectTypes))
	}
	// Completion does fetch it -- it runs first, by design, so that a pinned object type living
	// outside the groups can still be completed in. The scope drops it right after.
	if backend.objectDetailCalls != 1 {
		t.Fatalf("expected endpoint completion to run once, got %d calls", backend.objectDetailCalls)
	}
	if len(res.RelationTypes) != 0 {
		t.Fatalf("relation reaching out of scope survived: %d", len(res.RelationTypes))
	}
}

// An allow list must not stop endpoint completion from doing its job for object types that are in
// scope: a relation between two pinned object types still needs both ends present.
func TestConceptRetrievalByGroups_InScopeEndpointIsStillCompleted(t *testing.T) {
	backend := &mockBknBackend{
		objectTypesResp: &interfaces.ObjectTypeConcepts{Entries: []*interfaces.ObjectType{
			{ID: "obj_0", Name: "对象类型_0"},
		}},
		relationTypesResp: &interfaces.RelationTypeConcepts{Entries: []*interfaces.RelationType{
			{ID: "rel_0", Name: "关联", SourceObjectTypeID: "obj_0", TargetObjectTypeID: "obj_1"},
		}},
		actionTypesResp:  &interfaces.ActionTypeConcepts{},
		objectDetailResp: []*interfaces.ObjectType{{ID: "obj_1", Name: "对象类型_1"}},
	}
	cfg := DefaultConceptRetrievalConfig()
	cfg.ConceptGroups = []string{"g1"}
	cfg.ObjectTypes = []string{"obj_0", "obj_1"}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	res, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsConcept(res.ObjectTypes, "obj_1") {
		t.Fatalf("in-scope endpoint was not completed: %v", conceptIDs(res.ObjectTypes))
	}
	if len(res.RelationTypes) != 1 {
		t.Fatalf("relation between two pinned object types was dropped: %d", len(res.RelationTypes))
	}
	if len(res.UnmatchedObjectTypes) != 0 {
		t.Fatalf("an id that only appears as an endpoint was reported unmatched: %v", res.UnmatchedObjectTypes)
	}
}
