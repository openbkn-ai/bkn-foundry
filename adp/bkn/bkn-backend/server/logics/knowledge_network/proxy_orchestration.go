// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

const (
	proxyLockLease = 5 * time.Minute
	proxyLockWait  = 30 * time.Second
)

type proxyPublishPlan struct {
	mapping        *interfaces.KNProxyAccount
	delegatorID    string
	modelVersion   string
	lockOwner      string
	createdMapping bool
}

type publishedProxyBindingCacheEntry struct {
	modelVersion string
	sources      []interfaces.ProxyGrantSourceSpec
}

type missingProxyPermission struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Operation    string `json:"operation"`
	BindingType  string `json:"binding_type"`
	BindingID    string `json:"binding_id"`
}

type missingProxyPermissionDetails struct {
	MissingPermissions []missingProxyPermission `json:"missing_permissions"`
}

func (kns *knowledgeNetworkService) proxyOrchestrationEnabled(branch string) bool {
	return branch == interfaces.MAIN_BRANCH && kns.kpa != nil && kns.mpa != nil
}

func (kns *knowledgeNetworkService) prepareProxyPublish(ctx context.Context, kn *interfaces.KN) (*proxyPublishPlan, error) {
	return kns.prepareProxyPublishWithBaseline(ctx, kn, false, "")
}

func (kns *knowledgeNetworkService) prepareProxyImport(ctx context.Context, kn *interfaces.KN,
	mergeCurrent bool, mergeMode string) (*proxyPublishPlan, error) {
	return kns.prepareProxyPublishWithBaseline(ctx, kn, mergeCurrent, mergeMode)
}

func (kns *knowledgeNetworkService) prepareProxyPublishWithBaseline(ctx context.Context, kn *interfaces.KN,
	mergeCurrent bool, mergeMode string) (*proxyPublishPlan, error) {
	if !kns.proxyOrchestrationEnabled(kn.Branch) {
		return nil, nil
	}
	plan, err := kns.beginProxyPublish(ctx, kn, true, mergeCurrent)
	if err != nil {
		return nil, err
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			kns.abortCreatedProxy(context.WithoutCancel(ctx), plan)
			kns.releaseProxyLock(context.WithoutCancel(ctx), plan)
		}
	}()

	candidate := kn
	if mergeCurrent {
		current, loadErr := kns.ExportKNForProjection(ctx, kn.KNID)
		if loadErr != nil {
			return nil, loadErr
		}
		candidate = mergeProxyMutationChanges(current, kn, mergeMode)
	}
	sources, version, err := buildProxyGrantSources(candidate)
	if err != nil {
		return nil, invalidProxyTargetError(ctx, err)
	}
	// Check the complete desired set, not only additions. Safe preserves a still-valid
	// historical delegator and asks the current editor to take over only when that
	// delegator has lost the exact downstream operation. Doing this before the
	// business write avoids discovering an invalid retained source after commit.
	if err := kns.preflightProxySources(ctx, plan.mapping.ProxyAccountID, plan.delegatorID, sources); err != nil {
		return nil, err
	}
	plan.modelVersion = version
	releaseOnError = false
	return plan, nil
}

func (kns *knowledgeNetworkService) beginProxyPublish(ctx context.Context, kn *interfaces.KN,
	createIfMissing, checkMissingWrite bool) (*proxyPublishPlan, error) {
	delegatorID := accountIDFromContext(ctx)
	if delegatorID == "" {
		return nil, proxyHTTPError(ctx, http.StatusForbidden, "proxy delegator identity is unavailable")
	}

	mapping, err := kns.kpa.Get(ctx, kn.KNID)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load knowledge network proxy mapping")
	}
	createdMapping := false
	if mapping == nil {
		if !createIfMissing {
			return nil, proxyHTTPError(ctx, http.StatusConflict, "knowledge network proxy mapping is unavailable")
		}
		if checkMissingWrite {
			if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_KN,
				ID:   kn.KNID,
			}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
				return nil, err
			}
		}
		mappingKN := kn
		if checkMissingWrite && strings.TrimSpace(kn.KNName) == "" {
			existingKN, loadErr := kns.kna.GetKNByID(ctx, kn.KNID, interfaces.MAIN_BRANCH)
			if loadErr != nil || existingKN == nil {
				return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load knowledge network for proxy mapping")
			}
			mappingCopy := *kn
			mappingCopy.KNName = existingKN.KNName
			mappingKN = &mappingCopy
		}
		mapping, createdMapping, err = kns.createProxyMapping(ctx, mappingKN)
		if err != nil {
			return nil, err
		}
	}
	if mapping.LifecycleStatus == interfaces.KNProxyLifecycleArchived && createIfMissing {
		if checkMissingWrite {
			if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_KN,
				ID:   kn.KNID,
			}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
				return nil, err
			}
		}
		if _, err := kns.mpa.Restore(ctx, mapping.ProxyAccountID); err != nil {
			return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "restore knowledge network proxy")
		}
		if err := kns.kpa.SetLifecycle(ctx, mapping.KNID, interfaces.KNProxyLifecycleActive,
			time.Now().UnixMilli()); err != nil {
			cleanupManagedProxy(context.WithoutCancel(ctx), kns.mpa, mapping.ProxyAccountID)
			return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "record restored knowledge network proxy")
		}
		mapping.LifecycleStatus = interfaces.KNProxyLifecycleActive
		createdMapping = true
	}
	if mapping.LifecycleStatus != interfaces.KNProxyLifecycleActive {
		return nil, proxyHTTPError(ctx, http.StatusConflict, "knowledge network proxy is not active")
	}

	lockOwner := uuid.NewString()
	if err := kns.acquireProxyLock(ctx, kn.KNID, lockOwner); err != nil {
		if createdMapping && !checkMissingWrite {
			kns.abortCreatedProxy(context.WithoutCancel(ctx), &proxyPublishPlan{
				mapping: mapping, createdMapping: true,
			})
		}
		return nil, err
	}
	return &proxyPublishPlan{
		mapping: mapping, delegatorID: delegatorID, lockOwner: lockOwner, createdMapping: createdMapping,
	}, nil
}

