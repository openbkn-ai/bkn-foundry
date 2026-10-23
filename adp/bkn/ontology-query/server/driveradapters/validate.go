// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// parseSearchAfterQuery preserves numeric cursor literals while retaining the
// legacy comma-separated query format. A JSON array can be used when a string
// cursor component contains a comma or when exact scalar types are required.
func parseSearchAfterQuery(raw string) (interfaces.SearchAfterArray, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return interfaces.SearchAfterArray{}, nil
	}

	if strings.HasPrefix(raw, "[") {
		var values interfaces.SearchAfterArray
		if err := common.UnmarshalPreciseJSON([]byte(raw), &values); err != nil {
			return nil, err
		}
		return values, nil
	}

	parts := strings.Split(raw, ",")
	values := make(interfaces.SearchAfterArray, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		var decoded any
		if err := common.UnmarshalPreciseJSON([]byte(part), &decoded); err == nil {
			if number, ok := decoded.(json.Number); ok {
				values = append(values, number)
				continue
			}
		}
		values = append(values, part)
	}
	return values, nil
}

// ValidateHeaderMethodOverride validates the method override passed in the request header.
func ValidateHeaderMethodOverride(ctx context.Context, headerMethod string) error {
	if headerMethod == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_NullParameter_OverrideMethod)
	}
	if headerMethod != "GET" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.OverrideMethodInvalid", map[string]any{"value": headerMethod})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_OverrideMethod).
			WithErrorDetails(localizedText)
	}

	return nil
}

// validateObjectsQueryParameters validates object query parameters.
func validateObjectsQueryParameters(ctx context.Context, includeTypeInfo string, ignoreLocalIndex string,
	includeLogicParams string, excludeSystemProperties []string) (interfaces.CommonQueryParameters, error) {

	includeType, err := strconv.ParseBool(includeTypeInfo)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.IncludeTypeInfoInvalid", map[string]any{"value": includeTypeInfo})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IncludeTypeInfo).
			WithErrorDetails(localizedText)
	}

	includeLogicP, err := strconv.ParseBool(includeLogicParams)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.IncludeLogicParamsInvalid", map[string]any{"value": includeLogicParams})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IncludeTypeInfo).
			WithErrorDetails(localizedText)
	}

	ignoreIndex, err := strconv.ParseBool(ignoreLocalIndex)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.IgnoreLocalIndexInvalid", map[string]any{"value": ignoreLocalIndex})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IgnoreLocalIndex).
			WithErrorDetails(localizedText)
	}

	// Validate excluded system properties.
	validFields := map[string]bool{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       true,
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: true,
		interfaces.SYSTEM_PROPERTY_DISPLAY:           true,
	}
	for _, field := range excludeSystemProperties {
		if !validFields[field] {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ExcludedSystemPropertyInvalid", map[string]any{"field": field})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
	}

	return interfaces.CommonQueryParameters{
		IncludeTypeInfo:         includeType,
		IncludeLogicParams:      includeLogicP,
		IgnoreLocalIndex:        ignoreIndex,
		ExcludeSystemProperties: excludeSystemProperties,
	}, nil
}

// validateSugraphQueryParameters validates subgraph query parameters.
func validateSugraphQueryParameters(ctx context.Context,
	includeLogicParams string, ignoreLocalIndex string, excludeSystemProperties []string) (interfaces.CommonQueryParameters, error) {

	includeLogicP, err := strconv.ParseBool(includeLogicParams)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.IncludeLogicParamsInvalid", map[string]any{"value": includeLogicParams})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IncludeTypeInfo).
			WithErrorDetails(localizedText)
	}

	ignoreIndex, err := strconv.ParseBool(ignoreLocalIndex)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.IgnoreLocalIndexInvalid", map[string]any{"value": ignoreLocalIndex})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter_IgnoreLocalIndex).
			WithErrorDetails(localizedText)
	}

	// Validate excluded system properties.
	validFields := map[string]bool{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       true,
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: true,
		interfaces.SYSTEM_PROPERTY_DISPLAY:           true,
	}
	for _, field := range excludeSystemProperties {
		if !validFields[field] {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ExcludedSystemPropertyInvalid", map[string]any{"field": field})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return interfaces.CommonQueryParameters{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
	}

	return interfaces.CommonQueryParameters{
		IncludeLogicParams:      includeLogicP,
		IgnoreLocalIndex:        ignoreIndex,
		ExcludeSystemProperties: excludeSystemProperties,
	}, nil
}

