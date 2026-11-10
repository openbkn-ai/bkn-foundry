// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	propertyaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/property_access"
)

func (s *queryAuthorizationService) AuthorizeObjectMetricExecutionWithFingerprint(ctx context.Context,
	knID, branch string, executionContext *interfaces.ObjectMetricExecutionContextV1,
	query *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricAuthorizationFingerprint, error) {
	if err := s.AuthorizeObjectMetricExecution(ctx, knID, branch, executionContext, query); err != nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{}, err
	}
	inputs := make([]objectMetricAuthorizationInput, 0, len(executionContext.Dependencies)+1)
	inputs = append(inputs, objectMetricAuthorizationInput{definition: &executionContext.Root.Definition, query: query})
	for _, dependency := range executionContext.Dependencies {
		if dependency != nil {
			inputs = append(inputs, objectMetricAuthorizationInput{definition: &dependency.Definition})
		}
	}
	return s.objectMetricAuthorizationFingerprint(ctx, knID, inputs)
}

func (s *queryAuthorizationService) AuthorizeObjectMetricTrialWithFingerprint(ctx context.Context,
	knID, branch string, definition *interfaces.ObjectMetricDefinitionV1,
	query *interfaces.ObjectMetricQueryRequestV1, dependencies []*interfaces.ObjectMetricDefinitionV1) (interfaces.ObjectMetricAuthorizationFingerprint, error) {
	if err := validateTrialIdentity(ctx, knID, branch); err != nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{}, err
	}
	inputs := []objectMetricAuthorizationInput{{definition: definition, query: query}}
	resources := []interfaces.PermissionResource{{Type: interfaces.PermissionResourceTypeKnowledgeNetwork, ID: knID}}
	for _, dependency := range dependencies {
		if dependency == nil || dependency.Version < 1 {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, invalidQuery(ctx, "published trial dependency is required")
		}
		inputs = append(inputs, objectMetricAuthorizationInput{definition: dependency})
		resources = append(resources, interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeMetric, knID, dependency.ID))
	}
	if err := s.authorizeObjectMetricDefinitions(ctx, knID, inputs, resources); err != nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{}, err
	}
	return s.objectMetricAuthorizationFingerprint(ctx, knID, inputs)
}

type objectMetricAuthorizationPropertyBinding struct {
	ObjectTypeRef string                              `json:"object_type_ref"`
	Properties    []string                            `json:"properties,omitempty"`
	Decisions     []interfaces.PropertyAccessDecision `json:"decisions,omitempty"`
}

type objectMetricAuthorizationRowBinding struct {
	ObjectTypeRef string `json:"object_type_ref"`
	Digest        string `json:"digest"`
}

type objectMetricAuthorizationProxyBinding struct {
	ProxyID               string `json:"proxy_id"`
	ProxyType             string `json:"proxy_type"`
	ProxyVersion          int64  `json:"proxy_version"`
	PublishedModelVersion string `json:"published_model_version"`
}

