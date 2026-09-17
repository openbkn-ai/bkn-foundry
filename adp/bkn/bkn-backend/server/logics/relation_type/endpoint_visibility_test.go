// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// endpointFixture is a network with three relation types the caller may read themselves:
// rt-in (ot-a -> ot-b), rt-out (ot-a -> ot-hidden) and rt-query (ot-b -> ot-query). The caller
// holds nothing on ot-hidden, and only query_data on ot-query.
func endpointFixture() []*interfaces.RelationType {
	relation := func(id, source, target string) *interfaces.RelationType {
		return &interfaces.RelationType{
			RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
				RTID: id, SourceObjectTypeID: source, TargetObjectTypeID: target,
			},
			KNID:   "kn-1",
			Branch: interfaces.MAIN_BRANCH,
		}
	}
	return []*interfaces.RelationType{
		relation("rt-in", "ot-a", "ot-b"),
		relation("rt-out", "ot-a", "ot-hidden"),
		relation("rt-query", "ot-b", "ot-query"),
	}
}

// endpointPermissions allows every relation type and answers object types the way the fixture
// describes. objectTypeErr, when set, fails the object type check.
func endpointPermissions(objectTypeErr error) func(context.Context, string, []string, []string) (map[string]interfaces.PermissionResourceOps, error) {
	objectTypeOps := map[string][]string{
		"kn-1/ot-a":     {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"kn-1/ot-b":     {interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA},
		"kn-1/ot-query": {interfaces.OPERATION_TYPE_QUERY_DATA},
	}
	return func(ctx context.Context, resourceType string, ids, visibility []string) (map[string]interfaces.PermissionResourceOps, error) {
		if resourceType != interfaces.RESOURCE_TYPE_OBJECT_TYPE {
			return allowAllRelationPermissionResources(ctx, resourceType, ids, visibility)
		}
		if objectTypeErr != nil {
			return nil, objectTypeErr
		}
		matched := map[string]interfaces.PermissionResourceOps{}
		for _, id := range ids {
			if ops, ok := objectTypeOps[id]; ok {
				matched[id] = interfaces.PermissionResourceOps{ResourceID: id, Operations: ops}
			}
		}
		return matched, nil
	}
}

func newEndpointTestService(t *testing.T, objectTypeErr error) (*relationTypeService, *bmock.MockRelationTypeAccess) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(endpointPermissions(objectTypeErr)).AnyTimes()
	ps.EXPECT().RequirePermissions(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]*interfaces.ObjectType{}, nil).AnyTimes()
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &relationTypeService{rta: rta, ps: ps, ots: ots, ums: ums}, rta
}

func relationIDs(relationTypes []*interfaces.RelationType) []string {
	ids := make([]string, 0, len(relationTypes))
	for _, relationType := range relationTypes {
		ids = append(ids, relationType.RTID)
	}
	return ids
}

