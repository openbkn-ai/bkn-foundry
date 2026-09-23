// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

type proxyGrantDelta struct {
	bindings      []interfaces.KNProxyBindingRef
	old           []interfaces.ProxyGrantSourceSpec
	desired       []interfaces.ProxyGrantSourceSpec
	upserts       []interfaces.ProxyGrantSourceSpec
	removals      []interfaces.ProxyGrantSourceSpec
	baseVersion   string
	targetVersion string
}

// prepareProxyChildDelta builds a bounded projection for an established ready
// proxy. New mappings, failed retries, and ambiguous tombstone cascades keep
// using the full reconciliation path.
func (kns *knowledgeNetworkService) prepareProxyChildDelta(ctx context.Context,
	plan *proxyPublishPlan, changes *interfaces.KN, mergeMode string) (*proxyGrantDelta, error) {
	if !proxyMappingSupportsDelta(plan) ||
		proxyChangesRequireFullProjection(changes, mergeMode) {
		return nil, nil
	}
	current, bindings, err := kns.loadAffectedProxyModel(ctx, changes)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, nil
	}
	candidate := mergeProxyMutationChanges(current, changes, mergeMode)
	desired, _, err := kns.buildTypedProxyGrantSourcesWithCapabilities(ctx, candidate,
		[]*interfaces.CapabilityBinding{})
	if err != nil {
		return nil, invalidProxyTargetError(ctx, err)
	}
	old, err := kns.kpa.ListPublishedSources(ctx, changes.KNID, bindings)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load affected proxy grant snapshot")
	}
	upserts, additions, removals := diffProxyGrantSources(old, desired)
	targetVersion, err := proxyGrantTransitionVersion(plan.mapping.PublishedModelVersion, additions, removals)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "hash proxy grant transition")
	}
	return &proxyGrantDelta{
		bindings: bindings, old: old, desired: desired, upserts: upserts, removals: removals,
		baseVersion: plan.mapping.PublishedModelVersion, targetVersion: targetVersion,
	}, nil
}

func proxyMappingSupportsDelta(plan *proxyPublishPlan) bool {
	return plan != nil && plan.mapping != nil &&
		plan.mapping.SyncStatus == interfaces.KNProxySyncReady &&
		plan.mapping.PublishedModelVersion != "" &&
		plan.mapping.PublishedModelVersion == plan.mapping.SyncedModelVersion
}

func (kns *knowledgeNetworkService) prepareProxyCapabilityDelta(ctx context.Context,
	plan *proxyPublishPlan, result *interfaces.KNCapabilityMutationResult,
	removedBindingIDs []string) (*proxyGrantDelta, error) {
	if !proxyMappingSupportsDelta(plan) {
		return nil, nil
	}
	removed := make(map[string]bool, len(removedBindingIDs))
	ids := make(map[string]bool, len(removedBindingIDs)+len(result.Bindings))
	for _, id := range removedBindingIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			removed[id], ids[id] = true, true
		}
	}
	desiredBindings := make([]*interfaces.CapabilityBinding, 0, len(result.Bindings))
	for _, binding := range result.Bindings {
		if binding == nil || strings.TrimSpace(binding.ID) == "" {
			continue
		}
		ids[binding.ID] = true
		if !removed[binding.ID] {
			desiredBindings = append(desiredBindings, binding)
		}
	}
	if len(ids) == 0 {
		return &proxyGrantDelta{
			baseVersion:   plan.mapping.PublishedModelVersion,
			targetVersion: plan.mapping.PublishedModelVersion,
		}, nil
	}
	bindings := make([]interfaces.KNProxyBindingRef, 0, len(ids))
	for _, id := range sortedProxyIDs(ids) {
		bindings = append(bindings, interfaces.KNProxyBindingRef{
			BindingType: interfaces.KNProxyBindingTypeCapability, BindingID: id,
		})
	}
	desired, _, err := kns.buildTypedProxyGrantSourcesWithCapabilities(ctx, &interfaces.KN{
		KNID: plan.mapping.KNID, Branch: interfaces.MAIN_BRANCH,
	}, desiredBindings)
	if err != nil {
		return nil, invalidProxyTargetError(ctx, err)
	}
	old, err := kns.kpa.ListPublishedSources(ctx, plan.mapping.KNID, bindings)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load affected capability proxy snapshot")
	}
	upserts, additions, removals := diffProxyGrantSources(old, desired)
	targetVersion, err := proxyGrantTransitionVersion(plan.mapping.PublishedModelVersion, additions, removals)
	if err != nil {
		return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "hash capability proxy transition")
	}
	return &proxyGrantDelta{
		bindings: bindings, old: old, desired: desired, upserts: upserts, removals: removals,
		baseVersion: plan.mapping.PublishedModelVersion, targetVersion: targetVersion,
	}, nil
}

