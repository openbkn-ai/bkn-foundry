// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	interfacemock "bkn-backend/interfaces/mock"
)

func TestKNImportPermissionPrecheckedIsScopedToMarkedContext(t *testing.T) {
	ctx := context.Background()
	if KNImportPermissionPrechecked(ctx) {
		t.Fatal("plain context must not skip child authorization")
	}

	marked := WithKNImportPermissionPrechecked(ctx)
	if !KNImportPermissionPrechecked(marked) {
		t.Fatal("marked whole-KN import context must skip duplicate child authorization")
	}
	if KNImportPermissionPrechecked(ctx) {
		t.Fatal("marking a derived context must not mutate its parent")
	}
}

func TestValidateKNChildAuthorizationIDsRejectsAmbiguousIDs(t *testing.T) {
	if err := ValidateKNChildAuthorizationIDs(context.Background(), "kn-1", []string{"bad/id"}); err == nil {
		t.Fatal("canonical child authorization must reject ambiguous child IDs")
	}
}

type childCandidate struct {
	id string
}

func TestKNChildOperationCandidatesMatchResourceContract(t *testing.T) {
	wantChild := []string{
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
		interfaces.OPERATION_TYPE_QUERY_DATA,
		interfaces.OPERATION_TYPE_MODIFY,
		interfaces.OPERATION_TYPE_DELETE,
		interfaces.OPERATION_TYPE_AUTHORIZE,
	}
	if got := KNChildOperationCandidates(interfaces.RESOURCE_TYPE_RELATION_TYPE); !reflect.DeepEqual(got, wantChild) {
		t.Fatalf("relation type operations = %#v, want %#v", got, wantChild)
	}
	wantAction := append(append([]string{}, wantChild...), interfaces.OPERATION_TYPE_TASK_MANAGE, interfaces.OPERATION_TYPE_EXECUTE)
	if got := KNChildOperationCandidates(interfaces.RESOURCE_TYPE_ACTION_TYPE); !reflect.DeepEqual(got, wantAction) {
		t.Fatalf("action type operations = %#v, want %#v", got, wantAction)
	}
}

func TestFilterAndPaginateKNChildrenWithOperationsProjectsCanonicalOperations(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
		[]string{"kn-1/action-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		[]string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_QUERY_DATA,
			interfaces.OPERATION_TYPE_MODIFY,
			interfaces.OPERATION_TYPE_DELETE,
			interfaces.OPERATION_TYPE_AUTHORIZE,
			interfaces.OPERATION_TYPE_TASK_MANAGE,
			interfaces.OPERATION_TYPE_EXECUTE,
		}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/action-1": {ResourceID: "kn-1/action-1", Operations: []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_AUTHORIZE,
			interfaces.OPERATION_TYPE_EXECUTE,
		}},
	}, nil)

	items, total, operations, err := FilterAndPaginateKNChildrenWithOperations(context.Background(), ps,
		interfaces.RESOURCE_TYPE_ACTION_TYPE, "kn-1", []childCandidate{{id: "action-1"}},
		func(candidate childCandidate) string { return candidate.id }, 0, -1)
	if err != nil {
		t.Fatalf("FilterAndPaginateKNChildrenWithOperations() error = %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("items = %#v, total = %d", items, total)
	}
	if got := operations["kn-1/action-1"].Operations; !reflect.DeepEqual(got, []string{
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
		interfaces.OPERATION_TYPE_AUTHORIZE,
		interfaces.OPERATION_TYPE_EXECUTE,
	}) {
		t.Fatalf("operations = %#v", got)
	}
}

func TestGetKNChildOperationsUsesCanonicalDetailResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/metric-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		[]string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_QUERY_DATA,
			interfaces.OPERATION_TYPE_MODIFY,
			interfaces.OPERATION_TYPE_DELETE,
			interfaces.OPERATION_TYPE_AUTHORIZE,
		}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/metric-1": {ResourceID: "kn-1/metric-1", Operations: []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_QUERY_DATA,
		}},
	}, nil)

	operations, err := GetKNChildOperations(context.Background(), ps,
		interfaces.RESOURCE_TYPE_METRIC, "kn-1", "metric-1")
	if err != nil {
		t.Fatalf("GetKNChildOperations() error = %v", err)
	}
	if !reflect.DeepEqual(operations, []string{
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
		interfaces.OPERATION_TYPE_QUERY_DATA,
	}) {
		t.Fatalf("operations = %#v", operations)
	}
}

