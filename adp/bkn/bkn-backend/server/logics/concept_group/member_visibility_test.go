// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package concept_group

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// memberPermissions answers every concept group, and object, relation and action types the way
// the member fixtures describe: nothing on ot-hidden, query_data alone on ot-query, and two
// relation and action types the caller may not read themselves.
func memberPermissions(objectTypeErr error) func(context.Context, string, []string, []string, bool,
	[]string) (map[string]interfaces.PermissionResourceOps, error) {
	denied := map[string]struct{}{
		"kn-1/ot-hidden":     {},
		"kn-1/rt-unreadable": {},
		"kn-1/at-unreadable": {},
	}
	return func(_ context.Context, resourceType string, ids, _ []string, _ bool,
		_ []string) (map[string]interfaces.PermissionResourceOps, error) {
		if resourceType == interfaces.RESOURCE_TYPE_OBJECT_TYPE && objectTypeErr != nil {
			return nil, objectTypeErr
		}
		matched := map[string]interfaces.PermissionResourceOps{}
		for _, id := range ids {
			if _, ok := denied[id]; ok {
				continue
			}
			ops := []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}
			if id == "kn-1/ot-query" {
				ops = []string{interfaces.OPERATION_TYPE_QUERY_DATA}
			}
			matched[id] = interfaces.PermissionResourceOps{ResourceID: id, Operations: ops}
		}
		return matched, nil
	}
}

type memberTestMocks struct {
	cga *bmock.MockConceptGroupAccess
	rta *bmock.MockRelationTypeAccess
	ata *bmock.MockActionTypeAccess
}

func newMemberTestService(t *testing.T, objectTypeErr error) (*conceptGroupService, memberTestMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mocks := memberTestMocks{
		cga: bmock.NewMockConceptGroupAccess(ctrl),
		rta: bmock.NewMockRelationTypeAccess(ctrl),
		ata: bmock.NewMockActionTypeAccess(ctrl),
	}
	ps := bmock.NewMockPermissionService(ctrl)
	ums := bmock.NewMockUserMgmtService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(memberPermissions(objectTypeErr)).AnyTimes()
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &conceptGroupService{cga: mocks.cga, rta: mocks.rta, ata: mocks.ata, ps: ps, ums: ums}, mocks
}

func memberRelation(id, source, target string) *interfaces.RelationType {
	return &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: id, SourceObjectTypeID: source, TargetObjectTypeID: target}}
}

func memberAction(id, bound string) *interfaces.ActionType {
	return &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: id, ObjectTypeID: bound}}
}

func listGroups(t *testing.T, service *conceptGroupService) []*interfaces.ConceptGroup {
	t.Helper()
	groups, _, err := service.ListConceptGroups(context.Background(), interfaces.ConceptGroupsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})
	if err != nil {
		t.Fatalf("ListConceptGroups() error = %v", err)
	}
	return groups
}

// TestListConceptGroups_MembersAndStatisticsCountOnlyWhatTheCallerSees pins #1532 for concept
// groups: a group the caller may read must not list, or count, an object type the caller holds
// nothing on, nor count relation or action types the caller may not read.
func TestListConceptGroups_MembersAndStatisticsCountOnlyWhatTheCallerSees(t *testing.T) {
	service, m := newMemberTestService(t, nil)
	m.cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return([]*interfaces.ConceptGroup{
		{CGID: "cg-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}, nil)
	m.cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"cg-1"},
		interfaces.MODULE_TYPE_OBJECT_TYPE).Return([]string{"ot-a", "ot-hidden", "ot-query"}, nil)
	m.rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, error) {
			if want := []string{"ot-a", "ot-query"}; !reflect.DeepEqual(query.SourceObjectTypeIDs, want) ||
				!reflect.DeepEqual(query.TargetObjectTypeIDs, want) {
				t.Errorf("relation types read for ends %v -> %v, want both %v",
					query.SourceObjectTypeIDs, query.TargetObjectTypeIDs, want)
			}
			return []*interfaces.RelationType{
				memberRelation("rt-in", "ot-a", "ot-query"),
				memberRelation("rt-unreadable", "ot-query", "ot-a"),
			}, nil
		})
	m.ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return([]*interfaces.ActionType{
		memberAction("at-a", "ot-a"),
		memberAction("at-unreadable", "ot-query"),
	}, nil)

	groups := listGroups(t, service)

	if got, want := groups[0].ObjectTypeIDs, []string{"ot-a", "ot-query"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("object_type_ids = %v, want %v", got, want)
	}
	if got, want := *groups[0].Statistics, (interfaces.Statistics{OtTotal: 2, RtTotal: 1, AtTotal: 1}); got != want {
		t.Fatalf("statistics = %+v, want %+v", got, want)
	}
}

// TestListConceptGroups_ResolvesMembersOnceForEveryGroup keeps the authorization cost of a group
// list independent of how many groups are on the page.
func TestListConceptGroups_ResolvesMembersOnceForEveryGroup(t *testing.T) {
	service, m := newMemberTestService(t, nil)
	m.cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return([]*interfaces.ConceptGroup{
		{CGID: "cg-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
		{CGID: "cg-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}, nil)
	m.cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"cg-1"},
		interfaces.MODULE_TYPE_OBJECT_TYPE).Return([]string{"ot-a", "ot-hidden"}, nil)
	m.cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"cg-2"},
		interfaces.MODULE_TYPE_OBJECT_TYPE).Return([]string{"ot-query", "ot-a"}, nil)
	m.rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return([]*interfaces.RelationType{
		memberRelation("rt-in", "ot-a", "ot-query"),
	}, nil).Times(1)
	m.ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return([]*interfaces.ActionType{
		memberAction("at-a", "ot-a"),
	}, nil).Times(1)

	groups := listGroups(t, service)

	if got, want := groups[0].ObjectTypeIDs, []string{"ot-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cg-1 object_type_ids = %v, want %v", got, want)
	}
	if got, want := *groups[0].Statistics, (interfaces.Statistics{OtTotal: 1, RtTotal: 0, AtTotal: 1}); got != want {
		t.Fatalf("cg-1 statistics = %+v, want %+v", got, want)
	}
	if got, want := groups[1].ObjectTypeIDs, []string{"ot-query", "ot-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cg-2 object_type_ids = %v, want %v", got, want)
	}
	if got, want := *groups[1].Statistics, (interfaces.Statistics{OtTotal: 2, RtTotal: 1, AtTotal: 1}); got != want {
		t.Fatalf("cg-2 statistics = %+v, want %+v", got, want)
	}
}

// TestListConceptGroups_MemberAuthorizationFailureIsAnError refuses to read an authorization
// outage as an empty group.
func TestListConceptGroups_MemberAuthorizationFailureIsAnError(t *testing.T) {
	unavailable := errors.New("authorization unavailable")
	service, m := newMemberTestService(t, unavailable)
	m.cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return([]*interfaces.ConceptGroup{
		{CGID: "cg-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}, nil)
	m.cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]string{"ot-a"}, nil)

	_, _, err := service.ListConceptGroups(context.Background(), interfaces.ConceptGroupsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn-1",
		Branch:                    interfaces.MAIN_BRANCH,
	})

	if !errors.Is(err, unavailable) {
		t.Fatalf("ListConceptGroups() error = %v, want %v", err, unavailable)
	}
}