func (kns *knowledgeNetworkService) createProxyMapping(ctx context.Context, kn *interfaces.KN) (*interfaces.KNProxyAccount, bool, error) {
	account, created, err := kns.mpa.Create(ctx, kn.KNID, "BKN proxy: "+kn.KNName)
	if err != nil {
		return nil, false, proxyHTTPError(ctx, http.StatusServiceUnavailable, "create managed knowledge network proxy")
	}
	now := time.Now().UnixMilli()
	mapping := &interfaces.KNProxyAccount{
		KNID:             kn.KNID,
		ProxyAccountID:   account.ProxyAccountID,
		ProxyAccountType: interfaces.KNProxyAccountTypeApp,
		LifecycleStatus:  interfaces.KNProxyLifecycleActive,
		Version:          1,
		SyncStatus:       interfaces.KNProxySyncPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	mapping, mappingCreated, err := kns.kpa.Ensure(ctx, mapping)
	if err == nil {
		if created && !mappingCreated && mapping.ProxyAccountID != account.ProxyAccountID {
			cleanupManagedProxy(context.WithoutCancel(ctx), kns.mpa, account.ProxyAccountID)
		}
		return mapping, mappingCreated, nil
	}

	// Compensate only when this request created the account and no concurrent
	// creator installed a mapping for that same account. A different winning
	// proxy is safe to preserve while this request's unreferenced account is archived.
	if created {
		if existing, getErr := kns.kpa.Get(ctx, kn.KNID); getErr == nil &&
			(existing == nil || existing.ProxyAccountID != account.ProxyAccountID) {
			cleanupManagedProxy(context.WithoutCancel(ctx), kns.mpa, account.ProxyAccountID)
		}
	}
	return nil, false, proxyHTTPError(ctx, http.StatusServiceUnavailable, "persist knowledge network proxy mapping")
}

func cleanupManagedProxy(ctx context.Context, mpa interfaces.ManagedProxyAccess, proxyAccountID string) {
	if _, err := mpa.Disable(ctx, proxyAccountID); err != nil {
		otellog.LogError(ctx, "Managed proxy compensation disable failed", err)
		return
	}
	if _, err := mpa.Archive(ctx, proxyAccountID); err != nil {
		otellog.LogError(ctx, "Managed proxy compensation archive failed", err)
	}
}

func (kns *knowledgeNetworkService) abortCreatedProxy(ctx context.Context, plan *proxyPublishPlan) {
	if plan == nil || !plan.createdMapping {
		return
	}
	cleanupManagedProxy(ctx, kns.mpa, plan.mapping.ProxyAccountID)
	if err := kns.kpa.SetLifecycle(ctx, plan.mapping.KNID, interfaces.KNProxyLifecycleArchived,
		time.Now().UnixMilli()); err != nil {
		otellog.LogError(ctx, "Record compensated proxy archive failed", err)
	}
}

func (kns *knowledgeNetworkService) preflightProxySources(ctx context.Context, proxyID, delegatorID string,
	sources []interfaces.ProxyGrantSourceSpec) error {
	missing := make([]missingProxyPermission, 0)
	for _, source := range sources {
		result, err := kns.mpa.CheckGrant(ctx, proxyID, delegatorID, source)
		if err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy permission preflight failed")
		}
		if !result.Allowed {
			missing = append(missing, missingProxyPermission{
				ResourceType: source.ResourceType,
				ResourceID:   source.ResourceID,
				Operation:    source.Operation,
				BindingType:  source.BindingType,
				BindingID:    source.BindingID,
			})
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool {
			left := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", missing[i].ResourceType,
				missing[i].ResourceID, missing[i].Operation, missing[i].BindingType, missing[i].BindingID)
			right := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", missing[j].ResourceType,
				missing[j].ResourceID, missing[j].Operation, missing[j].BindingType, missing[j].BindingID)
			return left < right
		})
		return rest.NewHTTPError(ctx, http.StatusForbidden,
			berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing).
			WithErrorDetails(missingProxyPermissionDetails{MissingPermissions: missing})
	}
	return nil
}

