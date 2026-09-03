// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knsearch

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type stubQueryCandidateAuthorizer struct {
	allowed      []string
	err          error
	gotKnID      string
	gotCandidate []string
	calls        int
}

func (s *stubQueryCandidateAuthorizer) FilterObjectTypeIDs(_ context.Context, knID string,
	candidateIDs []string,
) ([]string, error) {
	s.calls++
	s.gotKnID = knID
	s.gotCandidate = append([]string(nil), candidateIDs...)
	return append([]string(nil), s.allowed...), s.err
}

func pepSearch(authorizer interfaces.QueryCandidateAuthorizer, oq interfaces.DrivenOntologyQuery) *localSearchImpl {
	return &localSearchImpl{
		logger:        &mockLogger{},
		config:        &config.Config{Auth: config.AuthorizationConfig{ContextLoaderKNPEPEnabled: true}},
		ontologyQuery: oq,
		authorizer:    authorizer,
	}
}

func TestFilterAuthorizedObjectTypes_PreservesCandidateOrder(t *testing.T) {
	authorizer := &stubQueryCandidateAuthorizer{allowed: []string{"ot3", "ot1"}}
	svc := pepSearch(authorizer, nil)
	candidates := []*interfaces.KnSearchObjectType{
		{ConceptID: "ot1"}, {ConceptID: "ot2"}, {ConceptID: "ot3"},
	}

	got, err := svc.filterAuthorizedObjectTypes(context.Background(), "kn1", candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotIDs := []string{got[0].ConceptID, got[1].ConceptID}
	if !reflect.DeepEqual(gotIDs, []string{"ot1", "ot3"}) {
		t.Fatalf("candidate order changed: %v", gotIDs)
	}
	if authorizer.gotKnID != "kn1" || !reflect.DeepEqual(authorizer.gotCandidate, []string{"ot1", "ot2", "ot3"}) {
		t.Fatalf("unexpected authorization request: kn=%q ids=%v", authorizer.gotKnID, authorizer.gotCandidate)
	}
}

func TestConceptRetrieval_PEPUsesTypedBKNEndpointsWithoutExportFallback(t *testing.T) {
	backend := &mockBknBackend{
		networkDetail:     &interfaces.KnowledgeNetworkDetail{ObjectTypes: []*interfaces.ObjectType{{ID: "export-only"}}},
		objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: []*interfaces.ObjectType{{ID: "allowed", Name: "Allowed"}}},
		relationTypesResp: &interfaces.RelationTypeConcepts{},
		actionTypesResp:   &interfaces.ActionTypeConcepts{},
	}
	svc := pepSearch(&stubQueryCandidateAuthorizer{}, nil)
	svc.bknBackend = backend
	retrievalConfig := DefaultRetrievalConfig()

	result, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "kn1", Query: "allowed"},
		retrievalConfig.ConceptRetrieval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend.networkCalls != 0 || backend.objectTypesReq == nil || backend.relationTypesReq == nil || backend.actionTypesReq == nil {
		t.Fatalf("unexpected BKN calls: export=%d object=%v relation=%v action=%v",
			backend.networkCalls, backend.objectTypesReq, backend.relationTypesReq, backend.actionTypesReq)
	}
	if len(result.ObjectTypes) != 1 || result.ObjectTypes[0].ConceptID != "allowed" {
		t.Fatalf("typed candidate result not used: %+v", result.ObjectTypes)
	}
}

func TestConceptRetrieval_PEPRejectsIncompleteTypedResponse(t *testing.T) {
	backend := &mockBknBackend{
		objectTypesResp:   &interfaces.ObjectTypeConcepts{},
		relationTypesResp: nil,
		actionTypesResp:   &interfaces.ActionTypeConcepts{},
	}
	svc := pepSearch(&stubQueryCandidateAuthorizer{}, nil)
	svc.bknBackend = backend
	retrievalConfig := DefaultRetrievalConfig()

	result, err := svc.conceptRetrieval(context.Background(), &interfaces.KnSearchLocalRequest{KnID: "kn1", Query: "query"},
		retrievalConfig.ConceptRetrieval)
	status, ok := infraerrors.HTTPStatus(err)
	if result != nil || !ok || status != http.StatusServiceUnavailable {
		t.Fatalf("result=%+v error=%v, want complete failure with 503", result, err)
	}
}

