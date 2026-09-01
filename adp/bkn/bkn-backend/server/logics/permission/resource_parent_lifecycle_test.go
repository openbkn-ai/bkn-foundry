// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestPrepareKNChildResourceID(t *testing.T) {
	id, err := PrepareKNChildResourceID(context.Background(), "", interfaces.ImportMode_Normal)
	if err != nil {
		t.Fatalf("PrepareKNChildResourceID() error = %v", err)
	}
	if len(id) != 20 {
		t.Fatalf("generated ID length = %d, want 20", len(id))
	}
	if _, err := PrepareKNChildResourceID(context.Background(), "bad/id", interfaces.ImportMode_Overwrite); err == nil {
		t.Fatal("ID containing a slash must be rejected")
	}
	if _, err := PrepareKNChildResourceID(context.Background(), "bad*id", interfaces.ImportMode_Overwrite); err == nil {
		t.Fatal("ID containing a wildcard must be rejected")
	}
	if _, err := PrepareKNChildResourceID(context.Background(), " child-id", interfaces.ImportMode_Overwrite); err == nil {
		t.Fatal("ID containing surrounding whitespace must be rejected")
	}
}

func TestResourceParentTrackerCompensatesNestedWrites(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, tracker, owner := WithResourceParentTracker(context.Background())
	if !owner {
		t.Fatal("new tracker must have an owner")
	}
	childCtx, childTracker, childOwner := WithResourceParentTracker(ctx)
	if childOwner || childTracker != tracker {
		t.Fatal("nested calls must share the outer transaction tracker")
	}
	TrackResourceParents(childCtx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, interfaces.RESOURCE_TYPE_KN,
		[]interfaces.PermissionResourceParent{{ResourceID: "kn-1/ot-1", ParentID: "kn-1"}})
	ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/ot-1"}).Return(nil)
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

func TestCleanupKNChildAuthorizationAttemptsPolicyAndParent(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	policyErr := errors.New("policy cleanup failed")
	resourceIDs := []string{"kn-1/ot-1"}
	ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, resourceIDs).Return(policyErr)
	ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, resourceIDs).Return(nil)
	if err := CleanupKNChildAuthorization(context.Background(), ps, interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		"kn-1", []string{"ot-1"}); !errors.Is(err, policyErr) {
		t.Fatalf("CleanupKNChildAuthorization() error = %v, want %v", err, policyErr)
	}
}

func TestAuthorizationCleanupTrackerBatchesAndDeduplicatesResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, tracker, owner := WithAuthorizationCleanupTracker(context.Background())
	if !owner {
		t.Fatal("new cleanup tracker must have an owner")
	}
	TrackKNChildAuthorizationCleanup(ctx, interfaces.RESOURCE_TYPE_METRIC, "kn-1", []string{"metric-1"})
	TrackKNChildAuthorizationCleanup(ctx, interfaces.RESOURCE_TYPE_METRIC, "kn-1", []string{"metric-1", "metric-2"})
	resourceIDs := []string{"kn-1/metric-1", "kn-1/metric-2"}
	ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC, resourceIDs).Return(nil)
	ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC, resourceIDs).Return(nil)
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}
