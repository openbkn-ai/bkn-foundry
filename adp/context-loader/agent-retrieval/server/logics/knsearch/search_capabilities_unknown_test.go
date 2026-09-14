// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knsearch

import (
	"context"
	"errors"
	"strings"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Issue #1536: an object type whose search capabilities bkn-backend could not determine was skipped
// as "no searchable field", and an answer made only of such object types came back as "no match".

func unenrichedObjectType(id string) *interfaces.KnSearchObjectType {
	return &interfaces.KnSearchObjectType{
		ConceptID: id,
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "brand_name", Type: "string"},
		},
	}
}

func TestConvertObjectTypesCarriesUnavailableDataSourceMetadata(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	local := svc.convertObjectTypesToLocal([]*interfaces.ObjectType{
		{ID: "brand", DataSourceMetadataUnavailable: true},
		{ID: "store"},
	}, true, false)

	if !local[0].SearchCapabilitiesUnknown || local[1].SearchCapabilitiesUnknown {
		t.Fatalf("capabilities unknown = %v, %v", local[0].SearchCapabilitiesUnknown, local[1].SearchCapabilitiesUnknown)
	}
}

func TestBackfillMarksCapabilitiesUnknownWhenDetailFails(t *testing.T) {
	backend := &mockBknBackend{objectDetailError: errors.New("bkn-backend unavailable")}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	objType := unenrichedObjectType("brand")

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, false)

	if !objType.SearchCapabilitiesUnknown {
		t.Fatal("a failed detail lookup must leave the capabilities unknown, not empty")
	}
}

func TestBackfillCarriesTheBackendMarker(t *testing.T) {
	backend := &mockBknBackend{objectDetailResp: []*interfaces.ObjectType{
		{ID: "brand", DataSourceMetadataUnavailable: true, DataProperties: []*interfaces.DataProperty{{Name: "brand_name"}}},
		{ID: "store", DataProperties: []*interfaces.DataProperty{{Name: "brand_name"}}},
	}}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	unreadable := unenrichedObjectType("brand")
	// store's resource was read and simply has nothing searchable: that is a real "none".
	store := unenrichedObjectType("store")
	store.SearchCapabilitiesUnknown = true

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{unreadable, store}, false)

	if !unreadable.SearchCapabilitiesUnknown {
		t.Fatal("bkn-backend's unavailable marker was dropped")
	}
	if store.SearchCapabilitiesUnknown {
		t.Fatal("a detail derived from the resource must clear the unknown state")
	}
}

func TestBackfillClearsUnknownOnceOperatorsArrive(t *testing.T) {
	backend := &mockBknBackend{objectDetailResp: []*interfaces.ObjectType{{
		ID: "brand", DataProperties: []*interfaces.DataProperty{{
			Name: "brand_name", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeMatch},
		}},
	}}}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	objType := unenrichedObjectType("brand")
	objType.SearchCapabilitiesUnknown = true // the search response could not read the resource

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, false)

	if objType.SearchCapabilitiesUnknown || len(findSemanticSearchableFields(objType)) != 1 {
		t.Fatalf("unknown=%v searchable=%v", objType.SearchCapabilitiesUnknown, findSemanticSearchableFields(objType))
	}
}

func newCapabilityRetrievalService(query *mockOntologyQuery) *localSearchImpl {
	return &localSearchImpl{logger: &mockLogger{}, ontologyQuery: query, authorizer: allowAllQueryCandidateAuthorizer{}}
}

func TestSemanticInstanceRetrievalReportsUnknownCapabilitiesInsteadOfNoMatch(t *testing.T) {
	query := &mockOntologyQuery{}
	objType := unenrichedObjectType("brand")
	objType.SearchCapabilitiesUnknown = true
	ctx := context.Background()

	res, err := newCapabilityRetrievalService(query).semanticInstanceRetrieval(ctx,
		&interfaces.KnSearchLocalRequest{KnID: "kn1", Query: "戴尔"}, []*interfaces.KnSearchObjectType{objType},
		DefaultRetrievalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 0 || query.calls() != 0 {
		t.Fatalf("nodes=%d queries=%d, want nothing searched", len(res.Nodes), query.calls())
	}
	want := infraErr.LocalizedDetail(ctx, "InstanceSearchCapabilitiesUnknown", "brand")
	if res.Message != want {
		t.Fatalf("message = %q, want %q", res.Message, want)
	}
	if res.Message == infraErr.LocalizedDetail(ctx, "NoMatchingInstances") || !strings.Contains(res.Message, "brand") {
		t.Fatalf("message does not name the unsearched object type: %q", res.Message)
	}
}

func TestSemanticInstanceRetrievalKeepsNoMatchForNothingSearchable(t *testing.T) {
	ctx := context.Background()
	res, err := newCapabilityRetrievalService(&mockOntologyQuery{}).semanticInstanceRetrieval(ctx,
		&interfaces.KnSearchLocalRequest{KnID: "kn1", Query: "戴尔"},
		[]*interfaces.KnSearchObjectType{unenrichedObjectType("brand")}, DefaultRetrievalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != infraErr.LocalizedDetail(ctx, "NoMatchingInstances") {
		t.Fatalf("message = %q, want the plain no-match message", res.Message)
	}
}

func TestSemanticInstanceRetrievalReportsUnknownCapabilitiesAlongsideRows(t *testing.T) {
	query := &mockOntologyQuery{instancesResp: &interfaces.QueryObjectInstancesResp{
		Data: []any{map[string]any{"instance_name": "戴尔", "_score": 0.9}},
	}}
	searchable := &interfaces.KnSearchObjectType{
		ConceptID: "store",
		DataProperties: []*interfaces.KnSearchDataProperty{{
			Name: "store_name", Type: "text", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeMatch},
		}},
	}
	unknown := unenrichedObjectType("brand")
	unknown.SearchCapabilitiesUnknown = true
	ctx := context.Background()

	res, err := newCapabilityRetrievalService(query).semanticInstanceRetrieval(ctx,
		&interfaces.KnSearchLocalRequest{KnID: "kn1", Query: "戴尔"},
		[]*interfaces.KnSearchObjectType{searchable, unknown}, DefaultRetrievalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) == 0 {
		t.Fatal("the searchable object type must still return its rows")
	}
	if res.Message != infraErr.LocalizedDetail(ctx, "InstanceSearchCapabilitiesUnknown", "brand") {
		t.Fatalf("message = %q, want the incomplete-result notice", res.Message)
	}
}

func TestJoinMessagesSkipsEmpty(t *testing.T) {
	if got := joinMessages("gate skipped.", "", "  ", "not searched."); got != "gate skipped. not searched." {
		t.Fatalf("joinMessages = %q", got)
	}
}