// PublishKNChildMutation applies one standalone child-resource write through
// the same serialized, fail-closed proxy publication lifecycle as whole-network
// publication. The pending state and business rows commit atomically.
func (kns *knowledgeNetworkService) PublishKNChildMutation(ctx context.Context, changes *interfaces.KN, mergeMode string,
	mutate func(context.Context, *sql.Tx) error) error {
	if mutate == nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, "child resource mutation callback is unavailable")
	}
	if changes == nil || changes.KNID == "" || changes.Branch == "" {
		return proxyHTTPError(ctx, http.StatusBadRequest, "child resource mutation identity is invalid")
	}
	if !kns.proxyOrchestrationEnabled(changes.Branch) {
		return mutate(ctx, nil)
	}
	if err := prepareProxyMutationIDs(ctx, changes); err != nil {
		return err
	}

	plan, err := kns.beginProxyPublish(ctx, changes, true, true)
	if err != nil {
		return err
	}
	defer kns.releaseProxyLock(context.WithoutCancel(ctx), plan)

	// Reload after acquiring the publication lock. This prevents a candidate
	// assembled from a stale model from omitting targets published by a writer
	// that held the lock immediately before this request.
	current, err := kns.ExportKNForProjection(ctx, changes.KNID)
	if err != nil {
		return err
	}
	candidate := mergeProxyMutationChanges(current, changes, mergeMode)
	sources, version, err := buildProxyGrantSources(candidate)
	if err != nil {
		return invalidProxyTargetError(ctx, err)
	}
	// Preflight the complete desired set. Safe accepts retained sources whose
	// historical delegator is still valid, and requires this editor's exact
	// downstream operation only for additions or an explicit delegator transfer.
	// Removed sources are absent from the candidate and therefore need no check.
	if err := kns.preflightProxySources(ctx, plan.mapping.ProxyAccountID, plan.delegatorID, sources); err != nil {
		return err
	}
	plan.modelVersion = version

	mutationCtx, parentTracker, parentTrackerOwner := permission.WithResourceParentTracker(ctx)
	mutationCtx, policyTracker, policyTrackerOwner := permission.WithCreatedPolicyTracker(mutationCtx)
	mutationCtx, cleanupTracker, cleanupTrackerOwner := permission.WithAuthorizationCleanupTracker(mutationCtx)
	tx, err := kns.db.BeginTx(mutationCtx, nil)
	if err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, "begin child resource publication")
	}
	rollback := func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != sql.ErrTxDone {
			otellog.LogError(ctx, "Rollback child resource publication failed", rollbackErr)
		}
	}
	cleanupCreatedAuthorization := func() {
		if parentTrackerOwner {
			_ = parentTracker.Cleanup(mutationCtx, kns.ps)
		}
		if policyTrackerOwner {
			_ = policyTracker.Cleanup(mutationCtx, kns.ps)
		}
	}

	if err := kns.markProxyPending(mutationCtx, tx, plan); err != nil {
		rollback()
		return err
	}
	if err := mutate(mutationCtx, tx); err != nil {
		rollback()
		cleanupCreatedAuthorization()
		return err
	}
	if err := tx.Commit(); err != nil {
		rollback()
		cleanupCreatedAuthorization()
		return proxyHTTPError(ctx, http.StatusInternalServerError, "commit child resource publication")
	}
	if cleanupTrackerOwner {
		_ = cleanupTracker.Cleanup(mutationCtx, kns.ps)
	}
	return kns.finishProxyPublish(ctx, plan)
}

