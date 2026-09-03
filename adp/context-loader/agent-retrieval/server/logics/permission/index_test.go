// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type fakePermissionAccess struct {
	allowed  map[string]bool
	requests []interfaces.PermissionFilterRequest
	err      error
	errAt    int
	mutate   func(*[]interfaces.PermissionFilterResult)
}

func (f *fakePermissionAccess) FilterResources(_ context.Context,
	request interfaces.PermissionFilterRequest,
) (interfaces.PermissionFilterResponse, error) {
	f.requests = append(f.requests, request)
	if f.err != nil && (f.errAt == 0 || len(f.requests) == f.errAt) {
		return interfaces.PermissionFilterResponse{}, f.err
	}
	results := make([]interfaces.PermissionFilterResult, 0)
	for _, resource := range request.Resources {
		if f.allowed[resource.ID] {
			results = append(results, interfaces.PermissionFilterResult{
				ResourceType: resource.Type,
				ResourceID:   resource.ID,
				Operations:   []string{interfaces.PermissionOperationQueryData},
			})
		}
	}
	if f.mutate != nil {
		f.mutate(&results)
	}
	return interfaces.PermissionFilterResponse{Resources: &results}, nil
}

func TestFilterObjectTypeIDsFailsWholeRequestWhenLaterChunkFails(t *testing.T) {
	access := &fakePermissionAccess{
		allowed: map[string]bool{"kn-a/ot-a": true, "kn-a/ot-b": true},
		err:     errors.New("timeout"),
		errAt:   2,
	}
	authorizer := NewQueryCandidateAuthorizerWith(access, 1)

	got, err := authorizer.FilterObjectTypeIDs(authorizedContext(), "kn-a", []string{"ot-a", "ot-b"})
	if got != nil {
		t.Fatalf("partial authorization result leaked: %v", got)
	}
	status, ok := infraerrors.HTTPStatus(err)
	if !ok || status != http.StatusServiceUnavailable || len(access.requests) != 2 {
		t.Fatalf("error=%v calls=%d, want 503 after second chunk", err, len(access.requests))
	}
}

func authorizedContext() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
}

func TestFilterObjectTypeIDsChunksDeduplicatesAndPreservesOrder(t *testing.T) {
	access := &fakePermissionAccess{allowed: map[string]bool{
		"kn-a/ot-a": true,
		"kn-a/ot-c": true,
	}}
	authorizer := NewQueryCandidateAuthorizerWith(access, 2)

	got, err := authorizer.FilterObjectTypeIDs(authorizedContext(), "kn-a", []string{"ot-a", "ot-b", "ot-a", "ot-c"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ot-a", "ot-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed = %v, want %v", got, want)
	}
	if len(access.requests) != 2 {
		t.Fatalf("calls = %d, want 2", len(access.requests))
	}
	for _, request := range access.requests {
		if request.AccessorID != "user-1" || !reflect.DeepEqual(request.VisibilityOperations, []string{"query_data"}) ||
			!reflect.DeepEqual(request.CandidateOperations, []string{"query_data"}) {
			t.Fatalf("request contract = %#v", request)
		}
	}
}

func TestFilterObjectTypeIDsTreatsEmptySafeResultAsDenyAll(t *testing.T) {
	authorizer := NewQueryCandidateAuthorizerWith(&fakePermissionAccess{allowed: map[string]bool{}}, 10)
	got, err := authorizer.FilterObjectTypeIDs(authorizedContext(), "kn-a", []string{"ot-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("allowed = %v, want empty", got)
	}
}

func TestFilterObjectTypeIDsRejectsMissingSubjectBeforeSafe(t *testing.T) {
	access := &fakePermissionAccess{}
	authorizer := NewQueryCandidateAuthorizerWith(access, 10)
	_, err := authorizer.FilterObjectTypeIDs(context.Background(), "kn-a", []string{"ot-a"})
	status, ok := infraerrors.HTTPStatus(err)
	if !ok || status != http.StatusUnauthorized {
		t.Fatalf("error = %v, want 401", err)
	}
	if len(access.requests) != 0 {
		t.Fatal("Safe must not be called without a trusted subject")
	}
}

func TestFilterObjectTypeIDsFailsClosedOnSafeErrorsAndInvalidRows(t *testing.T) {
	tests := []struct {
		name   string
		access *fakePermissionAccess
	}{
		{name: "transport error", access: &fakePermissionAccess{err: errors.New("timeout")}},
		{name: "unexpected resource", access: &fakePermissionAccess{allowed: map[string]bool{"kn-a/ot-a": true}, mutate: func(rows *[]interfaces.PermissionFilterResult) {
			(*rows)[0].ResourceID = "kn-b/ot-a"
		}}},
		{name: "wrong type", access: &fakePermissionAccess{allowed: map[string]bool{"kn-a/ot-a": true}, mutate: func(rows *[]interfaces.PermissionFilterResult) {
			(*rows)[0].ResourceType = "relation_type"
		}}},
		{name: "missing operation", access: &fakePermissionAccess{allowed: map[string]bool{"kn-a/ot-a": true}, mutate: func(rows *[]interfaces.PermissionFilterResult) {
			(*rows)[0].Operations = nil
		}}},
		{name: "duplicate row", access: &fakePermissionAccess{allowed: map[string]bool{"kn-a/ot-a": true}, mutate: func(rows *[]interfaces.PermissionFilterResult) {
			*rows = append(*rows, (*rows)[0])
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := NewQueryCandidateAuthorizerWith(tt.access, 10)
			_, err := authorizer.FilterObjectTypeIDs(authorizedContext(), "kn-a", []string{"ot-a"})
			status, ok := infraerrors.HTTPStatus(err)
			if !ok || status != http.StatusServiceUnavailable {
				t.Fatalf("error = %v, want 503", err)
			}
		})
	}
}

func TestFilterObjectTypeIDsKeepsSameChildSeparateAcrossNetworks(t *testing.T) {
	access := &fakePermissionAccess{allowed: map[string]bool{"kn-b/shared": true}}
	authorizer := NewQueryCandidateAuthorizerWith(access, 10)
	got, err := authorizer.FilterObjectTypeIDs(authorizedContext(), "kn-a", []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || access.requests[0].Resources[0].ID != "kn-a/shared" {
		t.Fatalf("cross-network candidate leaked: got=%v request=%#v", got, access.requests[0])
	}
}
