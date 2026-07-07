// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestUpdateResourceIndexNameEmptyOldIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	resource := &interfaces.Resource{ID: "r1"}

	ra.EXPECT().Update(gomock.Any(), resource).DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
		if got.LocalIndexName != "new-index" {
			t.Fatalf("expected new-index, got %q", got.LocalIndexName)
		}
		return nil
	})

	if err := updateResourceIndexName(context.Background(), resource, ra, "new-index"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateResourceIndexNameUnchangedIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	resource := &interfaces.Resource{ID: "r1", LocalIndexName: "same-index"}

	if err := updateResourceIndexName(context.Background(), resource, ra, "same-index"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateResourceIndexNameUpdateFailureKeepsOldIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

	ra.EXPECT().Update(gomock.Any(), resource).DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
		if got.LocalIndexName != "new-index" {
			t.Fatalf("expected attempted update to new-index, got %q", got.LocalIndexName)
		}
		return errors.New("update failed")
	})

	err := updateResourceIndexName(context.Background(), resource, ra, "new-index")
	if err == nil {
		t.Fatal("expected update error")
	}
	if resource.LocalIndexName != "old-index" {
		t.Fatalf("expected in-memory LocalIndexName restored to old-index, got %q", resource.LocalIndexName)
	}
}