func prepareProxyMutationIDs(ctx context.Context, changes *interfaces.KN) error {
	for _, objectType := range changes.ObjectTypes {
		if objectType == nil {
			continue
		}
		id, err := permission.PrepareKNChildResourceID(ctx, objectType.OTID)
		if err != nil {
			return err
		}
		objectType.OTID = id
	}
	for _, relationType := range changes.RelationTypes {
		if relationType == nil {
			continue
		}
		id, err := permission.PrepareKNChildResourceID(ctx, relationType.RTID)
		if err != nil {
			return err
		}
		relationType.RTID = id
	}
	for _, actionType := range changes.ActionTypes {
		if actionType == nil {
			continue
		}
		id, err := permission.PrepareKNChildResourceID(ctx, actionType.ATID)
		if err != nil {
			return err
		}
		actionType.ATID = id
	}
	for _, metric := range changes.Metrics {
		if metric == nil {
			continue
		}
		id, err := permission.PrepareKNChildResourceID(ctx, strings.TrimSpace(metric.ID))
		if err != nil {
			return err
		}
		metric.ID = id
	}
	return nil
}

func mergeProxyMutationChanges(current, changes *interfaces.KN, mergeMode string) *interfaces.KN {
	candidate := *current
	keepExisting := mergeMode != interfaces.ImportMode_Overwrite
	candidate.ObjectTypes = mergeProxyItems(current.ObjectTypes, changes.ObjectTypes, keepExisting,
		func(item *interfaces.ObjectType) (string, bool) {
			if item == nil {
				return "", false
			}
			return item.OTID, true
		})
	candidate.RelationTypes = mergeProxyItems(current.RelationTypes, changes.RelationTypes, keepExisting,
		func(item *interfaces.RelationType) (string, bool) {
			if item == nil {
				return "", false
			}
			return item.RTID, true
		})
	candidate.ActionTypes = mergeProxyItems(current.ActionTypes, changes.ActionTypes, keepExisting,
		func(item *interfaces.ActionType) (string, bool) {
			if item == nil {
				return "", false
			}
			return item.ATID, true
		})
	candidate.Metrics = mergeProxyItems(current.Metrics, changes.Metrics, keepExisting,
		func(item *interfaces.MetricDefinition) (string, bool) {
			if item == nil {
				return "", false
			}
			return item.ID, true
		})
	return &candidate
}

func mergeProxyItems[T any](current, changes []T, keepExisting bool,
	itemID func(T) (string, bool)) []T {
	byID := make(map[string]T, len(current)+len(changes))
	for _, item := range current {
		if id, ok := itemID(item); ok {
			byID[id] = item
		}
	}
	for _, item := range changes {
		id, ok := itemID(item)
		if !ok {
			continue
		}
		if _, exists := byID[id]; keepExisting && exists {
			continue
		}
		byID[id] = item
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	merged := make([]T, 0, len(ids))
	for _, id := range ids {
		merged = append(merged, byID[id])
	}
	return merged
}

func (kns *knowledgeNetworkService) markProxyPending(ctx context.Context, tx *sql.Tx, plan *proxyPublishPlan) error {
	if plan == nil {
		return nil
	}
	if err := kns.kpa.SetPending(ctx, tx, plan.mapping.KNID, plan.modelVersion, plan.delegatorID, time.Now().UnixMilli()); err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "mark proxy synchronization pending")
	}
	return nil
}

func (kns *knowledgeNetworkService) finishProxyPublish(ctx context.Context, plan *proxyPublishPlan) error {
	if plan == nil {
		return nil
	}
	latest, err := kns.ExportKNForProjection(ctx, plan.mapping.KNID)
	if err != nil {
		kns.recordProxySyncFailure(ctx, plan, plan.modelVersion, err)
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "reload latest published main model")
	}
	sources, latestVersion, err := buildProxyGrantSources(latest)
	if err != nil {
		kns.recordProxySyncFailure(ctx, plan, plan.modelVersion, err)
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "derive latest proxy permissions")
	}
	if latestVersion != plan.modelVersion {
		if err := kns.markProxyPendingInNewTransaction(ctx, plan, latestVersion); err != nil {
			return err
		}
		plan.modelVersion = latestVersion
	}
	if _, err := kns.mpa.SyncGrants(ctx, plan.mapping.ProxyAccountID, plan.delegatorID, sources); err != nil {
		kns.recordProxySyncFailure(ctx, plan, latestVersion, err)
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "synchronize latest proxy permissions")
	}
	updated, err := kns.kpa.SetSyncResult(ctx, plan.mapping.KNID, latestVersion,
		interfaces.KNProxySyncReady, latestVersion, "", time.Now().UnixMilli())
	if err != nil || !updated {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "record proxy synchronization result")
	}
	return nil
}

func (kns *knowledgeNetworkService) markProxyPendingInNewTransaction(ctx context.Context, plan *proxyPublishPlan, modelVersion string) error {
	tx, err := kns.db.BeginTx(ctx, nil)
	if err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "begin proxy synchronization state update")
	}
	defer tx.Rollback()
	copy := *plan
	copy.modelVersion = modelVersion
	if err := kns.markProxyPending(ctx, tx, &copy); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "commit proxy synchronization state update")
	}
	return nil
}