// TestListRelationTypes_HidesRelationTypesWithAnEndpointTheCallerHoldsNothingOn pins #1532: an
// endpoint the caller holds no operation on must not surface through a relation type.
//
// The query_data-only endpoint stays: ontology-query reads relation types with the caller's
// identity to run their queries, and that caller is allowed to run them.
func TestListRelationTypes_HidesRelationTypesWithAnEndpointTheCallerHoldsNothingOn(t *testing.T) {
	service, rta := newEndpointTestService(t, nil)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(endpointFixture(), nil)

	relationTypes, total, err := service.ListRelationTypes(context.Background(), interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if err != nil {
		t.Fatalf("ListRelationTypes() error = %v", err)
	}
	if got, want := relationIDs(relationTypes), []string{"rt-in", "rt-query"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRelationTypes() = %v, want %v", got, want)
	}
	if total != 2 {
		t.Fatalf("ListRelationTypes() total = %d, want 2", total)
	}
}

// TestListRelationTypes_PagesAfterHidingEndpoints keeps paging honest: a hidden relation type
// must not occupy a slot on a page or count toward the total.
func TestListRelationTypes_PagesAfterHidingEndpoints(t *testing.T) {
	service, rta := newEndpointTestService(t, nil)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(endpointFixture(), nil)

	relationTypes, total, err := service.ListRelationTypes(context.Background(), interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 1, Limit: 1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if err != nil {
		t.Fatalf("ListRelationTypes() error = %v", err)
	}
	if got, want := relationIDs(relationTypes), []string{"rt-query"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
}

// TestListRelationTypes_EndpointAuthorizationFailureIsAnError refuses to read an authorization
// outage as "every endpoint is hidden".
func TestListRelationTypes_EndpointAuthorizationFailureIsAnError(t *testing.T) {
	unavailable := errors.New("authorization unavailable")
	service, rta := newEndpointTestService(t, unavailable)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(endpointFixture(), nil)

	_, _, err := service.ListRelationTypes(context.Background(), interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if !errors.Is(err, unavailable) {
		t.Fatalf("ListRelationTypes() error = %v, want %v", err, unavailable)
	}
}

// TestGetRelationTypesByIDs_RefusesARelationTypeWithAHiddenEndpoint keeps the by-id read all or
// nothing, like its permission check.
func TestGetRelationTypesByIDs_RefusesARelationTypeWithAHiddenEndpoint(t *testing.T) {
	fixture := endpointFixture()
	for _, tc := range []struct {
		name    string
		ids     []string
		rows    []*interfaces.RelationType
		wantErr bool
	}{
		{"hidden endpoint alone", []string{"rt-out"}, fixture[1:2], true},
		{"hidden endpoint in a batch", []string{"rt-in", "rt-out"}, fixture[:2], true},
		{"query_data endpoint", []string{"rt-query"}, fixture[2:3], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, rta := newEndpointTestService(t, nil)
			rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, tc.ids).Return(tc.rows, nil)

			_, err := service.GetRelationTypesByIDs(context.Background(), "kn-1", interfaces.MAIN_BRANCH, tc.ids)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("GetRelationTypesByIDs() error = %v", err)
				}
				return
			}
			var httpErr *rest.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
				t.Fatalf("GetRelationTypesByIDs() error = %v, want 403", err)
			}
		})
	}
}

// TestReadableRelationTypes_AppliesTheReadRule pins the rule relation type paths reuse (#1553):
// view_detail on the relation type itself, and at least one operation on both of its endpoints.
func TestReadableRelationTypes_AppliesTheReadRule(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	objectTypes := endpointPermissions(nil)
	filter := func(ctx context.Context, resourceType string, ids, visibility []string) (map[string]interfaces.PermissionResourceOps, error) {
		if resourceType == interfaces.RESOURCE_TYPE_OBJECT_TYPE {
			return objectTypes(ctx, resourceType, ids, visibility)
		}
		matched := map[string]interfaces.PermissionResourceOps{}
		for _, id := range ids {
			// rt-query is granted query_data alone, which does not make it readable.
			if id != "kn-1/rt-query" {
				matched[id] = interfaces.PermissionResourceOps{ResourceID: id,
					Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}}
			}
		}
		return matched, nil
	}
	ps.EXPECT().FilterVisibleResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(filter).AnyTimes()
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(filter).AnyTimes()

	readable, err := ReadableRelationTypes(context.Background(), ps, "kn-1", endpointFixture())

	if err != nil {
		t.Fatalf("ReadableRelationTypes() error = %v", err)
	}
	// rt-out points at an object type the caller holds nothing on.
	if got, want := relationIDs(readable), []string{"rt-in"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadableRelationTypes() = %v, want %v", got, want)
	}
}

// TestWithVisibleEndpointIDs_NarrowsSearchCandidatesInOrder covers the ids search restricts its
// dataset query to, so a hidden relation type is neither returned nor counted.
func TestWithVisibleEndpointIDs_NarrowsSearchCandidatesInOrder(t *testing.T) {
	service, rta := newEndpointTestService(t, nil)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(endpointFixture(), nil)

	ids, err := service.withVisibleEndpointIDs(context.Background(), "kn-1", "",
		[]string{"rt-query", "rt-out", "rt-in"})

	if err != nil {
		t.Fatalf("withVisibleEndpointIDs() error = %v", err)
	}
	if want := []string{"rt-query", "rt-in"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("withVisibleEndpointIDs() = %v, want %v", ids, want)
	}
}
