// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

const authorizationCleanupTimeout = 5 * time.Second

type resourceParentTrackerKey struct{}
type authorizationCleanupTrackerKey struct{}
type createdPolicyTrackerKey struct{}

type trackedResourceParent struct {
	resourceType string
	parentType   string
	resourceID   string
	parentID     string
}

// ResourceParentTracker records Safe parent edges written inside a business transaction.
// The transaction owner uses it to compensate all nested writes if the transaction fails.
type ResourceParentTracker struct {
	mu      sync.Mutex
	entries map[string]trackedResourceParent
}

type trackedAuthorizationCleanup struct {
	resourceType string
	knID         string
	childIDs     []string
}

// AuthorizationCleanupTracker delays Safe cleanup until the owning database
// transaction has committed successfully.
type AuthorizationCleanupTracker struct {
	mu      sync.Mutex
	entries map[string]trackedAuthorizationCleanup
}

type trackedCreatedPolicy struct {
	resourceType string
	resourceID   string
}

// CreatedPolicyTracker records Safe policies created inside a business transaction.
// The transaction owner removes them if any nested operation or the commit fails.
type CreatedPolicyTracker struct {
	mu      sync.Mutex
	entries map[string]trackedCreatedPolicy
}

// WithResourceParentTracker returns the existing transaction tracker or installs a new one.
// The owner flag identifies the caller responsible for cleanup on transaction failure.
func WithResourceParentTracker(ctx context.Context) (context.Context, *ResourceParentTracker, bool) {
	if tracker, ok := ctx.Value(resourceParentTrackerKey{}).(*ResourceParentTracker); ok {
		return ctx, tracker, false
	}
	tracker := &ResourceParentTracker{entries: map[string]trackedResourceParent{}}
	return context.WithValue(ctx, resourceParentTrackerKey{}, tracker), tracker, true
}