func TestFetchAuthorizedSampleData_FiltersBeforeOntologyQuery(t *testing.T) {
	authorizer := &stubQueryCandidateAuthorizer{allowed: []string{"allowed"}}
	var queried []string
	oq := &mockOntologyQuery{instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
		queried = append(queried, req.OtID)
		return &interfaces.QueryObjectInstancesResp{Data: []any{map[string]any{"id": req.OtID}}}, nil
	}}
	svc := pepSearch(authorizer, oq)
	allowed := &interfaces.KnSearchObjectType{ConceptID: "allowed"}
	denied := &interfaces.KnSearchObjectType{ConceptID: "denied"}

	if err := svc.fetchAuthorizedSampleData(context.Background(), "kn1", []*interfaces.KnSearchObjectType{allowed, denied}, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(queried, []string{"allowed"}) {
		t.Fatalf("denied object type reached ontology-query: %v", queried)
	}
	if allowed.SampleData == nil {
		t.Fatal("allowed object type should receive sample data")
	}
	if denied.SampleData != nil {
		t.Fatalf("denied object type must stay in schema without sample data: %+v", denied.SampleData)
	}
}

func TestFetchAuthorizedSampleData_AuthorizationFailureStopsFanout(t *testing.T) {
	ctx := context.Background()
	authErr := infraerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "safe unavailable")
	authorizer := &stubQueryCandidateAuthorizer{err: authErr}
	oq := &mockOntologyQuery{instancesResp: &interfaces.QueryObjectInstancesResp{}}
	svc := pepSearch(authorizer, oq)

	err := svc.fetchAuthorizedSampleData(ctx, "kn1", []*interfaces.KnSearchObjectType{{ConceptID: "ot1"}}, false)
	if !errors.Is(err, authErr) {
		t.Fatalf("expected authorization error, got %v", err)
	}
	if oq.calls() != 0 {
		t.Fatalf("ontology-query must not be called after authorization failure, calls=%d", oq.calls())
	}
}

func TestFetchAuthorizedSampleData_IncompleteQueryResponseFailsClosed(t *testing.T) {
	authorizer := &stubQueryCandidateAuthorizer{allowed: []string{"ot1"}}
	oq := &mockOntologyQuery{instancesFunc: func(_ *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
		return nil, nil
	}}
	svc := pepSearch(authorizer, oq)

	err := svc.fetchAuthorizedSampleData(context.Background(), "kn1", []*interfaces.KnSearchObjectType{{ConceptID: "ot1"}}, false)
	status, ok := infraerrors.HTTPStatus(err)
	if !ok || status != http.StatusServiceUnavailable {
		t.Fatalf("error=%v, want 503", err)
	}
}

func TestSemanticInstanceRetrieval_PEPFailureDoesNotReturnPartialNodes(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx := context.Background()
			dependencyErr := infraerrors.DefaultHTTPError(ctx, status, "ontology-query PEP failure")
			authorizer := &stubQueryCandidateAuthorizer{allowed: []string{"ot1", "ot2"}}
			oq := &mockOntologyQuery{instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
				if req.OtID == "ot2" {
					return nil, dependencyErr
				}
				return &interfaces.QueryObjectInstancesResp{Data: []any{
					map[string]any{"instance_name": "visible", "_score": 1.0},
				}}, nil
			}}
			svc := pepSearch(authorizer, oq)
			matchOnly := []interfaces.KnOperationType{interfaces.KnOperationTypeMatch}
			objectTypes := []*interfaces.KnSearchObjectType{
				{ConceptID: "ot1", DataProperties: []*interfaces.KnSearchDataProperty{{Name: "name", Type: "text", ConditionOperations: matchOnly}}},
				{ConceptID: "ot2", DataProperties: []*interfaces.KnSearchDataProperty{{Name: "name", Type: "text", ConditionOperations: matchOnly}}},
			}
			retrievalConfig := DefaultRetrievalConfig()
			retrievalConfig.SemanticInstanceRetrieval.ObjectTypeConcurrency = 2

			result, err := svc.semanticInstanceRetrieval(ctx, &interfaces.KnSearchLocalRequest{KnID: "kn1", Query: "visible"},
				objectTypes, retrievalConfig)
			if result != nil {
				t.Fatalf("authorization failure must not return partial nodes: %+v", result)
			}
			if !errors.Is(err, dependencyErr) {
				t.Fatalf("expected dependency error, got %v", err)
			}
		})
	}
}