func (s *queryAuthorizationService) objectMetricAuthorizationFingerprint(ctx context.Context, knID string,
	inputs []objectMetricAuthorizationInput) (interfaces.ObjectMetricAuthorizationFingerprint, error) {
	caller, ok := interfaces.RowFilterCallerFromContext(ctx)
	if !ok {
		return interfaces.ObjectMetricAuthorizationFingerprint{},
			queryPermissionDenied(ctx, "object metric row-filter caller is unavailable")
	}
	objectTypeIDs := make(map[string]struct{})
	relationIDs := make(map[string]struct{})
	propertyRequirements := make(map[string][]string)
	for _, input := range inputs {
		if input.definition == nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, invalidQuery(ctx, "object metric definition is required")
		}
		definition := input.definition
		objectTypeIDs[definition.OwnerObjectTypeID] = struct{}{}
		if qualifiers := definition.Specification.BusinessQualifiers; qualifiers != nil && qualifiers.RelationPath != nil {
			for _, step := range qualifiers.RelationPath.Steps {
				objectTypeIDs[step.TargetObjectTypeID] = struct{}{}
				relationIDs[step.RelationTypeID] = struct{}{}
			}
		}
		if alignment := definition.Specification.Alignment; alignment != nil && alignment.ResultOwnerObjectTypeID != "" {
			objectTypeIDs[alignment.ResultOwnerObjectTypeID] = struct{}{}
		}
		requirements, err := objectMetricPropertyRequirements(definition, input.query)
		if err != nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, invalidQuery(ctx, err.Error())
		}
		for objectTypeID, properties := range requirements {
			objectTypeIDs[objectTypeID] = struct{}{}
			propertyRequirements[objectTypeID] = append(propertyRequirements[objectTypeID], properties...)
		}
	}
	orderedIDs := make([]string, 0, len(objectTypeIDs))
	for objectTypeID := range objectTypeIDs {
		if strings.TrimSpace(objectTypeID) == "" {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, invalidQuery(ctx, "object metric object type is required")
		}
		orderedIDs = append(orderedIDs, objectTypeID)
	}
	sort.Strings(orderedIDs)
	refs := make([]string, 0, len(orderedIDs))
	propertyBindings := make([]objectMetricAuthorizationPropertyBinding, 0, len(orderedIDs))
	models := make([]interfaces.ObjectType, 0, len(orderedIDs))
	proxies := make([]*interfaces.TrustedProxyContext, 0, len(orderedIDs))
	for _, objectTypeID := range orderedIDs {
		properties := uniqueStrings(propertyRequirements[objectTypeID])
		sort.Strings(properties)
		ref := knID + "/" + objectTypeID
		refs = append(refs, ref)
		propertyBindings = append(propertyBindings, objectMetricAuthorizationPropertyBinding{
			ObjectTypeRef: ref,
			Properties:    properties,
		})
		model, err := s.loadObjectType(ctx, knID, objectTypeID)
		if err != nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, err
		}
		// Physical index progress and storage statistics are not model or
		// authorization facts and must not decide result validity.
		model.Status = nil
		models = append(models, model)
		if s.proxy == nil || model.DataSource == nil || model.DataSource.ID == "" {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, dependencyResolutionFailed(ctx, fmt.Errorf("metric proxy binding is unavailable"))
		}
		binding, err := s.proxy.Resolve(ctx, interfaces.TrustedProxyBinding{
			KNID: knID, ChildType: interfaces.PermissionResourceTypeObjectType, ChildID: objectTypeID,
			TargetType: interfaces.ProxyTargetTypeResource, TargetID: model.DataSource.ID, Operation: interfaces.PermissionOperationQueryData,
		})
		if err != nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, err
		}
		if binding == nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, dependencyResolutionFailed(ctx, fmt.Errorf("metric proxy binding is unavailable"))
		}
		proxies = append(proxies, binding)
	}
	// Include implicit join/identity fields as well as explicit expressions.
	// They may be checked only by the graph/object reader during execution,
	// which a snapshot hit deliberately skips.
	plan, err := propertyaccess.Build(ctx, s.properties, models)
	if err != nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{}, err
	}
	for index, model := range models {
		names := make([]string, 0, len(model.DataProperties))
		for _, property := range model.DataProperties {
			names = append(names, property.Name)
		}
		sort.Strings(names)
		for _, name := range names {
			decision, exists := plan.Decision(refs[index], name)
			if !exists {
				return interfaces.ObjectMetricAuthorizationFingerprint{}, dependencyResolutionFailed(ctx, fmt.Errorf("metric property decision is unavailable"))
			}
			propertyBindings[index].Decisions = append(propertyBindings[index].Decisions, decision)
		}
	}
	orderedRelations := make([]string, 0, len(relationIDs))
	for id := range relationIDs {
		orderedRelations = append(orderedRelations, id)
	}
	sort.Strings(orderedRelations)
	relations := make([]interfaces.RelationType, 0, len(orderedRelations))
	for _, id := range orderedRelations {
		relation, err := s.loadRelationType(ctx, knID, id)
		if err != nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, err
		}
		relations = append(relations, relation)
		backing, exists, err := indirectBackingResource(relation)
		if err != nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, dependencyResolutionFailed(ctx, err)
		}
		if exists {
			binding, err := s.proxy.Resolve(ctx, interfaces.TrustedProxyBinding{
				KNID: knID, ChildType: interfaces.PermissionResourceTypeRelationType, ChildID: id,
				TargetType: interfaces.ProxyTargetTypeResource, TargetID: backing.ID, Operation: interfaces.PermissionOperationQueryData,
			})
			if err != nil {
				return interfaces.ObjectMetricAuthorizationFingerprint{}, err
			}
			if binding == nil {
				return interfaces.ObjectMetricAuthorizationFingerprint{}, dependencyResolutionFailed(ctx, fmt.Errorf("metric relation proxy binding is unavailable"))
			}
			proxies = append(proxies, binding)
		}
	}
	if s.snapshotProxies == nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{}, dependencyResolutionFailed(ctx, fmt.Errorf("metric proxy authorization is unavailable"))
	}
	if err := s.snapshotProxies.AuthorizeMetricSnapshotProxies(ctx, proxies); err != nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{}, err
	}
	if s == nil || s.rowFilters == nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{},
			dependencyResolutionFailed(ctx, fmt.Errorf("row-filter service is not configured"))
	}
	decisions := make([]interfaces.RowFilterDecisionEntry, 0, len(refs))
	for start := 0; start < len(refs); start += 100 {
		batch, err := s.rowFilters.ResolveRowFilters(ctx, refs[start:min(start+100, len(refs))])
		if err != nil {
			return interfaces.ObjectMetricAuthorizationFingerprint{}, err
		}
		decisions = append(decisions, batch...)
	}
	if len(decisions) != len(refs) {
		return interfaces.ObjectMetricAuthorizationFingerprint{},
			dependencyResolutionFailed(ctx, fmt.Errorf("row-filter decision count mismatch"))
	}
	rowBindings := make([]objectMetricAuthorizationRowBinding, 0, len(decisions))
	for index, decision := range decisions {
		if decision.ObjectTypeRef != refs[index] || strings.TrimSpace(decision.EffectiveRowFilterDigest) == "" {
			return interfaces.ObjectMetricAuthorizationFingerprint{},
				dependencyResolutionFailed(ctx, fmt.Errorf("row-filter decision does not match the metric input"))
		}
		rowBindings = append(rowBindings, objectMetricAuthorizationRowBinding{
			ObjectTypeRef: decision.ObjectTypeRef,
			Digest:        decision.EffectiveRowFilterDigest,
		})
	}
	var proxy *objectMetricAuthorizationProxyBinding
	if trusted, exists := interfaces.TrustedProxyContextFromContext(ctx); exists {
		proxy = &objectMetricAuthorizationProxyBinding{
			ProxyID:               trusted.Proxy.ID,
			ProxyType:             trusted.Proxy.Type,
			ProxyVersion:          trusted.ProxyVersion,
			PublishedModelVersion: trusted.PublishedModelVersion,
		}
	}
	payload := struct {
		Caller     interfaces.AccountInfo                     `json:"caller"`
		Proxy      *objectMetricAuthorizationProxyBinding     `json:"proxy,omitempty"`
		Properties []objectMetricAuthorizationPropertyBinding `json:"properties"`
		RowFilters []objectMetricAuthorizationRowBinding      `json:"row_filters"`
		Models     []interfaces.ObjectType                    `json:"models"`
		Relations  []interfaces.RelationType                  `json:"relations"`
		Bindings   []*interfaces.TrustedProxyContext          `json:"bindings"`
	}{Caller: caller, Proxy: proxy, Properties: propertyBindings, RowFilters: rowBindings,
		Models: models, Relations: relations, Bindings: proxies}
	data, err := json.Marshal(payload)
	if err != nil {
		return interfaces.ObjectMetricAuthorizationFingerprint{},
			dependencyResolutionFailed(ctx, fmt.Errorf("encode object metric authorization fingerprint: %w", err))
	}
	digest := sha256.Sum256(data)
	return interfaces.ObjectMetricAuthorizationFingerprint{Digest: hex.EncodeToString(digest[:])}, nil
}
