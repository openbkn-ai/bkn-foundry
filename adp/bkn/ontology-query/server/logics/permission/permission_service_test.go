// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

func TestPermissionServiceRequireQueryData(t *testing.T) {
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID: "account-1", Type: "user",
	})
	resources := []interfaces.PermissionResource{
		interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeMetric, "kn-a", "m-1"),
		interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeObjectType, "kn-a", "ot-1"),
	}

	t.Run("requires every dependency and deduplicates the request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := omock.NewMockPermissionAccess(ctrl)
		access.EXPECT().FilterResources(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request interfaces.PermissionFilterRequest) (interfaces.PermissionFilterResponse, error) {
				if request.AccessorID != "account-1" || len(request.Resources) != 2 {
					t.Fatalf("request = %#v", request)
				}
				return interfaces.PermissionFilterResponse{Resources: []interfaces.PermissionFilterResult{
					{ResourceType: "metric", ResourceID: "kn-a/m-1", Operations: []string{"query_data"}},
					{ResourceType: "object_type", ResourceID: "kn-a/ot-1", Operations: []string{"query_data"}},
				}}, nil
			})

		service := &permissionService{access: access}
		if err := service.RequireQueryData(ctx, append(resources, resources[0])); err != nil {
			t.Fatalf("RequireQueryData() error = %v", err)
		}
	})

	t.Run("denies when bkn-safe omits one dependency", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := omock.NewMockPermissionAccess(ctrl)
		access.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(interfaces.PermissionFilterResponse{
			Resources: []interfaces.PermissionFilterResult{
				{ResourceType: "metric", ResourceID: "kn-a/m-1", Operations: []string{"query_data"}},
			},
		}, nil)

		err := (&permissionService{access: access}).RequireQueryData(ctx, resources)
		assertHTTPStatus(t, err, http.StatusForbidden)
	})

	t.Run("fails closed when bkn-safe is unavailable", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := omock.NewMockPermissionAccess(ctrl)
		access.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(
			interfaces.PermissionFilterResponse{}, errors.New("timeout"))

		err := (&permissionService{access: access}).RequireQueryData(ctx, resources)
		assertHTTPStatus(t, err, http.StatusServiceUnavailable)
	})

	t.Run("denies an internal request without a subject", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := omock.NewMockPermissionAccess(ctrl)
		err := (&permissionService{access: access}).RequireQueryData(context.Background(), resources)
		assertHTTPStatus(t, err, http.StatusForbidden)
	})

	t.Run("denies an internal request with an unsupported subject type", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := omock.NewMockPermissionAccess(ctrl)
		invalidCtx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
			ID: "account-1", Type: "anonymous",
		})
		err := (&permissionService{access: access}).RequireQueryData(invalidCtx, resources)
		assertHTTPStatus(t, err, http.StatusForbidden)
	})
}

func TestPermissionServiceFilterQueryDataReturnsOnlyAllowedCandidatesInRequestOrder(t *testing.T) {
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID: "account-1", Type: "user",
	})
	ctrl := gomock.NewController(t)
	access := omock.NewMockPermissionAccess(ctrl)
	access.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(interfaces.PermissionFilterResponse{
		Resources: []interfaces.PermissionFilterResult{
			{ResourceType: "relation_type", ResourceID: "kn-a/rt-2", Operations: []string{"query_data"}},
			{ResourceType: "relation_type", ResourceID: "kn-a/rt-1", Operations: []string{}},
		},
	}, nil)

	allowed, err := (&permissionService{access: access}).FilterQueryData(ctx, []interfaces.PermissionResource{
		{Type: "relation_type", ID: "kn-a/rt-1"},
		{Type: "relation_type", ID: "kn-a/rt-2"},
	})
	if err != nil {
		t.Fatalf("FilterQueryData() error = %v", err)
	}
	if len(allowed) != 1 || allowed[0].ID != "kn-a/rt-2" {
		t.Fatalf("FilterQueryData() = %#v", allowed)
	}
}

func assertHTTPStatus(t *testing.T, err error, expected int) {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if httpErr.HTTPCode != expected {
		t.Fatalf("HTTP status = %d, want %d", httpErr.HTTPCode, expected)
	}
}