func TestFilterAndPaginateKNChildrenFiltersCanonicalIDsBeforePaging(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-2/one", "kn-2/two", "kn-2/three"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-2/one":   {ResourceID: "kn-2/one", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		"kn-2/three": {ResourceID: "kn-2/three", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)

	candidates := []childCandidate{{id: "one"}, {id: "two"}, {id: "three"}}
	got, total, err := FilterAndPaginateKNChildren(context.Background(), ps,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-2", candidates,
		func(candidate childCandidate) string { return candidate.id }, 1, 1)
	if err != nil {
		t.Fatalf("FilterAndPaginateKNChildren() error = %v", err)
	}
	if total != 2 || !reflect.DeepEqual(got, []childCandidate{{id: "three"}}) {
		t.Fatalf("result = %#v, total = %d", got, total)
	}
}

func TestFilterAndPaginateKNChildrenPropagatesFilterFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	wantErr := errors.New("bkn-safe unavailable")
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
		[]string{"kn-1/risk-1"}, gomock.Any(), true, gomock.Any()).Return(nil, wantErr)

	got, total, err := FilterAndPaginateKNChildren(context.Background(), ps,
		interfaces.RESOURCE_TYPE_RISK_TYPE, "kn-1", []childCandidate{{id: "risk-1"}},
		func(candidate childCandidate) string { return candidate.id }, 0, 10)
	if !errors.Is(err, wantErr) || got != nil || total != 0 {
		t.Fatalf("result = %#v, total = %d, error = %v", got, total, err)
	}
}

func TestFilterAndPaginateKNChildrenSkipsHistoricalInvalidIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/valid"}, gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/valid": {ResourceID: "kn-1/valid"},
	}, nil)

	got, total, err := FilterAndPaginateKNChildren(context.Background(), ps,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1",
		[]childCandidate{{id: "legacy/id"}, {id: "valid"}},
		func(candidate childCandidate) string { return candidate.id }, 0, -1)
	if err != nil || total != 1 || !reflect.DeepEqual(got, []childCandidate{{id: "valid"}}) {
		t.Fatalf("result = %#v, total = %d, error = %v", got, total, err)
	}
}

func TestFilterAndPaginateKNChildrenSupportsEveryChildResourceType(t *testing.T) {
	resourceTypes := []string{
		interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.RESOURCE_TYPE_METRIC,
		interfaces.RESOURCE_TYPE_RISK_TYPE,
	}
	for _, resourceType := range resourceTypes {
		t.Run(resourceType, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps := interfacemock.NewMockPermissionService(ctrl)
			ps.EXPECT().FilterResources(gomock.Any(), resourceType, []string{"kn-1/child-1"},
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
				"kn-1/child-1": {ResourceID: "kn-1/child-1"},
			}, nil)

			got, total, err := FilterAndPaginateKNChildren(context.Background(), ps,
				resourceType, "kn-1", []childCandidate{{id: "child-1"}},
				func(candidate childCandidate) string { return candidate.id }, 0, -1)
			if err != nil || total != 1 || len(got) != 1 {
				t.Fatalf("result = %#v, total = %d, error = %v", got, total, err)
			}
		})
	}
}

func TestFilterAndPaginateKNChildrenMergesRuntimeConfiguredBlocks(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_FILTER_CHUNK_SIZE", "2")
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	gomock.InOrder(
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
			[]string{"kn-1/one", "kn-1/two"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"kn-1/one": {ResourceID: "kn-1/one"},
			}, nil),
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
			[]string{"kn-1/three"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"kn-1/three": {ResourceID: "kn-1/three"},
			}, nil),
	)

	got, total, err := FilterAndPaginateKNChildren(context.Background(), ps,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1",
		[]childCandidate{{id: "one"}, {id: "two"}, {id: "three"}},
		func(candidate childCandidate) string { return candidate.id }, 0, -1)
	if err != nil || total != 2 || !reflect.DeepEqual(got,
		[]childCandidate{{id: "one"}, {id: "three"}}) {
		t.Fatalf("result = %#v, total = %d, error = %v", got, total, err)
	}
}