func (kns *knowledgeNetworkService) loadAffectedProxyModel(ctx context.Context,
	changes *interfaces.KN) (*interfaces.KN, []interfaces.KNProxyBindingRef, error) {
	knID, branch := changes.KNID, changes.Branch
	objectIDs := map[string]bool{}
	changedObjectIDs := map[string]bool{}
	relationIDs := map[string]bool{}
	actionIDs := map[string]bool{}
	metricIDs := map[string]bool{}
	for _, item := range changes.ObjectTypes {
		if item != nil && strings.TrimSpace(item.OTID) != "" {
			objectIDs[item.OTID] = true
			changedObjectIDs[item.OTID] = true
		}
	}
	for _, item := range changes.RelationTypes {
		if item != nil && strings.TrimSpace(item.RTID) != "" {
			relationIDs[item.RTID] = true
			if item.SourceObjectTypeID != "" {
				objectIDs[item.SourceObjectTypeID] = true
			}
			if item.TargetObjectTypeID != "" {
				objectIDs[item.TargetObjectTypeID] = true
			}
		}
	}
	for _, item := range changes.ActionTypes {
		if item != nil && strings.TrimSpace(item.ATID) != "" {
			actionIDs[item.ATID] = true
		}
	}
	for _, item := range changes.Metrics {
		if item != nil && strings.TrimSpace(item.ID) != "" {
			metricIDs[item.ID] = true
			if item.ScopeRef != "" {
				objectIDs[item.ScopeRef] = true
			}
		}
	}

	var err error
	currentRelations := []*interfaces.RelationType{}
	loadedRelationIDs := sortedProxyIDs(relationIDs)
	if len(relationIDs) > 0 {
		currentRelations, err = kns.rta.GetRelationTypesByIDs(ctx, knID, branch, loadedRelationIDs)
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load changed relation proxy bindings")
		}
	}
	for _, relation := range currentRelations {
		if relation == nil {
			continue
		}
		if relation.SourceObjectTypeID != "" {
			objectIDs[relation.SourceObjectTypeID] = true
		}
		if relation.TargetObjectTypeID != "" {
			objectIDs[relation.TargetObjectTypeID] = true
		}
	}
	currentMetrics := []*interfaces.MetricDefinition{}
	loadedMetricIDs := sortedProxyIDs(metricIDs)
	if len(metricIDs) > 0 {
		currentMetrics, err = kns.ma.GetMetricsByIDs(ctx, knID, branch, loadedMetricIDs)
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load changed metric proxy bindings")
		}
		for _, metric := range currentMetrics {
			if metric != nil && metric.ScopeRef != "" {
				objectIDs[metric.ScopeRef] = true
			}
		}
	}
	changedObjectIDList := sortedProxyIDs(changedObjectIDs)
	if len(changedObjectIDList) > 0 {
		referencing, err := kns.rta.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
			KNID:                      knID, Branch: branch, BoundObjectTypeIDs: changedObjectIDList,
		})
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load relation proxy dependants")
		}
		for _, relation := range referencing {
			if relation == nil {
				continue
			}
			relationIDs[relation.RTID] = true
			if relation.SourceObjectTypeID != "" {
				objectIDs[relation.SourceObjectTypeID] = true
			}
			if relation.TargetObjectTypeID != "" {
				objectIDs[relation.TargetObjectTypeID] = true
			}
		}
		metrics, err := kns.ma.ListMetrics(ctx, interfaces.MetricsListQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
			KNID:                      knID, Branch: branch, ScopeRefs: changedObjectIDList,
		})
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load metric proxy dependants")
		}
		for _, metric := range metrics {
			if metric != nil {
				metricIDs[metric.ID] = true
			}
		}
	}

	objects := []*interfaces.ObjectType{}
	if len(objectIDs) > 0 {
		objects, err = kns.ota.GetObjectTypesByIDs(ctx, nil, knID, branch, sortedProxyIDs(objectIDs))
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load affected object proxy bindings")
		}
	}
	relations := currentRelations
	affectedRelationIDs := sortedProxyIDs(relationIDs)
	if len(affectedRelationIDs) > 0 && !slices.Equal(loadedRelationIDs, affectedRelationIDs) {
		relations, err = kns.rta.GetRelationTypesByIDs(ctx, knID, branch, affectedRelationIDs)
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load affected relation proxy bindings")
		}
	}
	actions := []*interfaces.ActionType{}
	if len(actionIDs) > 0 {
		actions, err = kns.ata.GetActionTypesByIDs(ctx, knID, branch, sortedProxyIDs(actionIDs))
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load affected action proxy bindings")
		}
	}
	metrics := currentMetrics
	affectedMetricIDs := sortedProxyIDs(metricIDs)
	if len(affectedMetricIDs) > 0 && !slices.Equal(loadedMetricIDs, affectedMetricIDs) {
		metrics, err = kns.ma.GetMetricsByIDs(ctx, knID, branch, affectedMetricIDs)
		if err != nil {
			return nil, nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "load affected metric proxy bindings")
		}
	}
	current := &interfaces.KN{
		KNID: knID, Branch: branch, ObjectTypes: objects, RelationTypes: relations,
		ActionTypes: actions, Metrics: metrics,
	}
	logicPropertyIDs := map[string]bool{}
	for _, objectType := range append(append([]*interfaces.ObjectType(nil), objects...), changes.ObjectTypes...) {
		if objectType == nil || strings.TrimSpace(objectType.OTID) == "" {
			continue
		}
		for _, property := range objectType.LogicProperties {
			if property == nil || strings.TrimSpace(property.Name) == "" {
				continue
			}
			logicPropertyIDs[stableProxySourceID(knID, "logic_property",
				strings.Join([]string{objectType.OTID, property.Name}, "\x00"))] = true
		}
	}
	bindings := make([]interfaces.KNProxyBindingRef, 0,
		len(objectIDs)+len(logicPropertyIDs)+len(relationIDs)+len(actionIDs)+len(metricIDs))
	appendBindings := func(bindingType string, ids map[string]bool) {
		for _, id := range sortedProxyIDs(ids) {
			bindings = append(bindings, interfaces.KNProxyBindingRef{BindingType: bindingType, BindingID: id})
		}
	}
	appendBindings(interfaces.MODULE_TYPE_OBJECT_TYPE, objectIDs)
	appendBindings("logic_property", logicPropertyIDs)
	appendBindings(interfaces.MODULE_TYPE_RELATION_TYPE, relationIDs)
	appendBindings(interfaces.MODULE_TYPE_ACTION_TYPE, actionIDs)
	appendBindings(interfaces.MODULE_TYPE_METRIC, metricIDs)
	return current, bindings, nil
}

