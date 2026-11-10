// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// PublishKNMain runs mutate, which replaces main with srcBranch, in one transaction. When the
// proxy is enabled, the grants are planned from the content of srcBranch and synchronized with
// the commit, the way a whole-network import of that content into main would be. After commit
// the concept index of main is rewritten from the new main.
func (kns *knowledgeNetworkService) PublishKNMain(ctx context.Context, knID, srcBranch string,
	mutate func(context.Context, *sql.Tx) error) error {
	oldMain, err := kns.loadKNForDatasetSnapshot(ctx, knID, interfaces.MAIN_BRANCH)
	if err != nil {
		return err
	}
	candidate, err := kns.loadKNForDatasetSnapshot(ctx, knID, srcBranch)
	if err != nil {
		return err
	}
	rebranch(candidate, interfaces.MAIN_BRANCH)

	if !kns.proxyOrchestrationEnabled(interfaces.MAIN_BRANCH) {
		if err := kns.runInTx(ctx, mutate); err != nil {
			return err
		}
		kns.refreshConceptIndex(ctx, oldMain, candidate)
		return nil
	}

	plan, err := kns.beginProxyPublish(ctx, candidate, true, true)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if plan.createdMapping && !committed {
			kns.abortCreatedProxy(context.WithoutCancel(ctx), plan)
		}
		kns.releaseProxyLock(context.WithoutCancel(ctx), plan)
	}()
	if err := refuseFailedProxyOutboxAppend(ctx, kns.kpoa, plan); err != nil {
		return err
	}
	var bindings []*interfaces.CapabilityBinding
	if kns.cba != nil {
		bindings, err = kns.cba.ListBindings(ctx, interfaces.CapabilityBindingsQueryParams{KNID: knID, Branch: srcBranch})
		if err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "load capability proxy bindings")
		}
	}
	sources, _, err := kns.buildTypedProxyGrantSourcesWithCapabilities(ctx, candidate, bindings)
	if err != nil {
		return invalidProxyTargetError(ctx, err)
	}
	if err := kns.planFullProxyProjection(ctx, plan, sources); err != nil {
		return err
	}

	tx, err := kns.db.BeginTx(ctx, nil)
	if err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, "begin main publication")
	}
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != sql.ErrTxDone {
				otellog.LogError(ctx, "Rollback main publication failed", rollbackErr)
			}
		}
	}()
	if err := mutate(ctx, tx); err != nil {
		return err
	}
	// Policies of definitions main no longer has ride on the outbox event, so the worker removes
	// them after it revoked the proxy grants on them.
	orphans, tracksOrphans := permission.AuthorizationCleanupFrom(ctx)
	if tracksOrphans {
		plan.authorizationCleanups = orphans.PendingResources()
	}
	staged, err := kns.stageProxyDelta(ctx, tx, plan)
	if err != nil {
		return err
	}
	if !staged {
		if err := kns.markProxyPending(ctx, tx, plan); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, "commit main publication")
	}
	committed = true
	kns.refreshConceptIndex(ctx, oldMain, candidate)
	if staged {
		if tracksOrphans {
			// The event carries them now; the owner of the tracker must not remove them first.
			orphans.Discard()
		}
		plan.grants.logSkipped(ctx, plan.mapping.KNID)
		return nil
	}
	return kns.finishProxyDeltaPublish(ctx, plan)
}

// ReplaceKNDraft runs mutate, which replaces the draft with a copy of another branch or deletes
// it, in one transaction, and rewrites the concept index of the draft after commit.
func (kns *knowledgeNetworkService) ReplaceKNDraft(ctx context.Context, knID string,
	mutate func(context.Context, *sql.Tx) error) error {
	oldDraft, err := kns.loadDraftIfAny(ctx, knID)
	if err != nil {
		return err
	}
	if err := kns.runInTx(ctx, mutate); err != nil {
		return err
	}
	newDraft, err := kns.loadDraftIfAny(context.WithoutCancel(ctx), knID)
	if err != nil {
		otellog.LogError(ctx, "Load the draft for its concept index failed", err)
		return nil
	}
	kns.refreshConceptIndex(ctx, oldDraft, newDraft)
	return nil
}

// loadDraftIfAny loads the draft, or returns nil when the network has none.
func (kns *knowledgeNetworkService) loadDraftIfAny(ctx context.Context, knID string) (*interfaces.KN, error) {
	draft, err := kns.loadKNForDatasetSnapshot(ctx, knID, interfaces.DRAFT_BRANCH)
	var httpErr *httperrors.HTTPError
	if err != nil && errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusNotFound {
		return nil, nil
	}
	return draft, err
}

