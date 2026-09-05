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
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

const (
	proxyLockLease = 5 * time.Minute
	proxyLockWait  = 30 * time.Second
)

type proxyPublishPlan struct {
	mapping        *interfaces.KNProxyAccount
	grantorID      string
	modelVersion   string
	lockOwner      string
	createdMapping bool
}

func (kns *knowledgeNetworkService) proxyOrchestrationEnabled(branch string) bool {
	return branch == interfaces.MAIN_BRANCH && kns.kpa != nil && kns.mpa != nil
}

func (kns *knowledgeNetworkService) prepareProxyPublish(ctx context.Context, kn *interfaces.KN) (*proxyPublishPlan, error) {
	if !kns.proxyOrchestrationEnabled(kn.Branch) {
		return nil, nil
	}
	grantorID := accountIDFromContext(ctx)
	if grantorID == "" {
		return nil, proxyHTTPError(ctx, http.StatusForbidden, "proxy grantor identity is unavailable")
	}

	mapping, err := kns.kpa.Get(ctx, kn.KNID)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load knowledge network proxy mapping")
	}
	createdMapping := false
	if mapping == nil {
		mapping, createdMapping, err = kns.createProxyMapping(ctx, kn)
		if err != nil {
			return nil, err
		}
	}
	if mapping.LifecycleStatus != interfaces.KNProxyLifecycleActive {
		return nil, proxyHTTPError(ctx, http.StatusConflict, "knowledge network proxy is not active")
	}

	lockOwner := uuid.NewString()
	if err := kns.acquireProxyLock(ctx, kn.KNID, lockOwner); err != nil {
		return nil, err
	}
	plan := &proxyPublishPlan{mapping: mapping, grantorID: grantorID, lockOwner: lockOwner, createdMapping: createdMapping}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			kns.abortCreatedProxy(context.WithoutCancel(ctx), plan)
			kns.releaseProxyLock(context.WithoutCancel(ctx), plan)
		}
	}()

	sources, version, err := buildProxyGrantSources(kn)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusBadRequest, "published model contains an invalid proxy target")
	}
	if err := kns.preflightProxySources(ctx, mapping.ProxyAccountID, grantorID, sources); err != nil {
		return nil, err
	}
	plan.modelVersion = version
	releaseOnError = false
	return plan, nil
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

func (kns *knowledgeNetworkService) preflightProxySources(ctx context.Context, proxyID, grantorID string,
	sources []interfaces.ProxyGrantSourceSpec) error {
	for _, source := range sources {
		result, err := kns.mpa.CheckGrant(ctx, proxyID, grantorID, source)
		if err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy permission preflight failed")
		}
		if !result.Allowed {
			return proxyHTTPError(ctx, http.StatusForbidden, "proxy permission preflight denied")
		}
	}
	return nil
}

func (kns *knowledgeNetworkService) markProxyPending(ctx context.Context, tx *sql.Tx, plan *proxyPublishPlan) error {
	if plan == nil {
		return nil
	}
	if err := kns.kpa.SetPending(ctx, tx, plan.mapping.KNID, plan.modelVersion, plan.grantorID, time.Now().UnixMilli()); err != nil {
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
	if _, err := kns.mpa.SyncGrants(ctx, plan.mapping.ProxyAccountID, plan.grantorID, sources); err != nil {
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
	plan := &proxyPublishPlan{mapping: mapping, grantorID: grantorID, lockOwner: uuid.NewString(), modelVersion: mapping.PublishedModelVersion}
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
	if _, err := kns.mpa.SyncGrants(ctx, plan.mapping.ProxyAccountID, plan.grantorID,
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
		[]string{interfaces.OPERATION_TYPE_AUTHORIZE}); err != nil {
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
		return nil, proxyHTTPError(ctx, http.StatusBadRequest, "published model contains an invalid proxy target")
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
	// Reconciliation mutates bkn-safe policy materialization. Authorize every
	// live knowledge network before applying any mutation so the operation is
	// all-or-nothing with respect to the caller's governance scope.
	for _, mapping := range mappings {
		if _, exists := knsByID[mapping.KNID]; !exists {
			continue
		}
		if err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   mapping.KNID,
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
			result.MarkersRemoved != 0 || result.UntrackedPolicies != 0 {
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
