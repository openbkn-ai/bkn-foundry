// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

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

// boundFixture is a network with action types the caller may read themselves. The caller holds
// nothing on ot-hidden and only query_data on ot-query.
//   - at-in is bound to ot-a;
//   - at-hidden is bound to ot-hidden;
//   - at-query is bound to ot-query;
//   - at-unbound has no bound object type;
//   - at-delegate is bound to ot-a but affects ot-hidden, the delegated-write case.
func boundFixture() []*interfaces.ActionType {
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

func boundPermissions(objectTypeErr error) func(context.Context, string, []string, []string, bool,
	[]string) (map[string]interfaces.PermissionResourceOps, error) {
	objectTypeOps := map[string][]string{
		"kn-1/ot-a":     {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"kn-1/ot-query": {interfaces.OPERATION_TYPE_QUERY_DATA},
	}
	return func(ctx context.Context, resourceType string, ids, visibility []string, _ bool,
		candidates []string) (map[string]interfaces.PermissionResourceOps, error) {
		if resourceType != interfaces.RESOURCE_TYPE_OBJECT_TYPE {
			return allowAllActionPermissionResources(ctx, resourceType, ids, visibility, true, candidates)
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

func newBoundTestService(t *testing.T, objectTypeErr error) (*actionTypeService, *bmock.MockActionTypeAccess) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(boundPermissions(objectTypeErr)).AnyTimes()
	ots.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]*interfaces.ObjectType{}, nil).AnyTimes()
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &actionTypeService{ata: ata, ps: ps, ots: ots, ums: ums}, ata
}

func actionIDs(actionTypes []*interfaces.ActionType) []string {
	ids := make([]string, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		ids = append(ids, actionType.ATID)
	}
	return ids
}

// TestListActionTypes_HidesActionTypesBoundToAnObjectTypeTheCallerHoldsNothingOn pins #1532 for
// action types, and the two cases that must stay: a query_data-only binding, which the caller may
// run, and a delegated write to an object type the caller cannot read.
func TestListActionTypes_HidesActionTypesBoundToAnObjectTypeTheCallerHoldsNothingOn(t *testing.T) {
	service, ata := newBoundTestService(t, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(boundFixture(), nil)

	actionTypes, total, err := service.ListActionTypes(context.Background(), interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if err != nil {
		t.Fatalf("ListActionTypes() error = %v", err)
	}
	want := []string{"at-in", "at-query", "at-unbound", "at-delegate"}
	if got := actionIDs(actionTypes); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListActionTypes() = %v, want %v", got, want)
	}
	if total != len(want) {
		t.Fatalf("ListActionTypes() total = %d, want %d", total, len(want))
	}
}

// TestListActionTypes_PagesAfterHidingBoundObjectTypes keeps a hidden action type out of every
// page and out of the total.
func TestListActionTypes_PagesAfterHidingBoundObjectTypes(t *testing.T) {
	service, ata := newBoundTestService(t, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(boundFixture(), nil)

	actionTypes, total, err := service.ListActionTypes(context.Background(), interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 1, Limit: 1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if err != nil {
		t.Fatalf("ListActionTypes() error = %v", err)
	}
	if got, want := actionIDs(actionTypes), []string{"at-query"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
}

func TestListActionTypes_BoundObjectTypeAuthorizationFailureIsAnError(t *testing.T) {
	unavailable := errors.New("authorization unavailable")
	service, ata := newBoundTestService(t, unavailable)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(boundFixture(), nil)

	_, _, err := service.ListActionTypes(context.Background(), interfaces.ActionTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if !errors.Is(err, unavailable) {
		t.Fatalf("ListActionTypes() error = %v, want %v", err, unavailable)
	}
}

// TestGetActionTypesByIDs_RefusesAnActionTypeBoundToAHiddenObjectType keeps the by-id read, which
// ontology-query uses to run actions, all or nothing -- and open for the cases that must run.
func TestGetActionTypesByIDs_RefusesAnActionTypeBoundToAHiddenObjectType(t *testing.T) {
	fixture := boundFixture()
	for _, tc := range []struct {
		name    string
		ids     []string
		rows    []*interfaces.ActionType
		wantErr bool
	}{
		{"hidden binding alone", []string{"at-hidden"}, fixture[1:2], true},
		{"hidden binding in a batch", []string{"at-in", "at-hidden"}, fixture[:2], true},
		{"query_data binding", []string{"at-query"}, fixture[2:3], false},
		{"delegated write", []string{"at-delegate"}, fixture[4:5], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, ata := newBoundTestService(t, nil)
			ata.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, tc.ids).Return(tc.rows, nil)

			_, err := service.GetActionTypesByIDs(context.Background(), "kn-1", interfaces.MAIN_BRANCH, tc.ids)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("GetActionTypesByIDs() error = %v", err)
				}
				return
			}
			var httpErr *rest.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
				t.Fatalf("GetActionTypesByIDs() error = %v, want 403", err)
			}
		})
	}
}

func TestWithVisibleBoundObjectTypeIDs_NarrowsSearchCandidatesInOrder(t *testing.T) {
	service, ata := newBoundTestService(t, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(boundFixture(), nil)

	ids, err := service.withVisibleBoundObjectTypeIDs(context.Background(), "kn-1", "",
		[]string{"at-delegate", "at-hidden", "at-in"})

	if err != nil {
		t.Fatalf("withVisibleBoundObjectTypeIDs() error = %v", err)
	}
	if want := []string{"at-delegate", "at-in"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("withVisibleBoundObjectTypeIDs() = %v, want %v", ids, want)
	}
}
