// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"errors"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type resourceParentTrackerKey struct{}

type trackedResourceParent struct {
	resourceType string
	resourceID   string
}

// ResourceParentTracker records parent edges written inside a caller-owned
// database transaction so its owner can compensate them if the transaction
// does not commit.
type ResourceParentTracker struct {
	mu      sync.Mutex
	ps      interfaces.PermissionService
	entries map[string]trackedResourceParent
}

// WithResourceParentTracker returns the active tracker or installs one. The
// owner is responsible for cleanup when its database transaction fails.
func WithResourceParentTracker(ctx context.Context) (context.Context, *ResourceParentTracker, bool) {
	if tracker, ok := ctx.Value(resourceParentTrackerKey{}).(*ResourceParentTracker); ok {
		return ctx, tracker, false
	}
	tracker := &ResourceParentTracker{entries: map[string]trackedResourceParent{}}
	return context.WithValue(ctx, resourceParentTrackerKey{}, tracker), tracker, true
}

func trackResourceParents(ctx context.Context, ps interfaces.PermissionService,
	resourceType string, items []interfaces.PermissionResourceParent) {

	tracker, ok := ctx.Value(resourceParentTrackerKey{}).(*ResourceParentTracker)
	if !ok || len(items) == 0 {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.ps = ps
	for _, item := range items {
		tracker.entries[resourceType+"\x00"+item.ResourceID] = trackedResourceParent{
			resourceType: resourceType,
			resourceID:   item.ResourceID,
		}
	}
}

// Cleanup removes all tracked parent edges. It is safe to call more than once.
func (tracker *ResourceParentTracker) Cleanup(ctx context.Context) error {
	tracker.mu.Lock()
	ps := tracker.ps
	entries := make([]trackedResourceParent, 0, len(tracker.entries))
	for _, entry := range tracker.entries {
		entries = append(entries, entry)
	}
	tracker.entries = map[string]trackedResourceParent{}
	tracker.mu.Unlock()

	if len(entries) == 0 || ps == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceParentCleanupTimeout)
	defer cancel()

	resourceIDsByType := make(map[string][]string)
	for _, entry := range entries {
		resourceIDsByType[entry.resourceType] = append(resourceIDsByType[entry.resourceType], entry.resourceID)
	}

	var cleanupErrs []error
	for resourceType, resourceIDs := range resourceIDsByType {
		if err := ps.DeleteResourceParents(cleanupCtx, resourceType, resourceIDs); err != nil {
			logger.Errorf("Delete tracked resource parents after transaction failure: resource type %s: %v",
				resourceType, err)
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}
