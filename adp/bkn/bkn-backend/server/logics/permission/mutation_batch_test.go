// Copyright openbkn.ai

package permission

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestPermissionMutationsUseBoundedBatches(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockPermissionAccess(ctrl)
	service := &PermissionServiceImpl{pa: access}
	count := safeMutationBatchSize + 1
	resources := make([]interfaces.PermissionResource, count)
	parents := make([]interfaces.PermissionResourceParent, count)
	ids := make([]string, count)
	for index := range count {
		id := string(rune(index + 1))
		resources[index] = interfaces.PermissionResource{Type: "object_type", ID: id}
		parents[index] = interfaces.PermissionResourceParent{ResourceID: id, ParentID: "kn-1"}
		ids[index] = id
	}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})

	access.EXPECT().CreateResources(gomock.Any(), gomock.Len(safeMutationBatchSize)).Return(nil)
	access.EXPECT().CreateResources(gomock.Any(), gomock.Len(1)).Return(nil)
	if err := service.CreateResources(ctx, resources, []string{"view"}); err != nil {
		t.Fatal(err)
	}

	access.EXPECT().UpsertResourceParents(gomock.Any(), "object_type", "knowledge_network",
		gomock.Len(safeMutationBatchSize)).Return(nil)
	access.EXPECT().UpsertResourceParents(gomock.Any(), "object_type", "knowledge_network", gomock.Len(1)).Return(nil)
	if err := service.UpsertResourceParents(ctx, "object_type", "knowledge_network", parents); err != nil {
		t.Fatal(err)
	}

	access.EXPECT().DeleteResources(gomock.Any(), gomock.Len(safeMutationBatchSize)).Return(nil)
	access.EXPECT().DeleteResources(gomock.Any(), gomock.Len(1)).Return(nil)
	if err := service.DeleteResources(ctx, "object_type", ids); err != nil {
		t.Fatal(err)
	}

	access.EXPECT().DeleteResourceParents(gomock.Any(), "object_type", gomock.Len(safeMutationBatchSize)).Return(nil)
	access.EXPECT().DeleteResourceParents(gomock.Any(), "object_type", gomock.Len(1)).Return(nil)
	if err := service.DeleteResourceParents(ctx, "object_type", ids); err != nil {
		t.Fatal(err)
	}
}

func TestParentMutationTracksAttemptedBatchBeforeFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockPermissionAccess(ctrl)
	service := &PermissionServiceImpl{pa: access}
	ctx, tracker, owner := WithResourceParentTracker(context.Background())
	if !owner {
		t.Fatal("expected tracker owner")
	}
	items := []interfaces.PermissionResourceParent{{ResourceID: "ot-1", ParentID: "kn-1"}}
	access.EXPECT().UpsertResourceParents(gomock.Any(), "object_type", "knowledge_network", items).
		Return(errors.New("ambiguous failure"))
	if err := service.UpsertResourceParents(ctx, "object_type", "knowledge_network", items); err == nil {
		t.Fatal("expected error")
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.entries) != 1 {
		t.Fatalf("tracked entries = %d, want 1", len(tracker.entries))
	}
}
