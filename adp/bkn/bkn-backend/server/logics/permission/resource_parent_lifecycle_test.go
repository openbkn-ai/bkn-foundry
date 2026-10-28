// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestPrepareKNChildResourceID(t *testing.T) {
	requestedID := "customer_object_id"
	id, err := PrepareKNChildResourceID(context.Background(), requestedID)
	if err != nil {
		t.Fatalf("PrepareKNChildResourceID() with a requested ID returned %v", err)
	}
	if id != requestedID {
		t.Fatalf("PrepareKNChildResourceID() = %q, want requested ID %q", id, requestedID)
	}

	id, err = PrepareKNChildResourceID(context.Background(), "")
	if err != nil {
		t.Fatalf("PrepareKNChildResourceID() error = %v", err)
	}
	generatedID, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("generated ID %q is not a UUID: %v", id, err)
	}
	if generatedID.Version() != 7 {
		t.Fatalf("generated UUID version = %d, want 7", generatedID.Version())
	}
	if _, err := PrepareKNChildResourceID(context.Background(), "bad/id"); err == nil {
		t.Fatal("ID containing a slash must be rejected")
	}
	if _, err := PrepareKNChildResourceID(context.Background(), "bad*id"); err == nil {
		t.Fatal("ID containing a wildcard must be rejected")
	}
	if _, err := PrepareKNChildResourceID(context.Background(), " child-id"); err == nil {
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

func TestResourceParentTrackerCleanupBatchesAndRetainsFailedEdges(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, tracker, _ := WithResourceParentTracker(context.Background())
	items := make([]interfaces.PermissionResourceParent, safeMutationBatchSize+1)
	firstBatchIDs := make([]string, safeMutationBatchSize)
	for i := range items {
		id := fmt.Sprintf("kn-1/ot-%03d", i)
		items[i] = interfaces.PermissionResourceParent{ResourceID: id, ParentID: "kn-1"}
		if i < safeMutationBatchSize {
			firstBatchIDs[i] = id
		}
	}
	TrackResourceParents(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, interfaces.RESOURCE_TYPE_KN, items)
	failedID := items[safeMutationBatchSize].ResourceID
	cleanupErr := errors.New("safe unavailable")
	ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, firstBatchIDs).Return(nil)
	ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{failedID}).Return(cleanupErr)
	if err := tracker.Cleanup(ctx, ps); !errors.Is(err, cleanupErr) {
		t.Fatalf("Cleanup() error = %v, want %v", err, cleanupErr)
	}
	ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{failedID}).Return(nil)
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

func TestCreatedPolicyTrackerCompensatesNestedWritesInTypeBatches(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, tracker, owner := WithCreatedPolicyTracker(context.Background())
	if !owner {
		t.Fatal("new tracker must have an owner")
	}
	childCtx, childTracker, childOwner := WithCreatedPolicyTracker(ctx)
	if childOwner || childTracker != tracker {
		t.Fatal("nested calls must share the outer transaction tracker")
	}
	TrackCreatedPolicies(childCtx, []interfaces.PermissionResource{
		{Type: interfaces.RESOURCE_TYPE_ACTION_TYPE, ID: "kn-1/at-2"},
		{Type: interfaces.RESOURCE_TYPE_KN, ID: "kn-1"},
		{Type: interfaces.RESOURCE_TYPE_ACTION_TYPE, ID: "kn-1/at-1"},
		{Type: interfaces.RESOURCE_TYPE_ACTION_TYPE, ID: "kn-1/at-1"},
	})
	gomock.InOrder(
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_ACTION_TYPE,
			[]string{"kn-1/at-1", "kn-1/at-2"}).Return(nil),
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_KN,
			[]string{"kn-1"}).Return(nil),
	)
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

func TestCleanupKNChildAuthorizationPreservesParentWhenPoliciesFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	policyErr := errors.New("policy cleanup failed")
	resourceIDs := []string{"kn-1/ot-1"}
	ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, resourceIDs).Return(policyErr)
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

func TestCleanupKNChildAuthorizationHonorsCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CleanupKNChildAuthorization(ctx, ps, interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1", []string{"ot-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup error = %v, want context.Canceled", err)
	}
}

func TestAuthorizationCleanupTrackerContinuesAfterCallerCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	ctx, tracker, _ := WithAuthorizationCleanupTracker(ctx)
	TrackKNChildAuthorizationCleanup(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn-1", []string{"ot-1"})
	cancel()
	var cleanupCtx context.Context
	ids := []string{"kn-1/ot-1"}
	gomock.InOrder(
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, ids).
			DoAndReturn(func(ctx context.Context, _ string, _ []string) error {
				cleanupCtx = ctx
				if err := ctx.Err(); err != nil {
					t.Fatalf("post-commit cleanup inherited caller cancellation: %v", err)
				}
				deadline, ok := ctx.Deadline()
				if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > resourceParentCleanupTimeout {
					t.Fatal("post-commit cleanup must have an independent, bounded deadline")
				}
				return nil
			}),
		ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, ids).
			DoAndReturn(func(ctx context.Context, _ string, _ []string) error {
				return ctx.Err()
			}),
	)
	if err := tracker.Cleanup(ctx, ps); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if !errors.Is(cleanupCtx.Err(), context.Canceled) {
		t.Fatal("cleanup context was not released")
	}
}

func TestAuthorizationCleanupTrackerAttemptsIndependentEntriesAndJoinsErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ctx, tracker, _ := WithAuthorizationCleanupTracker(context.Background())
	for _, resourceType := range []string{interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		interfaces.RESOURCE_TYPE_RELATION_TYPE, interfaces.RESOURCE_TYPE_ACTION_TYPE} {
		TrackKNChildAuthorizationCleanup(ctx, resourceType, "kn-1", []string{"child-1"})
	}
	firstErr, secondErr := errors.New("first cleanup failed"), errors.New("second cleanup failed")
	calls := 0
	var successfulType string
	ps.EXPECT().DeleteResources(gomock.Any(), gomock.Any(), []string{"kn-1/child-1"}).
		DoAndReturn(func(_ context.Context, resourceType string, _ []string) error {
			calls++
			switch calls {
			case 1:
				return firstErr
			case 2:
				return secondErr
			default:
				successfulType = resourceType
				return nil
			}
		}).Times(3)
	ps.EXPECT().DeleteResourceParents(gomock.Any(), gomock.Any(), []string{"kn-1/child-1"}).
		DoAndReturn(func(_ context.Context, resourceType string, _ []string) error {
			if calls != 3 || resourceType != successfulType {
				t.Fatal("parent cleanup ran for a resource whose policy cleanup failed")
			}
			return nil
		})
	if err := tracker.Cleanup(ctx, ps); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Cleanup() error = %v, want both cleanup failures", err)
	}
}
