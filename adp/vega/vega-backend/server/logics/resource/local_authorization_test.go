// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package resource

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

type localDecisionKey struct {
	resourceType string
	resourceID   string
	operation    string
}

type localPermissionServiceStub struct {
	interfaces.PermissionService
	decisions  map[localDecisionKey]interfaces.PermissionOperationDecision
	errors     map[localDecisionKey]error
	calls      []localDecisionKey
	batchCalls []interfaces.LocalPermissionFilter
}

func (s *localPermissionServiceStub) LocalDecision(_ context.Context, resource interfaces.PermissionResource,
	operation string) (interfaces.PermissionOperationDecision, error) {
	key := localDecisionKey{resourceType: resource.Type, resourceID: resource.ID, operation: operation}
	s.calls = append(s.calls, key)
	if err := s.errors[key]; err != nil {
		return interfaces.PermissionOperationDecision{}, err
	}
	if decision, ok := s.decisions[key]; ok {
		return copyDecisionForOperation(decision, operation), nil
	}
	return noneDecision(operation), nil
}

func (s *localPermissionServiceStub) LocalResourceDecisions(_ context.Context, resourceType string,
	ids, operations []string) (map[string]map[string]interfaces.PermissionOperationDecision, error) {
	s.batchCalls = append(s.batchCalls, interfaces.LocalPermissionFilter{
		ResourceType: resourceType, ResourceIDs: append([]string(nil), ids...), Operations: append([]string(nil), operations...),
	})
	result := make(map[string]map[string]interfaces.PermissionOperationDecision, len(ids))
	for _, id := range ids {
		result[id] = make(map[string]interfaces.PermissionOperationDecision, len(operations))
		for _, operation := range operations {
			key := localDecisionKey{resourceType: resourceType, resourceID: id, operation: operation}
			if err := s.errors[key]; err != nil {
				return nil, err
			}
			decision, ok := s.decisions[key]
			if !ok {
				decision = noneDecision(operation)
			}
			result[id][operation] = copyDecisionForOperation(decision, operation)
		}
	}
	return result, nil
}

func noneDecision(operation string) interfaces.PermissionOperationDecision {
	return interfaces.PermissionOperationDecision{
		Operation: operation, Decision: interfaces.PermissionDecisionNone, Basis: interfaces.PermissionBasisNone,
	}
}

func localDecision(decision interfaces.PermissionDecision,
	basis interfaces.PermissionDecisionBasis, requires ...string) interfaces.PermissionOperationDecision {
	return interfaces.PermissionOperationDecision{Decision: decision, Basis: basis, Requires: requires}
}

func TestLocalOperationDecisionPriority(t *testing.T) {
	resourceKey := func(operation string) localDecisionKey {
		return localDecisionKey{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", operation}
	}
	catalogKey := func(operation string) localDecisionKey {
		return localDecisionKey{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", operation}
	}
	tests := []struct {
		name          string
		resource      interfaces.PermissionOperationDecision
		catalog       interfaces.PermissionOperationDecision
		wantDecision  interfaces.PermissionDecision
		wantBasis     interfaces.PermissionDecisionBasis
		wantCallCount int
	}{
		{
			name:         "resource deny is terminal over catalog allow",
			resource:     localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
			catalog:      localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
			wantDecision: interfaces.PermissionDecisionDeny, wantBasis: interfaces.PermissionBasisDirect,
			wantCallCount: 1,
		},
		{
			name:         "resource direct allow is terminal over catalog deny",
			resource:     localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
			catalog:      localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
			wantDecision: interfaces.PermissionDecisionAllow, wantBasis: interfaces.PermissionBasisDirect,
			wantCallCount: 1,
		},
		{
			name:         "catalog allow fills resource none",
			resource:     noneDecision(interfaces.OPERATION_TYPE_VIEW_DETAIL),
			catalog:      localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
			wantDecision: interfaces.PermissionDecisionAllow, wantBasis: interfaces.PermissionBasisInherited,
			wantCallCount: 2,
		},
		{
			name:         "catalog deny beats resource wildcard allow",
			resource:     localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisWildcard),
			catalog:      localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
			wantDecision: interfaces.PermissionDecisionDeny, wantBasis: interfaces.PermissionBasisInherited,
			wantCallCount: 2,
		},
		{
			name:         "resource wildcard allow is the final fallback",
			resource:     localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisWildcard),
			catalog:      noneDecision(interfaces.OPERATION_TYPE_VIEW_DETAIL),
			wantDecision: interfaces.PermissionDecisionAllow, wantBasis: interfaces.PermissionBasisWildcard,
			wantCallCount: 2,
		},
		{
			name:         "two none decisions become default deny",
			resource:     noneDecision(interfaces.OPERATION_TYPE_VIEW_DETAIL),
			catalog:      noneDecision(interfaces.OPERATION_TYPE_VIEW_DETAIL),
			wantDecision: interfaces.PermissionDecisionDeny, wantBasis: interfaces.PermissionBasisDefault,
			wantCallCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &localPermissionServiceStub{decisions: map[localDecisionKey]interfaces.PermissionOperationDecision{
				resourceKey(interfaces.OPERATION_TYPE_VIEW_DETAIL): tt.resource,
				catalogKey(interfaces.OPERATION_TYPE_VIEW_DETAIL):  tt.catalog,
			}}
			rs := &resourceService{ps: stub}

			got, err := rs.localOperationDecision(context.Background(), "resource-1", "catalog-1",
				interfaces.OPERATION_TYPE_VIEW_DETAIL)

			require.NoError(t, err)
			assert.Equal(t, tt.wantDecision, got.Decision)
			assert.Equal(t, tt.wantBasis, got.Basis)
			assert.Len(t, stub.calls, tt.wantCallCount)
		})
	}
}

