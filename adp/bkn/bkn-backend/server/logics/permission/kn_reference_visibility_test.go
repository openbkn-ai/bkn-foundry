// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestVisibleReferencedObjectTypes_AnyEffectiveOperationMakesAReferenceVisible(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/ot-a", "kn-1/ot-query", "kn-1/ot-hidden"}, []string(nil)).
		Return(map[string]interfaces.PermissionResourceOps{
			"kn-1/ot-a":      {ResourceID: "kn-1/ot-a", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			"kn-1/ot-query":  {ResourceID: "kn-1/ot-query", Operations: []string{interfaces.OPERATION_TYPE_QUERY_DATA}},
			"kn-1/ot-hidden": {ResourceID: "kn-1/ot-hidden"},
		}, nil)

	visible, err := VisibleReferencedObjectTypes(context.Background(), ps, "kn-1",
		[]string{"ot-a", "", "ot-query", "ot-a", "ot-hidden", "bad/id"})

	if err != nil {
		t.Fatalf("VisibleReferencedObjectTypes() error = %v", err)
	}
	want := map[string]struct{}{"ot-a": {}, "ot-query": {}}
	if !reflect.DeepEqual(visible, want) {
		t.Fatalf("VisibleReferencedObjectTypes() = %v, want %v", visible, want)
	}
	if !ReferencesVisible(visible, "ot-a", "") || ReferencesVisible(visible, "ot-a", "ot-hidden") ||
		ReferencesVisible(visible, "bad/id") {
		t.Fatal("ReferencesVisible() must accept empty ids and refuse ids outside the visible set")
	}
}

func TestVisibleReferencedObjectTypes_NoReferencesAskNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)

	visible, err := VisibleReferencedObjectTypes(context.Background(), ps, "kn-1", []string{"", ""})

	if err != nil || len(visible) != 0 {
		t.Fatalf("VisibleReferencedObjectTypes() = %v, %v; want empty without consulting permissions", visible, err)
	}
}

func TestVisibleReferencedObjectTypes_AuthorizationFailureIsReturned(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	unavailable := errors.New("authorization unavailable")
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, unavailable)

	if _, err := VisibleReferencedObjectTypes(context.Background(), ps, "kn-1", []string{"ot-a"}); !errors.Is(err, unavailable) {
		t.Fatalf("VisibleReferencedObjectTypes() error = %v, want %v", err, unavailable)
	}
}

func TestVisibleReferencedObjectTypesByKN_OneCallForEveryNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/ot-a", "kn-1/ot-b", "kn-2/ot-a"}, []string(nil)).
		Return(map[string]interfaces.PermissionResourceOps{
			"kn-1/ot-a": {ResourceID: "kn-1/ot-a", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			"kn-2/ot-a": {ResourceID: "kn-2/ot-a", Operations: []string{interfaces.OPERATION_TYPE_QUERY_DATA}},
		}, nil).Times(1)

	visible, err := VisibleReferencedObjectTypesByKN(context.Background(), ps, map[string][]string{
		"kn-2": {"ot-a", "ot-a"},
		"kn-1": {"ot-a", "ot-b", ""},
		"kn-3": {""},
	})

	if err != nil {
		t.Fatalf("VisibleReferencedObjectTypesByKN() error = %v", err)
	}
	want := map[string]map[string]struct{}{
		"kn-1": {"ot-a": {}},
		"kn-2": {"ot-a": {}},
		"kn-3": {},
	}
	if !reflect.DeepEqual(visible, want) {
		t.Fatalf("VisibleReferencedObjectTypesByKN() = %v, want %v", visible, want)
	}
}
