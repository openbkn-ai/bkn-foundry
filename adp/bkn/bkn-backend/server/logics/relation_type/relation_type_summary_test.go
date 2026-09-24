// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func newRelationSummaryTestService(t *testing.T) (*relationTypeService, *bmock.MockRelationTypeAccess,
	*bmock.MockPermissionService, *bmock.MockObjectTypeService, *bmock.MockUserMgmtService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	return &relationTypeService{rta: rta, ps: ps, ots: ots, ums: ums}, rta, ps, ots, ums
}

func summaryRelation(id, source, target string) *interfaces.RelationType {
	return &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: id, SourceObjectTypeID: source, TargetObjectTypeID: target,
		},
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	}
}

func TestListRelationTypeSummariesPushesAuthorizationIntoCountAndPage(t *testing.T) {
	service, rta, ps, ots, ums := newRelationSummaryTestService(t)
	query := interfaces.RelationTypesQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, NamePattern: "order",
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Offset: 20, Limit: 10, Sort: "f_name", Direction: interfaces.ASC_DIRECTION,
		},
	}
	visibleQuery := query
	visibleQuery.ValidAuthorizationIDsOnly = true
	visibleQuery.RTIDS = []string{"rt-orders", "rt-customers"}
	visibleQuery.SourceObjectTypeIDs = []string{"orders", "customers", "query-only"}
	visibleQuery.TargetObjectTypeIDs = []string{"orders", "customers", "query-only"}

	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{
		ResourceIDs: []string{"kn-2/ignored", "kn-1/rt-orders", "kn-1/rt-customers", "kn-1/rt-orders"},
	}, nil)
	ps.EXPECT().ListAccessibleResourcesWithAnyOperation(gomock.Any(),
		interfaces.RESOURCE_TYPE_OBJECT_TYPE).Return(interfaces.PermissionResourceScope{
		ResourceIDs: []string{"kn-1/orders", "kn-1/customers", "kn-1/query-only"},
	}, nil)
	rta.EXPECT().GetRelationTypesTotal(gomock.Any(), visibleQuery).Return(2, nil)
	page := []*interfaces.RelationType{summaryRelation("rt-orders", "orders", "query-only")}
	rta.EXPECT().ListRelationTypeSummaries(gomock.Any(), visibleQuery).Return(page, nil)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		[]string{"kn-1/rt-orders"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(
		map[string]interfaces.PermissionResourceOps{
			"kn-1/rt-orders": {ResourceID: "kn-1/rt-orders", Operations: []string{
				interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY,
			}},
		}, nil)
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"orders", "query-only"}, false).Return(map[string]*interfaces.ObjectType{
		"orders": {
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders", OTName: "Orders"},
		},
		"query-only": {
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "query-only", OTName: "Query only"},
		},
	}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(2)).Return(nil)

	items, total, err := service.ListRelationTypeSummaries(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 1 || items[0].RTID != "rt-orders" {
		t.Fatalf("result = (%v, %d), want rt-orders and total 2", relationIDs(items), total)
	}
	if items[0].TargetObjectType.OTName != "Query only" {
		t.Fatalf("query-only endpoint was not hydrated: %#v", items[0].TargetObjectType)
	}
	if !reflect.DeepEqual(items[0].Operations,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY}) {
		t.Fatalf("operations = %v", items[0].Operations)
	}
}

func TestListRelationTypeSummariesFallsBackOnlyForWildcardDimension(t *testing.T) {
	service, rta, ps, ots, ums := newRelationSummaryTestService(t)
	query := interfaces.RelationTypesQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 1, Limit: 1},
	}
	candidateQuery := query
	candidateQuery.ValidAuthorizationIDsOnly = true
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	candidateQuery.SourceObjectTypeIDs = []string{"a", "b"}
	candidateQuery.TargetObjectTypeIDs = []string{"a", "b"}

	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{
		RequiresCandidateFilter: true,
	}, nil)
	ps.EXPECT().ListAccessibleResourcesWithAnyOperation(gomock.Any(),
		interfaces.RESOURCE_TYPE_OBJECT_TYPE).Return(interfaces.PermissionResourceScope{
		ResourceIDs: []string{"kn-1/a", "kn-1/b"},
	}, nil)
	candidates := []*interfaces.RelationType{
		summaryRelation("denied", "a", "b"),
		summaryRelation("visible-1", "a", "b"),
		summaryRelation("visible-2", "b", "a"),
	}
	rta.EXPECT().ListRelationTypeSummaries(gomock.Any(), candidateQuery).Return(candidates, nil)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		[]string{"kn-1/denied", "kn-1/visible-1", "kn-1/visible-2"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/visible-1": {ResourceID: "kn-1/visible-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		"kn-1/visible-2": {ResourceID: "kn-1/visible-2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"b", "a"}, false).Return(map[string]*interfaces.ObjectType{}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(2)).Return(nil)

	items, total, err := service.ListRelationTypeSummaries(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || !reflect.DeepEqual(relationIDs(items), []string{"visible-2"}) {
		t.Fatalf("result = (%v, %d), want second visible row and total 2", relationIDs(items), total)
	}
}

func TestListRelationTypeSummariesReturnsEmptyWithoutStorageReadWhenEndpointScopeIsEmpty(t *testing.T) {
	service, _, ps, _, _ := newRelationSummaryTestService(t)
	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{Unrestricted: true}, nil)
	ps.EXPECT().ListAccessibleResourcesWithAnyOperation(gomock.Any(),
		interfaces.RESOURCE_TYPE_OBJECT_TYPE).Return(interfaces.PermissionResourceScope{ResourceIDs: []string{}}, nil)

	items, total, err := service.ListRelationTypeSummaries(context.Background(), interfaces.RelationTypesQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("result = (%v, %d, %v), want empty", items, total, err)
	}
}

func TestFilterRelationSummaryOperationsDropsInvalidAuthorizationIDs(t *testing.T) {
	service, _, ps, _, _ := newRelationSummaryTestService(t)
	items := []*interfaces.RelationType{
		summaryRelation("valid", "source", "target"),
		summaryRelation("bad/id", "source", "target"),
		summaryRelation("*", "source", "target"),
	}
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		[]string{"kn-1/valid"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(
		map[string]interfaces.PermissionResourceOps{
			"kn-1/valid": {ResourceID: "kn-1/valid", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		}, nil)

	visible, err := service.filterRelationSummaryOperations(context.Background(), "kn-1", items)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(relationIDs(visible), []string{"valid"}) {
		t.Fatalf("visible relation IDs = %v, want only valid", relationIDs(visible))
	}
}

func TestRelationSummaryAuthorizationPredicateCountIncludesRepeatedBoundPredicate(t *testing.T) {
	query := interfaces.RelationTypesQueryParams{
		RTIDS:               []string{"r1", "r2"},
		SourceObjectTypeIDs: []string{"s1"},
		TargetObjectTypeIDs: []string{"t1", "t2"},
		BoundObjectTypeIDs:  []string{"b1", "b2"},
	}
	if got := relationSummaryAuthorizationPredicateCount(query, true, true); got != 9 {
		t.Fatalf("predicate count = %d, want 9", got)
	}
	if got := relationSummaryAuthorizationPredicateCount(query, false, true); got != 7 {
		t.Fatalf("object-only predicate count = %d, want 7", got)
	}
}