// planFullProxyProjection records in plan the grants of the complete desired source set.
func (kns *knowledgeNetworkService) planFullProxyProjection(ctx context.Context, plan *proxyPublishPlan,
	sources []interfaces.ProxyGrantSourceSpec) error {
	if kns.kpoa != nil {
		delta, err := kns.buildFullProxyDelta(ctx, plan, sources)
		if err != nil {
			return err
		}
		selection, err := kns.preflightProxyDelta(ctx, plan.mapping.ProxyAccountID, plan.delegatorID, delta)
		if err != nil {
			return err
		}
		if err := finalizeProxyGrantDelta(delta, selection); err != nil {
			return proxyHTTPError(ctx, http.StatusServiceUnavailable, "finalize full proxy grant delta")
		}
		plan.delta = delta
		plan.grants = selection
		plan.resolvedSources = selection.resolved
		plan.modelVersion = delta.targetVersion
		return nil
	}
	grants, err := kns.preflightProxySources(ctx, plan.mapping.ProxyAccountID, plan.delegatorID, sources)
	if err != nil {
		return err
	}
	plan.grants = grants
	plan.resolvedSources = grants.resolved
	plan.modelVersion, err = proxyGrantSnapshotVersion(grants.materialized(sources))
	if err != nil {
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "hash proxy grant snapshot")
	}
	return nil
}

func (kns *knowledgeNetworkService) runInTx(ctx context.Context, mutate func(context.Context, *sql.Tx) error) error {
	tx, err := kns.db.BeginTx(ctx, nil)
	if err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, "begin transaction")
	}
	if err := mutate(ctx, tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != sql.ErrTxDone {
			otellog.LogError(ctx, "Rollback transaction failed", rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, "commit transaction")
	}
	return nil
}

// rebranch moves a loaded snapshot onto branch, so that index documents and grant sources
// built from it name that branch.
func rebranch(kn *interfaces.KN, branch string) {
	kn.Branch = branch
	for _, item := range kn.ObjectTypes {
		item.Branch = branch
	}
	for _, item := range kn.RelationTypes {
		item.Branch = branch
	}
	for _, item := range kn.ActionTypes {
		item.Branch = branch
	}
	for _, item := range kn.ConceptGroups {
		item.Branch = branch
	}
	for _, item := range kn.RiskTypes {
		item.Branch = branch
	}
}

// refreshConceptIndex rewrites the concept index of a branch after its content was replaced
// by a copy: documents of definitions no longer on the branch are deleted, the others written
// again. Like the index writes of an import, it runs after commit, and a failure is only logged.
// oldKN is nil when the branch was empty, newKN when it no longer exists; both are on one branch.
func (kns *knowledgeNetworkService) refreshConceptIndex(ctx context.Context, oldKN, newKN *interfaces.KN) {
	ctx = context.WithoutCancel(ctx)
	kns.deleteStaleConceptDocuments(ctx, oldKN, newKN)
	if newKN == nil {
		return
	}
	steps := []struct {
		name string
		run  func() error
	}{
		{"concept groups", func() error { return kns.cgs.InsertDatasetDatas(ctx, newKN.ConceptGroups) }},
		{"object types", func() error { return kns.ots.InsertDatasetData(ctx, newKN.ObjectTypes) }},
		{"relation types", func() error { return kns.rts.InsertDatasetData(ctx, newKN.RelationTypes) }},
		{"action types", func() error { return kns.ats.InsertDatasetData(ctx, newKN.ActionTypes) }},
		{"risk types", func() error { return kns.riskTypeS.InsertDatasetData(ctx, newKN.RiskTypes) }},
		{"network", func() error { return kns.InsertDatasetData(ctx, newKN) }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Write the concept index of %s on %s failed", step.name, newKN.Branch), err)
		}
	}
}

// deleteStaleConceptDocuments deletes the concept index documents of the definitions oldKN has
// and newKN no longer has. newKN nil means the branch is gone, its network document too.
func (kns *knowledgeNetworkService) deleteStaleConceptDocuments(ctx context.Context, oldKN, newKN *interfaces.KN) {
	ids := func(kn *interfaces.KN) map[string]struct{} {
		set := map[string]struct{}{}
		if kn == nil {
			return set
		}
		add := func(moduleType, id string) {
			set[interfaces.GenerateConceptDocuemtnID(kn.KNID, moduleType, id, kn.Branch)] = struct{}{}
		}
		for _, item := range kn.ConceptGroups {
			add(interfaces.MODULE_TYPE_CONCEPT_GROUP, item.CGID)
		}
		for _, item := range kn.ObjectTypes {
			add(interfaces.MODULE_TYPE_OBJECT_TYPE, item.OTID)
		}
		for _, item := range kn.RelationTypes {
			add(interfaces.MODULE_TYPE_RELATION_TYPE, item.RTID)
		}
		for _, item := range kn.ActionTypes {
			add(interfaces.MODULE_TYPE_ACTION_TYPE, item.ATID)
		}
		for _, item := range kn.RiskTypes {
			add(interfaces.MODULE_TYPE_RISK_TYPE, item.RTID)
		}
		if newKN == nil {
			add(interfaces.MODULE_TYPE_KN, kn.KNID)
		}
		return set
	}
	keep := ids(newKN)
	for id := range ids(oldKN) {
		if _, ok := keep[id]; ok {
			continue
		}
		if err := kns.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, id); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Delete concept index document %s failed", id), err)
		}
	}
}