// TrackResourceParents adds successfully written parent edges to the active transaction tracker.
func TrackResourceParents(ctx context.Context, resourceType, parentType string,
	items []interfaces.PermissionResourceParent) {

	tracker, ok := ctx.Value(resourceParentTrackerKey{}).(*ResourceParentTracker)
	if !ok || len(items) == 0 {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for _, item := range items {
		key := resourceType + "\x00" + item.ResourceID
		tracker.entries[key] = trackedResourceParent{
			resourceType: resourceType,
			parentType:   parentType,
			resourceID:   item.ResourceID,
			parentID:     item.ParentID,
		}
	}
}

// Cleanup removes all tracked parent edges. It is safe to call more than once.
func (tracker *ResourceParentTracker) Cleanup(ctx context.Context, ps interfaces.PermissionService) error {
	tracker.mu.Lock()
	entries := make([]trackedResourceParent, 0, len(tracker.entries))
	for _, entry := range tracker.entries {
		entries = append(entries, entry)
	}
	tracker.entries = map[string]trackedResourceParent{}
	tracker.mu.Unlock()

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authorizationCleanupTimeout)
	defer cancel()

	var cleanupErrs []error
	for _, entry := range entries {
		if err := ps.DeleteResourceParents(cleanupCtx, entry.resourceType, []string{entry.resourceID}); err != nil {
			logAuthorizationCleanupFailure("resource_parent", entry.resourceType, entry.resourceID,
				entry.parentType, entry.parentID, err)
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

// WithCreatedPolicyTracker returns the existing transaction tracker or installs a new one.
// The owner flag identifies the caller responsible for cleanup on transaction failure.
func WithCreatedPolicyTracker(ctx context.Context) (context.Context, *CreatedPolicyTracker, bool) {
	if tracker, ok := ctx.Value(createdPolicyTrackerKey{}).(*CreatedPolicyTracker); ok {
		return ctx, tracker, false
	}
	tracker := &CreatedPolicyTracker{entries: map[string]trackedCreatedPolicy{}}
	return context.WithValue(ctx, createdPolicyTrackerKey{}, tracker), tracker, true
}

// TrackCreatedPolicies adds resources that may have been written by Safe to the
// active transaction tracker. Callers track before the write so partial remote
// success is compensated when the write returns an error.
func TrackCreatedPolicies(ctx context.Context, resources []interfaces.PermissionResource) {
	tracker, ok := ctx.Value(createdPolicyTrackerKey{}).(*CreatedPolicyTracker)
	if !ok || len(resources) == 0 {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for _, resource := range resources {
		key := resource.Type + "\x00" + resource.ID
		tracker.entries[key] = trackedCreatedPolicy{
			resourceType: resource.Type,
			resourceID:   resource.ID,
		}
	}
}

// Cleanup removes all tracked policies in deterministic type batches. It is
// safe to call more than once.
func (tracker *CreatedPolicyTracker) Cleanup(ctx context.Context, ps interfaces.PermissionService) error {
	tracker.mu.Lock()
	byType := make(map[string][]string)
	for _, entry := range tracker.entries {
		byType[entry.resourceType] = append(byType[entry.resourceType], entry.resourceID)
	}
	tracker.entries = map[string]trackedCreatedPolicy{}
	tracker.mu.Unlock()

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authorizationCleanupTimeout)
	defer cancel()

	resourceTypes := make([]string, 0, len(byType))
	for resourceType := range byType {
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Strings(resourceTypes)

	var cleanupErrs []error
	for _, resourceType := range resourceTypes {
		resourceIDs := byType[resourceType]
		sort.Strings(resourceIDs)
		if err := ps.DeleteResources(cleanupCtx, resourceType, resourceIDs); err != nil {
			for _, resourceID := range resourceIDs {
				logAuthorizationCleanupFailure("policy", resourceType, resourceID, "", "", err)
			}
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

// WithAuthorizationCleanupTracker returns the existing post-commit cleanup
// tracker or installs one for the transaction owner.
func WithAuthorizationCleanupTracker(ctx context.Context) (context.Context, *AuthorizationCleanupTracker, bool) {
	if tracker, ok := ctx.Value(authorizationCleanupTrackerKey{}).(*AuthorizationCleanupTracker); ok {
		return ctx, tracker, false
	}
	tracker := &AuthorizationCleanupTracker{entries: map[string]trackedAuthorizationCleanup{}}
	return context.WithValue(ctx, authorizationCleanupTrackerKey{}, tracker), tracker, true
}

// TrackKNChildAuthorizationCleanup queues canonical child resources for cleanup
// after the database transaction commits.
func TrackKNChildAuthorizationCleanup(ctx context.Context, resourceType, knID string, childIDs []string) {
	tracker, ok := ctx.Value(authorizationCleanupTrackerKey{}).(*AuthorizationCleanupTracker)
	if !ok || len(childIDs) == 0 {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	key := resourceType + "\x00" + knID
	entry := tracker.entries[key]
	entry.resourceType = resourceType
	entry.knID = knID
	for _, childID := range childIDs {
		duplicate := false
		for _, trackedID := range entry.childIDs {
			if trackedID == childID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			entry.childIDs = append(entry.childIDs, childID)
		}
	}
	tracker.entries[key] = entry
}

// Cleanup removes all queued policies and parent edges. It is safe to call
// more than once.
func (tracker *AuthorizationCleanupTracker) Cleanup(ctx context.Context, ps interfaces.PermissionService) error {
	tracker.mu.Lock()
	entries := make([]trackedAuthorizationCleanup, 0, len(tracker.entries))
	for _, entry := range tracker.entries {
		entries = append(entries, entry)
	}
	tracker.entries = map[string]trackedAuthorizationCleanup{}
	tracker.mu.Unlock()

	var cleanupErrs []error
	for _, entry := range entries {
		if err := CleanupKNChildAuthorization(ctx, ps, entry.resourceType, entry.knID, entry.childIDs); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

// CleanupKNChildAuthorization attempts both policy and parent-edge cleanup.
// Failures are logged per resource so an operations job can replay them later.
func CleanupKNChildAuthorization(ctx context.Context, ps interfaces.PermissionService,
	resourceType, knID string, childIDs []string) error {

	resourceIDs := interfaces.KNChildResourceIDs(knID, childIDs)
	if len(resourceIDs) == 0 {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authorizationCleanupTimeout)
	defer cancel()

	var cleanupErrs []error
	if err := ps.DeleteResources(cleanupCtx, resourceType, resourceIDs); err != nil {
		for _, resourceID := range resourceIDs {
			logAuthorizationCleanupFailure("policy", resourceType, resourceID,
				interfaces.RESOURCE_TYPE_KN, knID, err)
		}
		cleanupErrs = append(cleanupErrs, err)
	}
	if err := ps.DeleteResourceParents(cleanupCtx, resourceType, resourceIDs); err != nil {
		for _, resourceID := range resourceIDs {
			logAuthorizationCleanupFailure("resource_parent", resourceType, resourceID,
				interfaces.RESOURCE_TYPE_KN, knID, err)
		}
		cleanupErrs = append(cleanupErrs, err)
	}
	return errors.Join(cleanupErrs...)
}

func logAuthorizationCleanupFailure(cleanupKind, resourceType, resourceID, parentType, parentID string, err error) {
	logger.GetLogger().Errorw("authorization cleanup failed",
		"cleanup_kind", cleanupKind,
		"resource_type", resourceType,
		"resource_id", resourceID,
		"parent_type", parentType,
		"parent_id", parentID,
		"error", err.Error())
}

// PrepareKNChildResourceID preserves valid client-provided IDs and generates an ID when omitted.
func PrepareKNChildResourceID(ctx context.Context, requestedID string) (string, error) {
	id := requestedID
	if id == "" {
		generatedID, err := uuid.NewV7()
		if err != nil {
			return "", fmt.Errorf("generate child resource UUIDv7: %w", err)
		}
		id = generatedID.String()
	}
	if !interfaces.IsValidAuthorizationID(id) {
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ID).
			WithErrorDetails(fmt.Sprintf("invalid child resource ID %q", id))
	}
	return id, nil
}

// ValidateKNChildAuthorizationIDs rejects ambiguous Safe resource references.
func ValidateKNChildAuthorizationIDs(ctx context.Context, knID string, childIDs []string) error {
	if !interfaces.IsValidAuthorizationID(knID) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ID).
			WithErrorDetails(fmt.Sprintf("invalid knowledge network ID %q", knID))
	}
	for _, childID := range childIDs {
		if !interfaces.IsValidAuthorizationID(childID) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ID).
				WithErrorDetails(fmt.Sprintf("invalid child resource ID %q", childID))
		}
	}
	return nil
}
