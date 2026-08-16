// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"strconv"

	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/locale"
)

// ValidateHeaderMethodOverride validates the method override passed in the request header.
func ValidateHeaderMethodOverride(ctx context.Context, headerMethod string) error {
	if headerMethod == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_NullParameter_OverrideMethod)
	}
	if headerMethod != "GET" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_OverrideMethod).
			WithErrorDetails(locale.ValidationDetail(ctx, "OverrideMethodInvalid", map[string]any{"value": headerMethod}))
	}

	return nil
}

// validateObjectsQueryParameters validates object query parameters.
func validateObjectsQueryParameters(ctx context.Context, includeTypeInfo string, ignoringStoreCache string,
	includeLogicParams string, excludeSystemProperties []string) (interfaces.CommonQueryParameters, error) {

	includeType, err := strconv.ParseBool(includeTypeInfo)
	if err != nil {
		return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IncludeTypeInfo).
			WithErrorDetails(locale.ValidationDetail(ctx, "IncludeTypeInfoInvalid", map[string]any{"value": includeTypeInfo}))
	}

	includeLogicP, err := strconv.ParseBool(includeLogicParams)
	if err != nil {
		return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IncludeTypeInfo).
			WithErrorDetails(locale.ValidationDetail(ctx, "IncludeLogicParamsInvalid", map[string]any{"value": includeLogicParams}))
	}

	ignoringStore, err := strconv.ParseBool(ignoringStoreCache)
	if err != nil {
		return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IgnoringStoreCache).
			WithErrorDetails(locale.ValidationDetail(ctx, "IgnoringStoreCacheInvalid", map[string]any{"value": ignoringStoreCache}))
	}

	// Validate excluded system properties.
	validFields := map[string]bool{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       true,
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: true,
		interfaces.SYSTEM_PROPERTY_DISPLAY:           true,
	}
	for _, field := range excludeSystemProperties {
		if !validFields[field] {
			return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(locale.ValidationDetail(ctx, "ExcludedSystemPropertyInvalid", map[string]any{"field": field}))
		}
	}

	return interfaces.CommonQueryParameters{
		IncludeTypeInfo:         includeType,
		IncludeLogicParams:      includeLogicP,
		IgnoringStore:           ignoringStore,
		ExcludeSystemProperties: excludeSystemProperties,
	}, nil
}

// validateSugraphQueryParameters validates subgraph query parameters.
func validateSugraphQueryParameters(ctx context.Context,
	includeLogicParams string, ignoringStoreCache string, excludeSystemProperties []string) (interfaces.CommonQueryParameters, error) {

	includeLogicP, err := strconv.ParseBool(includeLogicParams)
	if err != nil {
		return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IncludeTypeInfo).
			WithErrorDetails(locale.ValidationDetail(ctx, "IncludeLogicParamsInvalid", map[string]any{"value": includeLogicParams}))
	}

	ignoringStore, err := strconv.ParseBool(ignoringStoreCache)
	if err != nil {
		return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IgnoringStoreCache).
			WithErrorDetails(locale.ValidationDetail(ctx, "IgnoringStoreCacheInvalid", map[string]any{"value": ignoringStoreCache}))
	}

	// Validate excluded system properties.
	validFields := map[string]bool{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       true,
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: true,
		interfaces.SYSTEM_PROPERTY_DISPLAY:           true,
	}
	for _, field := range excludeSystemProperties {
		if !validFields[field] {
			return interfaces.CommonQueryParameters{}, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(locale.ValidationDetail(ctx, "ExcludedSystemPropertyInvalid", map[string]any{"field": field}))
		}
	}

	return interfaces.CommonQueryParameters{
		IncludeLogicParams:      includeLogicP,
		IgnoringStore:           ignoringStore,
		ExcludeSystemProperties: excludeSystemProperties,
	}, nil
}

