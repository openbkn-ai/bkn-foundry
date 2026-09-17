// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

type navigationFixture struct {
	service *knowledgeNetworkService
	kna     *bmock.MockKNAccess
	rta     *bmock.MockRelationTypeAccess
	ata     *bmock.MockActionTypeAccess
}

// newNavigationFixture grants no network-level access, every relation and action type candidate,
// and the object types in visibleObjectTypes -- with any single operation, query_data included.
// objectTypeErr, when set, fails the reference check (the second object type check).
func newNavigationFixture(t *testing.T, visibleObjectTypes []string, objectTypeErr error) navigationFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := navigationFixture{
		kna: bmock.NewMockKNAccess(ctrl),
		rta: bmock.NewMockRelationTypeAccess(ctrl),
		ata: bmock.NewMockActionTypeAccess(ctrl),
	}
	ps := bmock.NewMockPermissionService(ctrl)
	visible := map[string]struct{}{}
	for _, id := range visibleObjectTypes {
		visible[interfaces.KNChildResourceID("kn1", id)] = struct{}{}
	}
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, resourceType string, ids, _ []string, _ bool) (map[string]interfaces.PermissionResourceOps, error) {
			matched := map[string]interfaces.PermissionResourceOps{}
			switch resourceType {
			case interfaces.RESOURCE_TYPE_KN:
				return matched, nil
			case interfaces.RESOURCE_TYPE_OBJECT_TYPE:
				if objectTypeErr != nil {
					return nil, objectTypeErr
				}
				for _, id := range ids {
					if _, ok := visible[id]; ok {
						matched[id] = interfaces.PermissionResourceOps{ResourceID: id,
							Operations: []string{interfaces.OPERATION_TYPE_QUERY_DATA}}
					}
				}
			default:
				for _, id := range ids {
					matched[id] = interfaces.PermissionResourceOps{ResourceID: id,
						Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}}
				}
			}
			return matched, nil
		}).AnyTimes()
	f.service = &knowledgeNetworkService{kna: f.kna, ps: ps, rta: f.rta, ata: f.ata}
	return f
}

func (f navigationFixture) expectNetwork(candidates ...interfaces.KNChildResourceCandidate) {
	f.kna.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).
		Return(&interfaces.KN{KNID: "kn1", KNName: "Network 1", Branch: interfaces.MAIN_BRANCH}, nil)
	f.kna.EXPECT().ListKNChildResourceCandidates(gomock.Any(), []string{"kn1"}, interfaces.MAIN_BRANCH).
		Return(candidates, nil)
}

func child(resourceType, id string) interfaces.KNChildResourceCandidate {
	return interfaces.KNChildResourceCandidate{KNID: "kn1", ResourceID: id, Type: resourceType}
}

func navRelation(id, source, target string) *interfaces.RelationType {
	return &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: id, SourceObjectTypeID: source, TargetObjectTypeID: target}}
}

// TestNavigation_ARelationTypeWithAHiddenEndpointDoesNotOpenTheNetwork pins #1532 at the network
// level: a relation type the caller cannot read -- one end is an object type they hold nothing on
// -- must not expose the network's shell either.
func TestNavigation_ARelationTypeWithAHiddenEndpointDoesNotOpenTheNetwork(t *testing.T) {
	f := newNavigationFixture(t, []string{"ot-a"}, nil)
	f.expectNetwork(child(interfaces.RESOURCE_TYPE_RELATION_TYPE, "rt-out"))
	f.rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"rt-out"}).
		Return([]*interfaces.RelationType{navRelation("rt-out", "ot-a", "ot-hidden")}, nil)

	_, err := f.service.GetKNByID(context.Background(), "kn1", interfaces.MAIN_BRANCH, "")

	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("GetKNByID() error = %v, want 403", err)
	}
}

// TestNavigation_StatisticsKeepDirectlyReadableActions keeps action type navigation independent
// from the permissions on the action's bound object type, while relation types still require both
// endpoints to be visible.
func TestNavigation_StatisticsKeepDirectlyReadableActions(t *testing.T) {
	f := newNavigationFixture(t, []string{"ot-a", "ot-query"}, nil)
	f.expectNetwork(
		child(interfaces.RESOURCE_TYPE_OBJECT_TYPE, "ot-a"),
		child(interfaces.RESOURCE_TYPE_RELATION_TYPE, "rt-in"),
		child(interfaces.RESOURCE_TYPE_RELATION_TYPE, "rt-out"),
		child(interfaces.RESOURCE_TYPE_RELATION_TYPE, "rt-gone"),
		child(interfaces.RESOURCE_TYPE_ACTION_TYPE, "at-in"),
		child(interfaces.RESOURCE_TYPE_ACTION_TYPE, "at-hidden"),
	)
	f.rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, gomock.Any()).
		Return([]*interfaces.RelationType{
			navRelation("rt-in", "ot-a", "ot-query"),
			navRelation("rt-out", "ot-a", "ot-hidden"),
			// rt-gone has no definition any more, so its references cannot be checked.
		}, nil)
	kn, err := f.service.GetKNByID(context.Background(), "kn1", interfaces.MAIN_BRANCH, "")
	if err != nil {
		t.Fatalf("GetKNByID() error = %v", err)
	}
	if !kn.NavigationOnly {
		t.Fatal("GetKNByID() must return a navigation shell")
	}
	stats, err := f.service.GetStatByKN(context.Background(), kn)
	if err != nil {
		t.Fatalf("GetStatByKN() error = %v", err)
	}
	if stats.OtTotal != 1 || stats.RtTotal != 1 || stats.AtTotal != 2 {
		t.Fatalf("navigation statistics = ot %d, rt %d, at %d; want 1, 1, 2", stats.OtTotal, stats.RtTotal, stats.AtTotal)
	}
}