func proxyChangesRequireFullProjection(changes *interfaces.KN, mergeMode string) bool {
	if changes == nil || mergeMode != interfaces.ImportMode_Overwrite {
		return false
	}
	for _, item := range changes.ObjectTypes {
		if item != nil && item.OTID != "" && item.OTName == "" && item.DataSource == nil &&
			len(item.DataProperties) == 0 && len(item.LogicProperties) == 0 {
			return true
		}
	}
	for _, item := range changes.RelationTypes {
		if item != nil && item.RTID != "" && item.RTName == "" && item.SourceObjectTypeID == "" &&
			item.TargetObjectTypeID == "" && item.Type == "" && item.MappingRules == nil {
			return true
		}
	}
	for _, item := range changes.ActionTypes {
		if item != nil && item.ATID != "" && item.ATName == "" && item.ActionSource.Type == "" {
			return true
		}
	}
	for _, item := range changes.Metrics {
		if item != nil && item.ID != "" && item.Name == "" && item.ScopeType == "" && item.ScopeRef == "" {
			return true
		}
	}
	return false
}

func sortedProxyIDs(values map[string]bool) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func diffProxyGrantSources(old, desired []interfaces.ProxyGrantSourceSpec) (
	[]interfaces.ProxyGrantSourceSpec, []interfaces.ProxyGrantSourceSpec, []interfaces.ProxyGrantSourceSpec) {
	oldByKey := make(map[string]interfaces.ProxyGrantSourceSpec, len(old))
	for _, source := range old {
		oldByKey[proxySourceResolutionKey(source)] = source
	}
	desiredByKey := make(map[string]interfaces.ProxyGrantSourceSpec, len(desired))
	for _, source := range desired {
		desiredByKey[proxySourceResolutionKey(source)] = source
	}
	// Every desired affected source is an upsert. Retained entries are touched
	// deliberately so Safe revalidates their historical delegator.
	upserts := append([]interfaces.ProxyGrantSourceSpec(nil), desired...)
	additions := make([]interfaces.ProxyGrantSourceSpec, 0)
	for key, source := range desiredByKey {
		if _, exists := oldByKey[key]; !exists {
			additions = append(additions, source)
		}
	}
	removals := make([]interfaces.ProxyGrantSourceSpec, 0)
	for key, source := range oldByKey {
		if _, keep := desiredByKey[key]; !keep {
			removals = append(removals, source)
		}
	}
	sortProxyGrantSources(upserts)
	sortProxyGrantSources(additions)
	sortProxyGrantSources(removals)
	return upserts, additions, removals
}

