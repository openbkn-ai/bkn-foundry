// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
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
		kns.refreshMainConceptIndex(ctx, oldMain, candidate)
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
	kns.refreshMainConceptIndex(ctx, oldMain, candidate)
	if staged {
		plan.grants.logSkipped(ctx, plan.mapping.KNID)
		return nil
	}
	return kns.finishProxyDeltaPublish(ctx, plan)
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

// refreshMainConceptIndex rewrites the concept index of main after its content was replaced:
// documents of definitions no longer on main are deleted, the others written again. Like the
// index writes of an import, it runs after commit, and a failure is only logged.
func (kns *knowledgeNetworkService) refreshMainConceptIndex(ctx context.Context, oldMain, newMain *interfaces.KN) {
	ctx = context.WithoutCancel(ctx)
	ids := func(kn *interfaces.KN) map[string]struct{} {
		set := map[string]struct{}{}
		add := func(moduleType, id string) {
			set[interfaces.GenerateConceptDocuemtnID(kn.KNID, moduleType, id, interfaces.MAIN_BRANCH)] = struct{}{}
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
		return set
	}
	keep := ids(newMain)
	for id := range ids(oldMain) {
		if _, ok := keep[id]; ok {
			continue
		}
		if err := kns.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, id); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Delete concept index document %s of main failed", id), err)
		}
	}
	steps := []struct {
		name string
		run  func() error
	}{
		{"concept groups", func() error { return kns.cgs.InsertDatasetDatas(ctx, newMain.ConceptGroups) }},
		{"object types", func() error { return kns.ots.InsertDatasetData(ctx, newMain.ObjectTypes) }},
		{"relation types", func() error { return kns.rts.InsertDatasetData(ctx, newMain.RelationTypes) }},
		{"action types", func() error { return kns.ats.InsertDatasetData(ctx, newMain.ActionTypes) }},
		{"risk types", func() error { return kns.riskTypeS.InsertDatasetData(ctx, newMain.RiskTypes) }},
		{"network", func() error { return kns.InsertDatasetData(ctx, newMain) }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Write the concept index of main %s failed", step.name), err)
		}
	}
}