// TestNavigation_ReferenceAuthorizationFailureIsAnError refuses to read an authorization outage
// as "no child is readable".
func TestNavigation_ReferenceAuthorizationFailureIsAnError(t *testing.T) {
	unavailable := errors.New("authorization unavailable")
	f := newNavigationFixture(t, nil, unavailable)
	f.expectNetwork(child(interfaces.RESOURCE_TYPE_RELATION_TYPE, "rt-in"))
	f.rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"rt-in"}).
		Return([]*interfaces.RelationType{navRelation("rt-in", "ot-a", "ot-query")}, nil)

	if _, err := f.service.GetKNByID(context.Background(), "kn1", interfaces.MAIN_BRANCH, ""); !errors.Is(err, unavailable) {
		t.Fatalf("GetKNByID() error = %v, want %v", err, unavailable)
	}
}

// TestNavigation_ReferencesAcrossNetworksCostOneAuthorizationCall keeps the network list's
// authorization cost independent of how many restricted networks it covers: the object types
// referenced by visible relation and action types are checked in one call for all of them.
func TestNavigation_ReferencesAcrossNetworksCostOneAuthorizationCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	service := &knowledgeNetworkService{kna: kna, ps: ps, rta: rta}

	parameter := interfaces.KNsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		Branch:                    interfaces.MAIN_BRANCH,
	}
	kna.EXPECT().ListKNs(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, query interfaces.KNsQueryParams) ([]*interfaces.KN, error) {
			if query.OnlyIDs {
				return []*interfaces.KN{{KNID: "kn1"}, {KNID: "kn2"}}, nil
			}
			kns := make([]*interfaces.KN, 0, len(query.CandidateIDs))
			for _, id := range query.CandidateIDs {
				kns = append(kns, &interfaces.KN{KNID: id, Branch: interfaces.MAIN_BRANCH})
			}
			return kns, nil
		}).Times(2)
	kna.EXPECT().ListKNChildResourceCandidates(gomock.Any(), []string{"kn1", "kn2"}, interfaces.MAIN_BRANCH).
		Return([]interfaces.KNChildResourceCandidate{
			{KNID: "kn1", ResourceID: "rt-1", Type: interfaces.RESOURCE_TYPE_RELATION_TYPE},
			{KNID: "kn2", ResourceID: "rt-2", Type: interfaces.RESOURCE_TYPE_RELATION_TYPE},
		}, nil)
	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"rt-1"}).
		Return([]*interfaces.RelationType{navRelation("rt-1", "ot-a", "ot-b")}, nil)
	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, []string{"rt-2"}).
		Return([]*interfaces.RelationType{navRelation("rt-2", "ot-c", "ot-hidden")}, nil)

	objectTypeCalls := 0
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, resourceType string, ids, _ []string, _ bool) (map[string]interfaces.PermissionResourceOps, error) {
			matched := map[string]interfaces.PermissionResourceOps{}
			switch resourceType {
			case interfaces.RESOURCE_TYPE_KN:
				return matched, nil
			case interfaces.RESOURCE_TYPE_OBJECT_TYPE:
				objectTypeCalls++
			}
			for _, id := range ids {
				if id == interfaces.KNChildResourceID("kn2", "ot-hidden") {
					continue
				}
				matched[id] = interfaces.PermissionResourceOps{ResourceID: id,
					Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}}
			}
			return matched, nil
		}).AnyTimes()

	kns, total, err := service.ListKNs(context.Background(), parameter)

	if err != nil {
		t.Fatalf("ListKNs() error = %v", err)
	}
	if total != 1 || len(kns) != 1 || kns[0].KNID != "kn1" {
		t.Fatalf("ListKNs() = %d networks (total %d), want only kn1: kn2's only child points at a hidden object type", len(kns), total)
	}
	if objectTypeCalls != 1 {
		t.Fatalf("object type authorization calls = %d, want 1 for both networks", objectTypeCalls)
	}
}