func TestFilterAndPaginateKNChildrenDiscardsEarlierBlocksOnLaterFailure(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_FILTER_CHUNK_SIZE", "1")
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	wantErr := errors.New("second block timed out")
	gomock.InOrder(
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
			[]string{"kn-1/one"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"kn-1/one": {ResourceID: "kn-1/one"},
			}, nil),
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
			[]string{"kn-1/two"}, gomock.Any(), true, gomock.Any()).Return(nil, wantErr),
	)

	got, total, err := FilterAndPaginateKNChildren(context.Background(), ps,
		interfaces.RESOURCE_TYPE_METRIC, "kn-1",
		[]childCandidate{{id: "one"}, {id: "two"}},
		func(candidate childCandidate) string { return candidate.id }, 0, -1)
	if !errors.Is(err, wantErr) || got != nil || total != 0 {
		t.Fatalf("result = %#v, total = %d, error = %v", got, total, err)
	}
}

func TestFilterKNChildIDsKeepsEqualChildIDsIsolatedByKN(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	gomock.InOrder(
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
			[]string{"kn-1/shared"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"kn-1/shared": {ResourceID: "kn-1/shared"},
			}, nil),
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
			[]string{"kn-2/shared"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil),
	)

	first, err := FilterKNChildIDs(context.Background(), ps,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1", []string{"shared"},
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil || !reflect.DeepEqual(first, []string{"shared"}) {
		t.Fatalf("first KN result = %#v, error = %v", first, err)
	}
	second, err := FilterKNChildIDs(context.Background(), ps,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-2", []string{"shared"},
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil || len(second) != 0 {
		t.Fatalf("second KN result = %#v, error = %v", second, err)
	}
}

func TestFilterKNChildIDsSkipsHistoricalInvalidIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/valid"}, gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/valid": {ResourceID: "kn-1/valid"},
	}, nil)

	got, err := FilterKNChildIDs(context.Background(), ps,
		interfaces.RESOURCE_TYPE_METRIC, "kn-1", []string{"bad*id", "valid", " spaced "},
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil || !reflect.DeepEqual(got, []string{"valid"}) {
		t.Fatalf("result = %#v, error = %v", got, err)
	}
}

func TestCheckKNChildBatchPermissionRequiresEveryCanonicalChild(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/one", "kn-1/two"}, []string{interfaces.OPERATION_TYPE_DELETE}, true,
		[]string{interfaces.OPERATION_TYPE_DELETE}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/one": {ResourceID: "kn-1/one", Operations: []string{interfaces.OPERATION_TYPE_DELETE}},
	}, nil)

	err := CheckKNChildBatchPermission(context.Background(), ps,
		interfaces.RESOURCE_TYPE_METRIC, "kn-1", []string{"one", "two"},
		interfaces.OPERATION_TYPE_DELETE)
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("CheckKNChildBatchPermission() error = %v, want HTTP 403", err)
	}
}

func TestCheckKNChildBatchPermissionPropagatesFilterFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := interfacemock.NewMockPermissionService(ctrl)
	wantErr := errors.New("bkn-safe unavailable")
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
		[]string{"kn-1/one", "kn-1/two"}, []string{interfaces.OPERATION_TYPE_DELETE}, true,
		[]string{interfaces.OPERATION_TYPE_DELETE}).Return(nil, wantErr)

	err := CheckKNChildBatchPermission(context.Background(), ps,
		interfaces.RESOURCE_TYPE_RISK_TYPE, "kn-1", []string{"one", "two"},
		interfaces.OPERATION_TYPE_DELETE)
	if !errors.Is(err, wantErr) {
		t.Fatalf("CheckKNChildBatchPermission() error = %v, want %v", err, wantErr)
	}
}