func (kns *knowledgeNetworkService) recordProxySyncFailure(ctx context.Context, plan *proxyPublishPlan, modelVersion string, cause error) {
	detail := fmt.Sprintf("%T", cause)
	if cause != nil {
		detail = cause.Error()
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
	}
	if _, err := kns.kpa.SetSyncResult(context.WithoutCancel(ctx), plan.mapping.KNID, modelVersion,
		interfaces.KNProxySyncFailed, "", detail, time.Now().UnixMilli()); err != nil {
		otellog.LogError(ctx, "Record proxy synchronization failure failed", err)
	}
}

func (kns *knowledgeNetworkService) acquireProxyLock(ctx context.Context, knID, owner string) error {
	deadline := time.Now().Add(proxyLockWait)
	for {
		now := time.Now()
		acquired, err := kns.kpa.TryAcquireLock(ctx, knID, owner, now.UnixMilli(), now.Add(proxyLockLease).UnixMilli())
		if err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "acquire knowledge network publish lock")
		}
		if acquired {
			return nil
		}
		if !now.Before(deadline) {
			return proxyHTTPError(ctx, http.StatusConflict, "knowledge network publication is already in progress")
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return proxyHTTPError(ctx, http.StatusRequestTimeout, "knowledge network publication wait cancelled")
		case <-timer.C:
		}
	}
}

func (kns *knowledgeNetworkService) releaseProxyLock(ctx context.Context, plan *proxyPublishPlan) {
	if plan == nil || plan.lockOwner == "" {
		return
	}
	if err := kns.kpa.ReleaseLock(ctx, plan.mapping.KNID, plan.lockOwner, time.Now().UnixMilli()); err != nil {
		otellog.LogError(ctx, "Release knowledge network publish lock failed", err)
	}
}

func (kns *knowledgeNetworkService) prepareProxyDelete(ctx context.Context, knID, branch string) (*proxyPublishPlan, error) {
	if !kns.proxyOrchestrationEnabled(branch) {
		return nil, nil
	}
	grantorID := accountIDFromContext(ctx)
	if grantorID == "" {
		return nil, proxyHTTPError(ctx, http.StatusForbidden, "proxy grantor identity is unavailable")
	}
	mapping, err := kns.kpa.Get(ctx, knID)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "knowledge network proxy mapping is unavailable")
	}
	// Existing installations can contain main-branch networks before proxy
	// backfill. Treat the missing mapping as legacy cleanup so upgrades do not
	// block deletion while the backfill is still pending.
	if mapping == nil {
		return nil, nil
	}
	plan := &proxyPublishPlan{mapping: mapping, delegatorID: grantorID, lockOwner: uuid.NewString(), modelVersion: mapping.PublishedModelVersion}
	if err := kns.acquireProxyLock(ctx, knID, plan.lockOwner); err != nil {
		return nil, err
	}
	return plan, nil
}

func (kns *knowledgeNetworkService) finalizeProxyDelete(ctx context.Context, plan *proxyPublishPlan) error {
	if plan == nil {
		return nil
	}
	if plan.mapping.LifecycleStatus == interfaces.KNProxyLifecycleArchived {
		return kns.ps.DeleteResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{plan.mapping.KNID})
	}
	account, err := kns.mpa.Disable(ctx, plan.mapping.ProxyAccountID)
	if err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "disable knowledge network proxy")
	}
	// Archive can succeed while persisting BKN's final lifecycle fails. The
	// idempotent disable response then reports archived, allowing a retry to
	// repair the mapping without regressing it to disabling. Grant cleanup is
	// still replayed because revoking an empty desired set is idempotent.
	alreadyArchived := account.LifecycleStatus == interfaces.KNProxyLifecycleArchived
	if !alreadyArchived {
		if err := kns.kpa.SetLifecycle(ctx, plan.mapping.KNID, interfaces.KNProxyLifecycleDisabling,
			time.Now().UnixMilli()); err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "record disabled knowledge network proxy")
		}
	}
	if _, err := kns.mpa.SyncGrants(ctx, plan.mapping.ProxyAccountID, plan.delegatorID,
		[]interfaces.ProxyGrantSourceSpec{}); err != nil {
		kns.recordProxySyncFailure(ctx, plan, plan.mapping.PublishedModelVersion, err)
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "clear knowledge network proxy grants")
	}
	if !alreadyArchived {
		if _, err := kns.mpa.Archive(ctx, plan.mapping.ProxyAccountID); err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "archive knowledge network proxy")
		}
	}
	if err := kns.kpa.SetLifecycle(ctx, plan.mapping.KNID, interfaces.KNProxyLifecycleArchived,
		time.Now().UnixMilli()); err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "record archived knowledge network proxy")
	}
	return kns.ps.DeleteResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{plan.mapping.KNID})
}