// initVersionedNetwork completes a network created versioned from the start: what the request
// defined is on the draft, main gets only the network row, and nothing is published yet.
func initVersionedNetwork(ctx context.Context, tx *sql.Tx, kn *interfaces.KN) error {
	if logics.BC == nil || logics.KNVA == nil {
		return proxyHTTPError(ctx, http.StatusNotImplemented, "knowledge network versioning is unavailable")
	}
	if err := logics.BC.CopyNetworkRow(ctx, tx, kn.KNID, interfaces.DRAFT_BRANCH, interfaces.MAIN_BRANCH); err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	now := time.Now().UnixMilli()
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if err := logics.KNVA.CreateState(ctx, tx, &interfaces.KNVersionState{
		KNID: kn.KNID, Mode: interfaces.KNVersioningEnabled, NextVersion: 1, DraftIndexRevision: -1,
		EnabledBy: account.ID, EnabledByType: account.Type, EnableTime: now, UpdateTime: now,
	}); err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return nil
}

// pruneReplacedDefinitions completes a whole-package replace: after the package overwrote the
// branch, the definitions it leaves out are deleted. The package was validated as a whole, so
// nothing it keeps refers to them. Capability bindings are replaced with the capabilities.
func pruneReplacedDefinitions(ctx context.Context, tx *sql.Tx, kn *interfaces.KN) error {
	if logics.BC == nil {
		return proxyHTTPError(ctx, http.StatusNotImplemented, "replace import is unavailable")
	}
	keep := map[string][]string{
		interfaces.MODULE_TYPE_OBJECT_TYPE:   {},
		interfaces.MODULE_TYPE_RELATION_TYPE: {},
		interfaces.MODULE_TYPE_ACTION_TYPE:   {},
		interfaces.MODULE_TYPE_CONCEPT_GROUP: {},
		interfaces.MODULE_TYPE_RISK_TYPE:     {},
		interfaces.MODULE_TYPE_VALUE_TYPE:    {},
	}
	add := func(kind, id string) { keep[kind] = append(keep[kind], id) }
	for _, item := range kn.ObjectTypes {
		add(interfaces.MODULE_TYPE_OBJECT_TYPE, item.OTID)
	}
	for _, item := range kn.RelationTypes {
		add(interfaces.MODULE_TYPE_RELATION_TYPE, item.RTID)
	}
	for _, item := range kn.ActionTypes {
		add(interfaces.MODULE_TYPE_ACTION_TYPE, item.ATID)
	}
	for _, item := range kn.ConceptGroups {
		add(interfaces.MODULE_TYPE_CONCEPT_GROUP, item.CGID)
	}
	for _, item := range kn.RiskTypes {
		add(interfaces.MODULE_TYPE_RISK_TYPE, item.RTID)
	}
	for _, item := range kn.ValueTypes {
		add(interfaces.MODULE_TYPE_VALUE_TYPE, item.VTID)
	}
	if err := logics.BC.PruneBranch(ctx, tx, kn.KNID, kn.Branch, keep); err != nil {
		return proxyHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return nil
}

// IndexVersion writes the concept index of version branch dst from the content of src before the
// version goes live. The index of a version is not written again afterwards, so a search of it
// reads one whole index; documents left from an earlier attempt at the same number are dropped.
func (kns *knowledgeNetworkService) IndexVersion(ctx context.Context, knID, src, dst string) error {
	kn, err := kns.loadKNForDatasetSnapshot(ctx, knID, src)
	if err != nil {
		return err
	}
	if err := kns.DeleteBranchIndex(ctx, knID, dst); err != nil {
		return err
	}
	rebranch(kn, dst)
	for _, write := range []func() error{
		func() error { return kns.cgs.InsertDatasetDatas(ctx, kn.ConceptGroups) },
		func() error { return kns.ots.InsertDatasetData(ctx, kn.ObjectTypes) },
		func() error { return kns.rts.InsertDatasetData(ctx, kn.RelationTypes) },
		func() error { return kns.ats.InsertDatasetData(ctx, kn.ActionTypes) },
		func() error { return kns.riskTypeS.InsertDatasetData(ctx, kn.RiskTypes) },
		func() error { return kns.InsertDatasetData(ctx, kn) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteBranchIndex deletes every concept index document of one branch.
func (kns *knowledgeNetworkService) DeleteBranchIndex(ctx context.Context, knID, branch string) error {
	docID := interfaces.GenerateConceptDocuemtnID(knID, interfaces.MODULE_TYPE_KN, knID, branch)
	if err := kns.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docID); err != nil {
		return err
	}
	return kns.vbs.DeleteDatasetDocumentsByQuery(ctx, interfaces.BKN_DATASET_ID, map[string]any{
		"operation": "and",
		"sub_conditions": []map[string]any{
			{"field": "kn_id", "operation": "==", "value": knID, "value_from": "const"},
			{"field": "branch", "operation": "==", "value": branch, "value_from": "const"},
		},
	})
}