func TestLocalOperationDecisionRequiresUsesTheSameComposition(t *testing.T) {
	stub := &localPermissionServiceStub{decisions: map[localDecisionKey]interfaces.PermissionOperationDecision{
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", interfaces.OPERATION_TYPE_RESOURCE_MANAGE}: localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect,
			interfaces.OPERATION_TYPE_VIEW_DETAIL),
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}: localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}:   localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
	}}
	rs := &resourceService{ps: stub}

	got, err := rs.localOperationDecision(context.Background(), "resource-1", "catalog-1",
		interfaces.OPERATION_TYPE_MODIFY)

	require.NoError(t, err)
	assert.Equal(t, interfaces.PermissionDecisionDeny, got.Decision)
	assert.Equal(t, interfaces.PermissionBasisRequires, got.Basis)
	assert.Equal(t, interfaces.OPERATION_TYPE_VIEW_DETAIL, got.DeniedRequirement)
	assert.Equal(t, interfaces.PermissionBasisDirect, got.RequirementBasis)
	assert.Len(t, stub.calls, 2, "the direct requirement deny must also short-circuit Catalog")

	stub.calls = nil
	err = rs.checkResourceOrCatalog(context.Background(), "resource-1", "catalog-1", false,
		interfaces.OPERATION_TYPE_MODIFY)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	assert.Equal(t, map[string]any{
		"message":            "Access denied: insufficient permissions for[modify]",
		"basis":              interfaces.PermissionBasisRequires,
		"denied_requirement": interfaces.OPERATION_TYPE_VIEW_DETAIL,
		"requirement_basis":  interfaces.PermissionBasisDirect,
	}, httpErr.BaseError.ErrorDetails)
}

func TestLocalOperationDecisionCatalogRequiresStillUsesResourceComposition(t *testing.T) {
	stub := &localPermissionServiceStub{decisions: map[localDecisionKey]interfaces.PermissionOperationDecision{
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", interfaces.OPERATION_TYPE_RESOURCE_MANAGE}: localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect,
			interfaces.OPERATION_TYPE_AUTHORIZE),
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", interfaces.OPERATION_TYPE_AUTHORIZE}: localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
	}}
	rs := &resourceService{ps: stub}

	got, err := rs.localOperationDecision(context.Background(), "resource-1", "catalog-1",
		interfaces.OPERATION_TYPE_MODIFY)

	require.NoError(t, err)
	assert.Equal(t, interfaces.PermissionDecisionDeny, got.Decision)
	assert.Equal(t, interfaces.PermissionBasisRequires, got.Basis)
	assert.Equal(t, interfaces.OPERATION_TYPE_AUTHORIZE, got.DeniedRequirement)
	assert.Equal(t, interfaces.PermissionBasisDefault, got.RequirementBasis)
	assert.Len(t, stub.calls, 1, "an intentionally unmapped Resource operation must not become a Catalog grant")
}

func TestLocalOperationDecisionDoesNotTreatFailuresAsNone(t *testing.T) {
	want := errors.New("bkn-safe unavailable")
	key := localDecisionKey{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}
	stub := &localPermissionServiceStub{errors: map[localDecisionKey]error{key: want}}
	rs := &resourceService{ps: stub}

	_, err := rs.localOperationDecision(context.Background(), "resource-1", "catalog-1",
		interfaces.OPERATION_TYPE_VIEW_DETAIL)

	require.ErrorIs(t, err, want)
	assert.Len(t, stub.calls, 1)
}