// FinalizeKNProxyDeletion resumes the recoverable tail of an ordered deletion
// after the knowledge-network rows have already committed.
func (kns *knowledgeNetworkService) FinalizeKNProxyDeletion(ctx context.Context, knID string) error {
	if !kns.proxyOrchestrationEnabled(interfaces.MAIN_BRANCH) {
		return proxyHTTPError(ctx, http.StatusNotFound, "knowledge network proxy mapping not found")
	}
	mapping, err := kns.kpa.Get(ctx, knID)
	if err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "load knowledge network proxy deletion state")
	}
	if mapping == nil {
		return proxyHTTPError(ctx, http.StatusNotFound, "knowledge network proxy mapping not found")
	}
	if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID},
		[]string{interfaces.OPERATION_TYPE_DELETE}); err != nil {
		return err
	}
	if mapping.LifecycleStatus == interfaces.KNProxyLifecycleArchived {
		return kns.ps.DeleteResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{knID})
	}
	plan, err := kns.prepareProxyDelete(ctx, knID, interfaces.MAIN_BRANCH)
	if err != nil {
		return err
	}
	defer kns.releaseProxyLock(context.WithoutCancel(ctx), plan)
	return kns.finalizeProxyDelete(ctx, plan)
}

// RetryKNProxySync re-reads the latest main model and never reuses a stale task's
// remembered additions or removals.
func (kns *knowledgeNetworkService) RetryKNProxySync(ctx context.Context, knID string) (*interfaces.KNProxyAccount, error) {
	if !kns.proxyOrchestrationEnabled(interfaces.MAIN_BRANCH) {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy orchestration is disabled")
	}
	if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID},
		[]string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return nil, err
	}
	latest, err := kns.ExportKNForProjection(ctx, knID)
	if err != nil {
		return nil, err
	}
	plan, err := kns.prepareProxyPublish(ctx, latest)
	if err != nil {
		return nil, err
	}
	defer kns.releaseProxyLock(context.WithoutCancel(ctx), plan)
	if err := kns.markProxyPendingInNewTransaction(ctx, plan, plan.modelVersion); err != nil {
		return nil, err
	}
	if err := kns.finishProxyPublish(ctx, plan); err != nil {
		return nil, err
	}
	return kns.kpa.Get(ctx, knID)
}

func (kns *knowledgeNetworkService) GetKNProxy(ctx context.Context, knID string) (*interfaces.KNProxyAccount, error) {
	if kns.kpa == nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy orchestration is disabled")
	}
	// This cluster-internal lookup resolves the service-managed execution
	// principal after the runtime has authorized the original business caller.
	// Requiring authorize here would incorrectly reject callers that can query
	// the network but are not allowed to administer its proxy lifecycle.
	mapping, err := kns.kpa.Get(ctx, knID)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load knowledge network proxy mapping")
	}
	if mapping == nil {
		return nil, proxyHTTPError(ctx, http.StatusNotFound, "knowledge network proxy mapping not found")
	}
	return mapping, nil
}

// GetGovernedKNProxy exposes proxy state only after checking the caller's
// authorize operation on the exact knowledge network. Runtime services must
// continue to use GetKNProxy after separately authorizing the business call.
func (kns *knowledgeNetworkService) GetGovernedKNProxy(ctx context.Context, knID string) (*interfaces.KNProxyGovernanceView, error) {
	if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_AUTHORIZE}); err != nil {
		return nil, err
	}
	mapping, err := kns.GetKNProxy(ctx, knID)
	if err != nil {
		return nil, err
	}
	return interfaces.NewKNProxyGovernanceView(mapping), nil
}

// ListGovernedKNProxies returns only mappings in the caller's authorize scope,
// so mapping existence and proxy identifiers are not disclosed across tenants.
func (kns *knowledgeNetworkService) ListGovernedKNProxies(ctx context.Context) (*interfaces.KNProxyAccountList, error) {
	if kns.kpa == nil {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxyUnavailable, "proxy orchestration is disabled")
	}
	mappings, err := kns.kpa.List(ctx)
	if err != nil {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxyUnavailable, "list knowledge network proxy mappings")
	}
	ids := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping != nil && strings.TrimSpace(mapping.KNID) != "" {
			ids = append(ids, mapping.KNID)
		}
	}
	if len(ids) == 0 {
		return &interfaces.KNProxyAccountList{Entries: []*interfaces.KNProxyGovernanceView{}}, nil
	}
	allowed, err := kns.ps.FilterResources(ctx, interfaces.RESOURCE_TYPE_KN, ids,
		[]string{interfaces.OPERATION_TYPE_AUTHORIZE}, true, interfaces.COMMON_OPERATIONS)
	if err != nil {
		return nil, err
	}
	entries := make([]*interfaces.KNProxyGovernanceView, 0, len(allowed))
	for _, mapping := range mappings {
		if mapping == nil {
			continue
		}
		if _, ok := allowed[mapping.KNID]; ok {
			entries = append(entries, interfaces.NewKNProxyGovernanceView(mapping))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].KNID < entries[j].KNID })
	return &interfaces.KNProxyAccountList{Entries: entries, Total: len(entries)}, nil
}

