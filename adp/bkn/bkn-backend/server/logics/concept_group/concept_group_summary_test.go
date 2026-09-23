// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package concept_group

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestListConceptGroupSummariesPushesAuthorizationAndBatchesMembers(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := bmock.NewMockConceptGroupAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	service := &conceptGroupService{cga: cga, ps: ps, ums: ums}
	query := interfaces.ConceptGroupsQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Offset: 10, Limit: 10, Sort: "f_name", Direction: interfaces.ASC_DIRECTION,
		},
	}
	visibleQuery := query
	visibleQuery.ValidAuthorizationIDsOnly = true
	visibleQuery.CGIDs = []string{"cg-1", "cg-2"}

	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{
		ResourceIDs: []string{"kn-1/cg-1", "kn-1/cg-2"},
	}, nil)
	cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), visibleQuery).Return(2, nil)
	page := []*interfaces.ConceptGroup{{CGID: "cg-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}}
	cga.EXPECT().ListConceptGroups(gomock.Any(), visibleQuery).Return(page, nil)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		[]string{"kn-1/cg-2"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(
		map[string]interfaces.PermissionResourceOps{
			"kn-1/cg-2": {ResourceID: "kn-1/cg-2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(2)).Return(nil)
	cga.EXPECT().GetConceptIDsGroupedByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-2"}, interfaces.MODULE_TYPE_OBJECT_TYPE).Return(map[string][]string{"cg-2": {}}, nil)

	items, total, err := service.ListConceptGroupSummaries(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 1 || items[0].CGID != "cg-2" {
		t.Fatalf("result = (%v, %d), want cg-2 and total 2", items, total)
	}
	if !reflect.DeepEqual(items[0].ObjectTypeIDs, []string{}) || items[0].Statistics == nil {
		t.Fatalf("members were not hydrated: %#v", items[0])
	}
}

func TestListConceptGroupSummariesReturnsEmptyForEmptyScope(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	service := &conceptGroupService{ps: ps}
	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{}, nil)

	items, total, err := service.ListConceptGroupSummaries(context.Background(),
		interfaces.ConceptGroupsQueryParams{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("result = (%v, %d, %v), want empty", items, total, err)
	}
}