func TestCheckResourceOrCatalogMapsInactiveAccountToForbidden(t *testing.T) {
	key := localDecisionKey{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}
	stub := &localPermissionServiceStub{errors: map[localDecisionKey]error{
		key: interfaces.ErrPermissionAccountNotActive,
	}}
	rs := &resourceService{ps: stub}

	err := rs.checkResourceOrCatalog(context.Background(), "resource-1", "catalog-1", false,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	assert.Len(t, stub.calls, 1)
}

func TestFilterResourcePermissionsMapsInactiveAccountToEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := vmock.NewMockResourceAccess(ctrl)
	access.EXPECT().GetPermissionRefsByIDs(gomock.Any(), []string{"resource-1"}).Return([]interfaces.ResourcePermissionRef{
		{ResourceID: "resource-1", CatalogID: "catalog-1"},
	}, nil)
	key := localDecisionKey{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}
	stub := &localPermissionServiceStub{errors: map[localDecisionKey]error{
		key: interfaces.ErrPermissionAccountNotActive,
	}}
	rs := &resourceService{ps: stub, ra: access}

	got, err := rs.filterResourcePermissions(context.Background(), []string{"resource-1"}, nil,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLocalFilterResourcePermissionsUsesFinalDecisions(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := vmock.NewMockResourceAccess(ctrl)
	ids := []string{"resource-deny", "resource-allow", "resource-wild-deny", "resource-wild-none"}
	access.EXPECT().GetPermissionRefsByIDs(gomock.Any(), ids).Return([]interfaces.ResourcePermissionRef{
		{ResourceID: "resource-deny", CatalogID: "catalog-allow"},
		{ResourceID: "resource-allow", CatalogID: "catalog-deny"},
		{ResourceID: "resource-wild-deny", CatalogID: "catalog-deny"},
		{ResourceID: "resource-wild-none", CatalogID: "catalog-none"},
	}, nil)
	view := interfaces.OPERATION_TYPE_VIEW_DETAIL
	stub := &localPermissionServiceStub{decisions: map[localDecisionKey]interfaces.PermissionOperationDecision{
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-deny", view}:      localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-allow", view}:     localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-wild-deny", view}: localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisWildcard),
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-wild-none", view}: localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisWildcard),
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-allow", view}:       localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-deny", view}:        localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
	}}
	rs := &resourceService{ps: stub, ra: access}

	got, err := rs.localFilterResourcePermissions(context.Background(), ids, []string{view}, true)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"resource-allow", "resource-wild-none"}, mapKeysOfResourceOps(got))
	assert.Equal(t, []string{view}, got["resource-allow"].Operations)
	assert.Equal(t, []string{view}, got["resource-wild-none"].Operations)
	assert.Len(t, stub.batchCalls, 2, "batch calls must scale by resource type, not by resource count")
}

func TestLocalFilterResourcePermissionsEnforcesRequirements(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := vmock.NewMockResourceAccess(ctrl)
	access.EXPECT().GetPermissionRefsByIDs(gomock.Any(), []string{"resource-1"}).Return([]interfaces.ResourcePermissionRef{
		{ResourceID: "resource-1", CatalogID: "catalog-1"},
	}, nil)
	stub := &localPermissionServiceStub{decisions: map[localDecisionKey]interfaces.PermissionOperationDecision{
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", interfaces.OPERATION_TYPE_RESOURCE_MANAGE}: localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect,
			interfaces.OPERATION_TYPE_VIEW_DETAIL),
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}: localDecision(interfaces.PermissionDecisionDeny, interfaces.PermissionBasisDirect),
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, "catalog-1", interfaces.OPERATION_TYPE_VIEW_DETAIL}:   localDecision(interfaces.PermissionDecisionAllow, interfaces.PermissionBasisDirect),
	}}
	rs := &resourceService{ps: stub, ra: access}

	got, err := rs.localFilterResourcePermissions(context.Background(), []string{"resource-1"}, nil, true)

	require.NoError(t, err)
	entry := got["resource-1"]
	assert.NotContains(t, entry.Operations, interfaces.OPERATION_TYPE_MODIFY)
	assert.NotContains(t, entry.Operations, interfaces.OPERATION_TYPE_DELETE)
}

func mapKeysOfResourceOps(values map[string]interfaces.PermissionResourceOps) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}