// ResolveKNProxyBinding validates a server-derived runtime target against the
// latest published main model and returns the proxy mapping only when that
// exact model version has finished permission synchronization.
func (kns *knowledgeNetworkService) ResolveKNProxyBinding(ctx context.Context, knID string,
	binding interfaces.KNProxyBinding) (*interfaces.KNProxyAccount, error) {
	if kns.kpa == nil {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxyUnavailable, "proxy orchestration is disabled")
	}
	mapping, err := kns.kpa.Get(ctx, knID)
	if err != nil {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxyUnavailable, "load knowledge network proxy mapping")
	}
	if mapping == nil {
		return nil, proxyStateHTTPError(ctx, http.StatusNotFound,
			berrors.BknBackend_KnowledgeNetwork_ProxyMappingNotFound, "knowledge network proxy mapping not found")
	}
	if mapping.LifecycleStatus != interfaces.KNProxyLifecycleActive {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxyDisabled, "knowledge network proxy is not active")
	}
	if mapping.SyncStatus == interfaces.KNProxySyncFailed {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxySyncFailed, "knowledge network proxy synchronization failed")
	}
	if mapping.SyncStatus != interfaces.KNProxySyncReady ||
		mapping.PublishedModelVersion == "" || mapping.SyncedModelVersion != mapping.PublishedModelVersion {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxySyncPending,
			"knowledge network proxy is not synchronized with the current published model")
	}
	sources, modelVersion, err := kns.loadPublishedProxyBindings(ctx, knID, mapping.PublishedModelVersion)
	if err != nil {
		return nil, err
	}
	if !containsProxyBinding(sources, knID, binding) {
		return nil, proxyStateHTTPError(ctx, http.StatusForbidden,
			berrors.BknBackend_KnowledgeNetwork_ProxyBindingInvalid, "target is not a current published binding")
	}
	if mapping.PublishedModelVersion != modelVersion || mapping.SyncedModelVersion != modelVersion {
		return nil, proxyStateHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_KnowledgeNetwork_ProxySyncPending,
			"knowledge network proxy is not synchronized with the current published model")
	}
	return mapping, nil
}

func (kns *knowledgeNetworkService) loadPublishedProxyBindings(ctx context.Context, knID,
	expectedVersion string) ([]interfaces.ProxyGrantSourceSpec, string, error) {
	if cached, ok := kns.proxyBindingCache.Load(knID); ok {
		entry, valid := cached.(publishedProxyBindingCacheEntry)
		if valid && entry.modelVersion == expectedVersion {
			return entry.sources, entry.modelVersion, nil
		}
	}
	latest, err := kns.ExportKNForProjection(ctx, knID)
	if err != nil {
		return nil, "", proxyHTTPError(ctx, http.StatusServiceUnavailable, "load current published proxy bindings")
	}
	sources, modelVersion, err := buildProxyGrantSources(latest)
	if err != nil {
		return nil, "", invalidProxyTargetError(ctx, err)
	}
	if modelVersion != expectedVersion {
		return nil, "", proxyHTTPError(ctx, http.StatusServiceUnavailable,
			"knowledge network proxy is not synchronized with the current published model")
	}
	entry := publishedProxyBindingCacheEntry{modelVersion: modelVersion, sources: sources}
	kns.proxyBindingCache.Store(knID, entry)
	return entry.sources, entry.modelVersion, nil
}

func containsProxyBinding(sources []interfaces.ProxyGrantSourceSpec, knID string,
	binding interfaces.KNProxyBinding) bool {
	values := []string{binding.ChildType, binding.ChildID, binding.TargetType, binding.TargetID, binding.Operation}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "*\r\n") {
			return false
		}
	}
	for _, source := range sources {
		if source.KNID == knID && source.BindingType == binding.ChildType && source.BindingID == binding.ChildID &&
			source.ResourceType == binding.TargetType && source.ResourceID == binding.TargetID &&
			source.Operation == binding.Operation {
			return true
		}
	}
	return false
}

