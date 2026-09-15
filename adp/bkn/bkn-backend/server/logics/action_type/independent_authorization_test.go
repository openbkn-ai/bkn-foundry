// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// independentActionFixture contains directly readable action types with different bindings. The
// caller holds nothing on ot-hidden and only query_data on ot-query; both remain usable when the
// action type itself is authorized.
func independentActionFixture() []*interfaces.ActionType {
	action := func(id, bound string) *interfaces.ActionType {
		return &interfaces.ActionType{
			ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: id, ObjectTypeID: bound},
			KNID:                   "kn-1",
			Branch:                 interfaces.MAIN_BRANCH,
		}
	}
	delegate := action("at-delegate", "ot-a")
	delegate.Affect = &interfaces.ActionAffect{ObjectTypeID: "ot-hidden"}
	delegate.ImpactContracts = []interfaces.ImpactContractItem{{ObjectTypeID: "ot-hidden"}}
	return []*interfaces.ActionType{
		action("at-in", "ot-a"),
		action("at-hidden", "ot-hidden"),
		action("at-query", "ot-query"),
		action("at-unbound", ""),
		delegate,
	}
}

func independentActionPermissions(objectTypeErr error) func(context.Context, string, []string, []string, bool,
	[]string) (map[string]interfaces.PermissionResourceOps, error) {
	return func(ctx context.Context, resourceType string, ids, visibility []string, _ bool,
		candidates []string) (map[string]interfaces.PermissionResourceOps, error) {
		if resourceType == interfaces.RESOURCE_TYPE_OBJECT_TYPE && objectTypeErr != nil {
			return nil, objectTypeErr
		}
		return allowAllActionPermissionResources(ctx, resourceType, ids, visibility, true, candidates)
	}
}

func newIndependentActionTestService(t *testing.T, objectTypeErr error) (*actionTypeService, *bmock.MockActionTypeAccess) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(independentActionPermissions(objectTypeErr)).AnyTimes()
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]*interfaces.ObjectType{}, nil).AnyTimes()
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &actionTypeService{ata: ata, ps: ps, ots: ots, ums: ums}, ata
}

func independentActionIDs(actionTypes []*interfaces.ActionType) []string {
	ids := make([]string, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		ids = append(ids, actionType.ATID)
	}
	return ids
}

func TestListActionTypes_KeepsDirectlyReadableActionsWithHiddenBindings(t *testing.T) {
	service, ata := newIndependentActionTestService(t, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(independentActionFixture(), nil)

	actionTypes, total, err := service.ListActionTypes(context.Background(), interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if err != nil {
		t.Fatalf("ListActionTypes() error = %v", err)
	}
	want := []string{"at-in", "at-hidden", "at-query", "at-unbound", "at-delegate"}
	if got := independentActionIDs(actionTypes); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListActionTypes() = %v, want %v", got, want)
	}
	if total != len(want) {
		t.Fatalf("ListActionTypes() total = %d, want %d", total, len(want))
	}
}

func TestListActionTypes_PagesDirectlyReadableActions(t *testing.T) {
	service, ata := newIndependentActionTestService(t, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(independentActionFixture(), nil)

	actionTypes, total, err := service.ListActionTypes(context.Background(), interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 1, Limit: 1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if err != nil {
		t.Fatalf("ListActionTypes() error = %v", err)
	}
	if got, want := independentActionIDs(actionTypes), []string{"at-hidden"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
}

func TestActionTypeReads_DoNotRequireBoundObjectPermissions(t *testing.T) {
	unavailable := errors.New("object type authorization unavailable")
	fixture := independentActionFixture()
	for _, tc := range []struct {
		name string
		ids  []string
		rows []*interfaces.ActionType
	}{
		{"hidden binding alone", []string{"at-hidden"}, fixture[1:2]},
		{"hidden binding in a batch", []string{"at-in", "at-hidden"}, fixture[:2]},
		{"query data binding", []string{"at-query"}, fixture[2:3]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, ata := newIndependentActionTestService(t, unavailable)
			ata.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, tc.ids).Return(tc.rows, nil)

			if _, err := service.GetActionTypesByIDs(context.Background(), "kn-1", interfaces.MAIN_BRANCH, tc.ids); err != nil {
				t.Fatalf("GetActionTypesByIDs() error = %v", err)
			}
		})
	}
}

func TestSearchActionTypes_KeepsDirectlyReadableActionsWithHiddenBindings(t *testing.T) {
	ctrl := gomock.NewController(t)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	objectTypeUnavailable := errors.New("object type authorization unavailable")
	ata.EXPECT().GetActionTypeIDsByKnID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).
		Return([]string{"at-hidden"}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(independentActionPermissions(objectTypeUnavailable)).AnyTimes()
	vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
		Return(&interfaces.DatasetQueryResponse{Entries: []map[string]any{{
			"id": "at-hidden", "name": "Independent action",
		}}}, nil)

	service := &actionTypeService{
		appSetting: &common.AppSetting{},
		ata:        ata,
		ps:         ps,
		vbs:        vbs,
	}
	result, err := service.SearchActionTypes(context.Background(), &interfaces.ConceptsQuery{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchActionTypes() error = %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].ATID != "at-hidden" {
		t.Fatalf("SearchActionTypes() = %#v, want at-hidden", result.Entries)
	}
}