// validateSubgraphSearchRequest validates a source-based subgraph query.
func validateSubgraphSearchRequest(ctx context.Context, query *interfaces.SubGraphQueryBaseOnSource) error {

	// Decode the untyped filter condition into CondCfg.
	var actualCond *cond.CondCfg
	err := mapstructure.Decode(query.Condition, &actualCond)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ConditionDecodeFailed", map[string]any{"error": err.Error()})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_Condition).
			WithErrorDetails(localizedText)
	}
	query.ActualCondition = actualCond

	// Require the source object type.
	if query.SourceObjecTypeId == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_SourceObjectTypeId)
	}

	// Require a direction.
	if query.Direction == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_Direction)
	}

	// Validate the direction.
	if !interfaces.DIRECTION_MAP[query.Direction] {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.DirectionInvalid", map[string]any{"value": query.Direction})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_Direction).
			WithErrorDetails(localizedText)
	}

	// Limit the path length to three edges.
	if query.PathLength > 3 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.PathLengthInvalid", map[string]any{"limit": 3, "value": query.PathLength})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_PathLength).WithErrorDetails(localizedText)
	}

	// Validate each optional sort definition. Field membership is validated by the service after loading the object type.
	if len(query.Sort) > 0 {
		for _, sp := range query.Sort {
			if sp.Field == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortFieldRequired", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if sp.Direction == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortDirectionRequired", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if sp.Direction != interfaces.DESC_DIRECTION && sp.Direction != interfaces.ASC_DIRECTION {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortDirectionInvalid", map[string]any{"value": sp.Direction})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
		}
	}

	// Apply the default limit and validate its range.
	if query.Limit == 0 {
		query.Limit = interfaces.DEFAULT_LIMIT
	}
	if query.Limit < 1 || query.Limit > interfaces.MAX_LIMIT {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.LimitRange", map[string]any{"min": 1, "max": interfaces.MAX_LIMIT, "value": query.Limit})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}

	return nil

}

// validateSubgraphQueryByPathRequest validates a path-based subgraph query.
func validateSubgraphQueryByPathRequest(ctx context.Context, query *interfaces.SubGraphQueryBaseOnTypePath) error {

	for i := range query.Paths.TypePaths {
		if len(query.Paths.TypePaths[i].Edges) > 10 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.PathDegreeLimit", map[string]any{"limit": 10})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		if query.Paths.TypePaths[i].Limit == 0 {
			query.Paths.TypePaths[i].Limit = interfaces.DEFAULT_PATHS // Use the maximum default when no path limit is provided.
		}
	}

	for pathIndex, path := range query.Paths.TypePaths {
		// 1. Require nodes for every path.
		if len(path.ObjectTypes) == 0 {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_TypePathObjectTypes)
		}
		// 2. Require edges for every path.
		if len(path.Edges) == 0 {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_NullParameter_TypePathRelationTypes)
		}

		// 3. A path of n edges walks n+1 nodes. Nodes past that have always been ignored
		// and stay accepted; too few used to index past the end of object_types.
		if len(path.ObjectTypes) < len(path.Edges)+1 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.PathNodeCountMismatch", map[string]any{
				"edges": len(path.Edges), "expected": len(path.Edges) + 1, "actual": len(path.ObjectTypes),
			})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).WithErrorDetails(localizedText)
		}
		for i := 0; i <= len(path.Edges); i++ {
			// Object-type existence is validated by the service.
			if path.ObjectTypes[i].OTID == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.PathNodeIDRequired", map[string]any{"index": i + 1})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
					oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).
					WithErrorDetails(localizedText)
			}
		}

		// 4. Validate each edge against the nodes it sits between.
		for i := range path.Edges {
			edge := &query.Paths.TypePaths[pathIndex].Edges[i]
			// Relation-type existence, and whether the edge can walk it, are validated by the service.
			if edge.RelationTypeId == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.RelationTypeIDRequired", map[string]any{"index": i + 1})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if edge.Direction != "" && edge.Direction != interfaces.DIRECTION_FORWARD &&
				edge.Direction != interfaces.DIRECTION_BACKWARD {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.EdgeDirectionInvalid", map[string]any{
					"index": i + 1, "value": edge.Direction,
				})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).WithErrorDetails(localizedText)
			}

			// The endpoints are the nodes this hop walks from and to, so both follow from
			// the path: an omitted one is filled in, a supplied one must agree.
			from, to := path.ObjectTypes[i].OTID, path.ObjectTypes[i+1].OTID
			givenFrom, givenTo := edge.SourceObjectTypeId, edge.TargetObjectTypeId
			if givenFrom == "" {
				givenFrom = from
			}
			if givenTo == "" {
				givenTo = to
			}
			if givenFrom != from || givenTo != to {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.EdgeEndpointsMismatch", map[string]any{
					"index": i + 1, "relation": edge.RelationTypeId,
					"expectedFrom": from, "expectedTo": to, "from": givenFrom, "to": givenTo,
				})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).WithErrorDetails(localizedText)
			}
			edge.SourceObjectTypeId, edge.TargetObjectTypeId = from, to
		}

		// 5. Decode and validate each node's filter and pagination configuration.
		for i := range path.ObjectTypes {
			var actualCond *cond.CondCfg
			err := mapstructure.Decode(path.ObjectTypes[i].Condition, &actualCond)
			if err != nil {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ConditionDecodeFailed", map[string]any{"error": err.Error()})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_Condition).
					WithErrorDetails(localizedText)
			}
			query.Paths.TypePaths[pathIndex].ObjectTypes[i].ActualCondition = actualCond

			// Field membership is validated after loading the object type.
			if len(path.ObjectTypes[i].Sort) > 0 {
				for _, sp := range path.ObjectTypes[i].Sort {
					if sp.Field == "" {
						localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortFieldRequired", nil)
						if renderErr != nil {
							logger.Errorf("localized message rendering failed: %v", renderErr)
						}
						return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
							WithErrorDetails(localizedText)
					}
					if sp.Direction == "" {
						localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortDirectionRequired", nil)
						if renderErr != nil {
							logger.Errorf("localized message rendering failed: %v", renderErr)
						}
						return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
							WithErrorDetails(localizedText)
					}
					if sp.Direction != interfaces.DESC_DIRECTION && sp.Direction != interfaces.ASC_DIRECTION {
						localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortDirectionInvalid", map[string]any{"value": sp.Direction})
						if renderErr != nil {
							logger.Errorf("localized message rendering failed: %v", renderErr)
						}
						return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
							WithErrorDetails(localizedText)
					}
				}
			}

			// Apply the default limit and validate its range.
			if path.ObjectTypes[i].Limit == 0 {
				path.ObjectTypes[i].Limit = interfaces.DEFAULT_LIMIT
			}
			if path.ObjectTypes[i].Limit < 1 || path.ObjectTypes[i].Limit > interfaces.MAX_LIMIT {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.LimitRange", map[string]any{"min": 1, "max": interfaces.MAX_LIMIT, "value": path.ObjectTypes[i].Limit})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
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
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ConditionDecodeFailed", map[string]any{"error": err.Error()})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InvalidParameter_Condition).
			WithErrorDetails(localizedText)
	}
	query.ActualCondition = actualCond

	// Validate optional sort definitions.
	if len(query.Sort) > 0 {
		for _, sp := range query.Sort {
			if sp.Field == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortFieldRequired", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if sp.Direction == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortDirectionRequired", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if sp.Direction != interfaces.DESC_DIRECTION && sp.Direction != interfaces.ASC_DIRECTION {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.SortDirectionInvalid", map[string]any{"value": sp.Direction})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}

			// Field membership is validated after loading the object type.
		}
	}

	// Validate the requested limit.
	if query.Limit < 1 || query.Limit > interfaces.MAX_LIMIT {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.LimitRange", map[string]any{"min": 1, "max": interfaces.MAX_LIMIT, "value": query.Limit})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
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
	// 	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionType_InvalidParameter).
	// 		WithErrorDetails("行动查询的唯一标识不能为空")
	// }
	return nil
}

// validateObjectPropertyValueQuery validates an object-property value query.
func validateObjectPropertyValueQuery(ctx context.Context, query *interfaces.ObjectPropertyValueQuery) error {

	// Require an object identity.
	if len(query.InstanceIdentities) == 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ObjectIdentityRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Require at least one property.
	if len(query.Properties) == 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.PropertiesRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Start, end, instant, and step defaults are applied by the service after loading the object type.

	return nil
}

// validateSubgraphQueryByObjectsRequest validates a subgraph query rooted at object instances.
func validateSubgraphQueryByObjectsRequest(ctx context.Context, query *interfaces.SubGraphQueryBaseOnObjects) error {

	// Require entries.
	if len(query.Entries) == 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.InstancesRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Limit the number of entries.
	if len(query.Entries) > 1000 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.InstancesLimit", map[string]any{"limit": 1000})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Validate every entry.
	for i, entry := range query.Entries {
		// Require object_type_id.
		if entry.ObjectTypeID == "" {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ObjectTypeIDRequired", map[string]any{"index": i + 1})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		// Require _instance_identity.
		if len(entry.InstanceIdentity) == 0 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ObjectIdentityAtIndexRequired", map[string]any{"index": i + 1})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
	}

	return nil
}