// PlanKNProxySync is a side-effect-free backfill and publication dry run. It
// derives its targets from the latest persisted main model and never accepts
// a caller-supplied proxy or resource target.
func (kns *knowledgeNetworkService) PlanKNProxySync(ctx context.Context, knID string) (*interfaces.KNProxySyncPlan, error) {
	if kns.kpa == nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy orchestration is disabled")
	}
	if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID},
		[]string{interfaces.OPERATION_TYPE_AUTHORIZE}); err != nil {
		return nil, err
	}
	latest, err := kns.ExportKNForProjection(ctx, knID)
	if err != nil {
		return nil, err
	}
	sources, version, err := buildProxyGrantSources(latest)
	if err != nil {
		return nil, invalidProxyTargetError(ctx, err)
	}
	plan := &interfaces.KNProxySyncPlan{KNID: knID, ModelVersion: version, Sources: sources}
	if mapping, getErr := kns.kpa.Get(ctx, knID); getErr != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load knowledge network proxy mapping")
	} else if mapping != nil {
		plan.ProxyAccountID = mapping.ProxyAccountID
	}
	return plan, nil
}

func (kns *knowledgeNetworkService) ReconcileKNProxies(ctx context.Context, requestedBy string) (*interfaces.KNProxyReconcileReport, error) {
	if kns.kpa == nil || kns.mpa == nil || requestedBy == "" {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy reconciliation is unavailable")
	}
	report := &interfaces.KNProxyReconcileReport{
		ConflictingProxy:   map[string][]string{},
		AuthorizationDrift: map[string]interfaces.ProxyGrantReconcileResult{},
		Errors:             map[string]string{},
	}
	knsByID, err := kns.kna.GetAllMainBranchKNs(ctx)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "list knowledge networks for proxy reconciliation")
	}
	mappings, err := kns.kpa.List(ctx)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "list proxy mappings for reconciliation")
	}
	// Reconciliation reports missing mappings from the complete set of live
	// knowledge networks and mutates bkn-safe policy materialization. Authorize
	// every live knowledge network, including those without a mapping, before
	// computing the report or applying any mutation. Otherwise an empty or
	// incomplete mapping table could bypass authorization and disclose KN IDs.
	liveKNIDs := make([]string, 0, len(knsByID))
	for knID, kn := range knsByID {
		if kn == nil || kn.Branch != interfaces.MAIN_BRANCH {
			continue
		}
		liveKNIDs = append(liveKNIDs, knID)
	}
	sort.Strings(liveKNIDs)
	for _, knID := range liveKNIDs {
		if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   knID,
		}, []string{interfaces.OPERATION_TYPE_AUTHORIZE}); err != nil {
			return nil, err
		}
	}
	mappingByKN := make(map[string]*interfaces.KNProxyAccount, len(mappings))
	for _, mapping := range mappings {
		mappingByKN[mapping.KNID] = mapping
		_, networkExists := knsByID[mapping.KNID]
		if !networkExists {
			if mapping.LifecycleStatus != interfaces.KNProxyLifecycleArchived {
				report.OrphanMappings = append(report.OrphanMappings, mapping.KNID)
			}
			continue
		}
		if mapping.LifecycleStatus != interfaces.KNProxyLifecycleActive {
			report.Errors[mapping.KNID] = "knowledge network proxy is not active"
			continue
		}
		result, reconcileErr := kns.mpa.ReconcileGrants(ctx, mapping.ProxyAccountID, requestedBy)
		if reconcileErr != nil {
			report.Errors[mapping.KNID] = "authorization reconciliation failed"
			continue
		}
		if result.PoliciesRestored != 0 || result.PoliciesRemoved != 0 || result.MarkersCreated != 0 ||
			result.MarkersRemoved != 0 || result.UntrackedPolicies != 0 || result.InvalidSources != 0 {
			report.AuthorizationDrift[mapping.KNID] = result
		}
	}
	for knID, kn := range knsByID {
		if kn != nil && kn.Branch == interfaces.MAIN_BRANCH && mappingByKN[knID] == nil {
			report.MissingMappings = append(report.MissingMappings, knID)
		}
	}
	report.ConflictingProxy, err = kns.kpa.ListProxyConflicts(ctx)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "detect conflicting proxy mappings")
	}
	sort.Strings(report.MissingMappings)
	sort.Strings(report.OrphanMappings)
	if len(report.Errors) == 0 {
		report.Errors = nil
	}
	return report, nil
}

func accountIDFromContext(ctx context.Context) string {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return account.ID
}

func proxyHTTPError(ctx context.Context, status int, detail string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, status, berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(detail)
}

func proxyStateHTTPError(ctx context.Context, status int, code, detail string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, status, code).WithErrorDetails(detail)
}

func invalidProxyTargetError(ctx context.Context, cause error) *rest.HTTPError {
	detail := "published model contains an invalid proxy target"
	if cause != nil {
		detail += ": " + cause.Error()
	}
	return proxyHTTPError(ctx, http.StatusBadRequest, detail)
}