// validateSubgraphSearchRequest validates a source-based subgraph query.
func validateSubgraphSearchRequest(ctx context.Context, query *interfaces.SubGraphQueryBaseOnSource) error {

	// Decode the untyped filter condition into CondCfg.
	var actualCond *cond.CondCfg
	err := mapstructure.Decode(query.Condition, &actualCond)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_Condition).
			WithErrorDetails(locale.ValidationDetail(ctx, "ConditionDecodeFailed", nil))
	}
	query.ActualCondition = actualCond

	// Require the source object type.
	if query.SourceObjecTypeId == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_SourceObjectTypeId)
	}

	// Require a direction.
	if query.Direction == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_Direction)
	}

	// Validate the direction.
	if !interfaces.DIRECTION_MAP[query.Direction] {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_Direction).
			WithErrorDetails(locale.ValidationDetail(ctx, "DirectionInvalid", map[string]any{"value": query.Direction}))
	}

	// Limit the path length to three edges.
	if query.PathLength > 3 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_PathLength).
			WithErrorDetails(locale.ValidationDetail(ctx, "PathLengthInvalid", map[string]any{"limit": 3, "value": query.PathLength}))
	}

	// Validate each optional sort definition. Field membership is validated by the service after loading the object type.
	if len(query.Sort) > 0 {
		for _, sp := range query.Sort {
			if sp.Field == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortFieldRequired", nil))
			}
			if sp.Direction == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortDirectionRequired", nil))
			}
			if sp.Direction != interfaces.DESC_DIRECTION && sp.Direction != interfaces.ASC_DIRECTION {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortDirectionInvalid", map[string]any{"value": sp.Direction}))
			}
		}
	}

	// Apply the default limit and validate its range.
	if query.Limit == 0 {
		query.Limit = interfaces.DEFAULT_LIMIT
	}
	if query.Limit < 1 || query.Limit > interfaces.MAX_LIMIT {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "LimitRange", map[string]any{"min": 1, "max": interfaces.MAX_LIMIT, "value": query.Limit}))
	}

	return nil

}

// validateSubgraphQueryByPathRequest validates a path-based subgraph query.
func validateSubgraphQueryByPathRequest(ctx context.Context, query *interfaces.SubGraphQueryBaseOnTypePath) error {

	for i := range query.Paths.TypePaths {
		if len(query.Paths.TypePaths[i].Edges) > 10 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(locale.ValidationDetail(ctx, "PathDegreeLimit", map[string]any{"limit": 10}))
		}
		if query.Paths.TypePaths[i].Limit == 0 {
			query.Paths.TypePaths[i].Limit = interfaces.DEFAULT_PATHS // Use the maximum default when no path limit is provided.
		}
	}

	for pathIndex, path := range query.Paths.TypePaths {
		// 1. Require nodes for every path.
		if len(path.ObjectTypes) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_TypePathObjectTypes)
		}
		// 2. Require edges for every path.
		if len(path.Edges) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_TypePathRelationTypes)
		}

		// 3. Validate edge identifiers and their positions in the path.
		for i, edge := range path.Edges {
			// Relation-type existence is validated by the service.
			if edge.RelationTypeId == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "RelationTypeIDRequired", map[string]any{"index": i + 1}))
			}
			// Object-type existence is validated by the service.
			if edge.SourceObjectTypeId == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SourceObjectTypeIDRequired", map[string]any{"index": i + 1}))
			}
			// Require the target object-type ID.
			if edge.TargetObjectTypeId == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "TargetObjectTypeIDRequired", map[string]any{"index": i + 1}))
			}

			// The source of edge i must equal object type i.
			if edge.SourceObjectTypeId != path.ObjectTypes[i].OTID {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).
					WithErrorDetails(locale.ValidationDetail(ctx, "EdgeSourceMismatch", map[string]any{
						"index": i, "actual": edge.SourceObjectTypeId, "position": i, "expected": path.ObjectTypes[i].OTID,
					}))
			}
			// The target of edge i must equal object type i+1.
			if edge.TargetObjectTypeId != path.ObjectTypes[i+1].OTID {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).
					WithErrorDetails(locale.ValidationDetail(ctx, "EdgeTargetMismatch", map[string]any{
						"index": i, "actual": edge.TargetObjectTypeId, "position": i + 1, "expected": path.ObjectTypes[i+1].OTID,
					}))
			}
			// Consecutive edges must connect.
			if i > 0 {
				if edge.SourceObjectTypeId != path.Edges[i-1].TargetObjectTypeId {
					return rest.NewHTTPError(ctx, http.StatusBadRequest,
						oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).
						WithErrorDetails(locale.ValidationDetail(ctx, "DisconnectedPath", map[string]any{
							"index": i, "actual": edge.SourceObjectTypeId, "expected": path.Edges[i-1].TargetObjectTypeId,
						}))
				}
			}
		}

		// 4. Decode and validate each node's filter and pagination configuration.
		for i := range path.ObjectTypes {
			var actualCond *cond.CondCfg
			err := mapstructure.Decode(path.ObjectTypes[i].Condition, &actualCond)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_Condition).
					WithErrorDetails(locale.ValidationDetail(ctx, "ConditionDecodeFailed", nil))
			}
			query.Paths.TypePaths[pathIndex].ObjectTypes[i].ActualCondition = actualCond

			// Field membership is validated after loading the object type.
			if len(path.ObjectTypes[i].Sort) > 0 {
				for _, sp := range path.ObjectTypes[i].Sort {
					if sp.Field == "" {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
							WithErrorDetails(locale.ValidationDetail(ctx, "SortFieldRequired", nil))
					}
					if sp.Direction == "" {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
							WithErrorDetails(locale.ValidationDetail(ctx, "SortDirectionRequired", nil))
					}
					if sp.Direction != interfaces.DESC_DIRECTION && sp.Direction != interfaces.ASC_DIRECTION {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
							WithErrorDetails(locale.ValidationDetail(ctx, "SortDirectionInvalid", map[string]any{"value": sp.Direction}))
					}
				}
			}

			// Apply the default limit and validate its range.
			if path.ObjectTypes[i].Limit == 0 {
				path.ObjectTypes[i].Limit = interfaces.DEFAULT_LIMIT
			}
			if path.ObjectTypes[i].Limit < 1 || path.ObjectTypes[i].Limit > interfaces.MAX_LIMIT {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "LimitRange", map[string]any{"min": 1, "max": interfaces.MAX_LIMIT, "value": path.ObjectTypes[i].Limit}))
			}
		}
	}

	return nil
}