func sortProxyGrantSources(sources []interfaces.ProxyGrantSourceSpec) {
	sort.Slice(sources, func(i, j int) bool {
		return proxySourceResolutionKey(sources[i]) < proxySourceResolutionKey(sources[j])
	})
}

func proxyGrantTransitionVersion(base string, additions,
	removals []interfaces.ProxyGrantSourceSpec) (string, error) {
	if len(additions) == 0 && len(removals) == 0 {
		return base, nil
	}
	added := append([]interfaces.ProxyGrantSourceSpec(nil), additions...)
	removed := append([]interfaces.ProxyGrantSourceSpec(nil), removals...)
	sortProxyGrantSources(added)
	sortProxyGrantSources(removed)
	canonical, err := json.Marshal(struct {
		Base     string                            `json:"base"`
		Upserts  []interfaces.ProxyGrantSourceSpec `json:"upserts"`
		Removals []interfaces.ProxyGrantSourceSpec `json:"removals"`
	}{Base: base, Upserts: added, Removals: removed})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (kns *knowledgeNetworkService) preflightProxyDelta(ctx context.Context, proxyID, delegatorID string,
	delta *proxyGrantDelta) (*proxyGrantSelection, error) {
	if delta == nil {
		return newProxyGrantSelection(nil), nil
	}
	selection := newProxyGrantSelection(delta.upserts)
	if len(delta.upserts) == 0 {
		return selection, nil
	}
	checked := delta.upserts
	result, err := kns.mpa.CheckGrantDelta(ctx, proxyID, delegatorID, checked, delta.removals)
	if err != nil {
		required, optional := splitBestEffortProxySources(delta.upserts)
		if len(optional) == 0 || !isManagedProxyRequestRejected(err) {
			return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy permission delta preflight failed")
		}
		checked = required
		if len(checked) == 0 {
			result = interfaces.ProxyGrantBatchCheckResult{}
		} else if result, err = kns.mpa.CheckGrantDelta(ctx, proxyID, delegatorID,
			checked, delta.removals); err != nil {
			return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable, "proxy permission delta preflight failed")
		}
		for _, source := range optional {
			selection.skip(source, proxySkipReasonUnsupported)
		}
	}
	missing := make([]missingProxyPermission, 0, len(result.DeniedSources))
	for _, source := range result.DeniedSources {
		if interfaces.IsBestEffortProxyGrantSource(source) {
			selection.skip(source, proxySkipReasonDelegatorDenied)
			continue
		}
		missing = append(missing, missingProxyPermission{
			ResourceType: source.ResourceType, ResourceID: source.ResourceID, Operation: source.Operation,
			BindingType: source.BindingType, BindingID: source.BindingID,
		})
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool {
			return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", missing[i].ResourceType,
				missing[i].ResourceID, missing[i].Operation, missing[i].BindingType, missing[i].BindingID) <
				fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", missing[j].ResourceType,
					missing[j].ResourceID, missing[j].Operation, missing[j].BindingType, missing[j].BindingID)
		})
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden,
			berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing).
			WithErrorDetails(missingProxyPermissionDetails{MissingPermissions: missing})
	}
	resolved := make(map[string]bool, len(result.ResolvedSources))
	for _, source := range result.ResolvedSources {
		if source.GrantedBy != "" {
			resolved[proxySourceResolutionKey(source.ProxyGrantSourceSpec)] = true
		}
	}
	for _, source := range checked {
		if selection.skipped(source) {
			continue
		}
		if !resolved[proxySourceResolutionKey(source)] {
			return nil, proxyHTTPError(ctx, http.StatusServiceUnavailable,
				"proxy delta preflight response omitted an effective delegator")
		}
	}
	selection.resolved = result.ResolvedSources
	return selection, nil
}

