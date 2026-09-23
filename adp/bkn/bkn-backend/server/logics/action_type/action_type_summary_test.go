// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func newActionSummaryTestService(t *testing.T) (*actionTypeService, *bmock.MockActionTypeAccess,
	*bmock.MockPermissionService, *bmock.MockObjectTypeService, *bmock.MockUserMgmtService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	return &actionTypeService{ata: ata, ps: ps, ots: ots, ums: ums}, ata, ps, ots, ums
}

func summaryAction(id, objectTypeID string) *interfaces.ActionType {
	return &interfaces.ActionType{
		ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: id, ObjectTypeID: objectTypeID,
		},
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	}
}

func actionIDs(items []*interfaces.ActionType) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ATID)
	}
	return ids
}

func TestListActionTypeSummariesPushesAuthorizationIntoCountAndPage(t *testing.T) {
	service, ata, ps, ots, ums := newActionSummaryTestService(t)
	query := interfaces.ActionTypesQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, NamePattern: "order",
		ObjectTypeIDs: []string{"orders"},
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Offset: 20, Limit: 10, Sort: "f_name", Direction: interfaces.ASC_DIRECTION,
		},
	}
	visibleQuery := query
	visibleQuery.ValidAuthorizationIDsOnly = true
	visibleQuery.ATIDs = []string{"at-orders", "at-customers"}

	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{
		ResourceIDs: []string{"kn-2/ignored", "kn-1/at-orders", "kn-1/at-customers", "kn-1/at-orders"},
	}, nil)
	ata.EXPECT().GetActionTypesTotal(gomock.Any(), visibleQuery).Return(2, nil)
	page := []*interfaces.ActionType{summaryAction("at-orders", "orders")}
	ata.EXPECT().ListActionTypeSummaries(gomock.Any(), visibleQuery).Return(page, nil)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
		[]string{"kn-1/at-orders"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(
		map[string]interfaces.PermissionResourceOps{
			"kn-1/at-orders": {ResourceID: "kn-1/at-orders", Operations: []string{
				interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_EXECUTE,
			}},
		}, nil)
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"orders"}, false).Return(map[string]*interfaces.ObjectType{
		"orders": {
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders", OTName: "Orders"},
		},
	}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(2)).Return(nil)

	items, total, err := service.ListActionTypeSummaries(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || !reflect.DeepEqual(actionIDs(items), []string{"at-orders"}) {
		t.Fatalf("result = (%v, %d), want at-orders and total 2", actionIDs(items), total)
	}
	if items[0].ObjectType.OTName != "Orders" {
		t.Fatalf("object type was not hydrated: %#v", items[0].ObjectType)
	}
}

func TestListActionTypeSummariesFallsBackForWildcardScope(t *testing.T) {
	service, ata, ps, ots, ums := newActionSummaryTestService(t)
	query := interfaces.ActionTypesQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 1, Limit: 1},
	}
	candidateQuery := query
	candidateQuery.ValidAuthorizationIDsOnly = true
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1

	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{
		RequiresCandidateFilter: true,
	}, nil)
	candidates := []*interfaces.ActionType{
		summaryAction("denied", "orders"),
		summaryAction("visible-1", "orders"),
		summaryAction("visible-2", "customers"),
	}
	ata.EXPECT().ListActionTypeSummaries(gomock.Any(), candidateQuery).Return(candidates, nil)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
		[]string{"kn-1/denied", "kn-1/visible-1", "kn-1/visible-2"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/visible-1": {ResourceID: "kn-1/visible-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		"kn-1/visible-2": {ResourceID: "kn-1/visible-2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"customers"}, false).Return(map[string]*interfaces.ObjectType{}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Len(2)).Return(nil)

	items, total, err := service.ListActionTypeSummaries(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || !reflect.DeepEqual(actionIDs(items), []string{"visible-2"}) {
		t.Fatalf("result = (%v, %d), want second visible row and total 2", actionIDs(items), total)
	}
}

func TestListActionTypeSummariesReturnsEmptyForEmptyScope(t *testing.T) {
	service, _, ps, _, _ := newActionSummaryTestService(t)
	ps.EXPECT().ListAccessibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(interfaces.PermissionResourceScope{}, nil)

	items, total, err := service.ListActionTypeSummaries(context.Background(), interfaces.ActionTypesQueryParams{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("result = (%v, %d, %v), want empty", items, total, err)
	}
}