// validateObjectSearchRequest validates an object-type data query.
func validateObjectSearchRequest(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType) error {

	// Decode the untyped filter condition into CondCfg.
	var actualCond *cond.CondCfg
	err := mapstructure.Decode(query.Condition, &actualCond)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_Condition).
			WithErrorDetails(locale.ValidationDetail(ctx, "ConditionDecodeFailed", nil))
	}
	query.ActualCondition = actualCond

	// Validate optional sort definitions.
	if len(query.Sort) > 0 {
		for _, sp := range query.Sort {
			if sp.Field == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortFieldRequired", nil))
			}
			if sp.Direction == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortDirectionRequired", nil))
			}
			if sp.Direction != interfaces.DESC_DIRECTION && sp.Direction != interfaces.ASC_DIRECTION {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortDirectionInvalid", map[string]any{"value": sp.Direction}))
			}

			// Field membership is validated after loading the object type.
		}
	}

	// Validate the requested limit.
	if query.Limit < 1 || query.Limit > interfaces.MAX_LIMIT {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "LimitRange", map[string]any{"min": 1, "max": interfaces.MAX_LIMIT, "value": query.Limit}))
	}
	if query.Limit == 0 {
		query.Limit = interfaces.DEFAULT_OBJECT_LIMIT
	}

	return nil
}

// validateActionQuery validates an action-type data query.
func validateActionQuery(ctx context.Context, query *interfaces.ActionQuery) error {

	// An empty identity list is allowed; the action-type condition is used instead.
	// if len(query.InstanceIdentities) == 0 {
	// 	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionType_InvalidParameter).
	// 		WithErrorDetails("行动查询的唯一标识不能为空")
	// }
	return nil
}

// validateObjectPropertyValueQuery validates an object-property value query.
func validateObjectPropertyValueQuery(ctx context.Context, query *interfaces.ObjectPropertyValueQuery) error {

	// Require an object identity.
	if len(query.InstanceIdentities) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "ObjectIdentityRequired", nil))
	}

	// Require at least one property.
	if len(query.Properties) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "PropertiesRequired", nil))
	}

	// Start, end, instant, and step defaults are applied by the service after loading the object type.

	return nil
}

// validateSubgraphQueryByObjectsRequest validates a subgraph query rooted at object instances.
func validateSubgraphQueryByObjectsRequest(ctx context.Context, query *interfaces.SubGraphQueryBaseOnObjects) error {

	// Require entries.
	if len(query.Entries) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "InstancesRequired", nil))
	}

	// Limit the number of entries.
	if len(query.Entries) > 1000 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "InstancesLimit", map[string]any{"limit": 1000}))
	}

	// Validate every entry.
	for i, entry := range query.Entries {
		// Require object_type_id.
		if entry.ObjectTypeID == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(locale.ValidationDetail(ctx, "ObjectTypeIDRequired", map[string]any{"index": i + 1}))
		}

		// Require _instance_identity.
		if len(entry.InstanceIdentity) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(locale.ValidationDetail(ctx, "ObjectIdentityAtIndexRequired", map[string]any{"index": i + 1}))
		}
	}

	return nil
}