func finalizeProxyGrantDelta(delta *proxyGrantDelta, selection *proxyGrantSelection) error {
	if delta == nil {
		return nil
	}
	delta.desired = selection.materialized(delta.desired)
	var additions []interfaces.ProxyGrantSourceSpec
	delta.upserts, additions, delta.removals = diffProxyGrantSources(delta.old, delta.desired)
	targetVersion, err := proxyGrantTransitionVersion(delta.baseVersion, additions, delta.removals)
	if err != nil {
		return err
	}
	delta.targetVersion = targetVersion
	return nil
}

func (kns *knowledgeNetworkService) finishProxyDeltaPublish(ctx context.Context,
	plan *proxyPublishPlan) error {
	if plan == nil || plan.delta == nil {
		return kns.finishProxyPublish(ctx, plan)
	}
	if err := kns.renewProxyLock(ctx, plan); err != nil {
		kns.recordProxySyncFailure(ctx, plan, err)
		return err
	}
	delta := plan.delta
	plan.grants.logSkipped(ctx, plan.mapping.KNID)
	if _, err := kns.mpa.SyncGrantDelta(ctx, plan.mapping.ProxyAccountID, plan.delegatorID,
		plan.syncGeneration, delta.baseVersion, delta.targetVersion, delta.upserts, delta.removals); err != nil {
		kns.recordProxySyncFailure(ctx, plan, err)
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "synchronize affected proxy permissions")
	}
	if err := kns.kpa.ReplacePublishedBindingsAndMarkReady(ctx, plan.mapping.KNID, plan.syncGeneration,
		plan.lockOwner, delta.targetVersion, delta.bindings, delta.desired, time.Now().UnixMilli()); err != nil {
		kns.recordProxySyncFailure(ctx, plan, err)
		return proxyHTTPError(ctx, http.StatusServiceUnavailable, "record affected proxy synchronization result")
	}
	otellog.LogInfo(ctx, fmt.Sprintf("Incremental proxy publication updated %d bindings (%d upserts, %d removals)",
		len(delta.bindings), len(delta.upserts), len(delta.removals)))
	return nil
}
