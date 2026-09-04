// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/common"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/logics"
	permissionlogic "ontology-query/logics/permission"
)

type queryAuthorizationService struct {
	models      interfaces.OntologyManagerAccess
	permissions interfaces.PermissionService
}

type authenticationDisabledQueryAuthorizationService struct{}

var (
	_ interfaces.QueryAuthorizationService = (*queryAuthorizationService)(nil)
	_ interfaces.QueryAuthorizationService = (*authenticationDisabledQueryAuthorizationService)(nil)
)

func NewQueryAuthorizationService(appSetting *common.AppSetting) interfaces.QueryAuthorizationService {
	if !common.GetAuthEnabled() {
		return &authenticationDisabledQueryAuthorizationService{}
	}
	return &queryAuthorizationService{
		models:      logics.OMA,
		permissions: permissionlogic.NewPermissionService(appSetting),
	}
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeObjectTypeQuery(
	context.Context, string, string, string,
) error {
	return nil
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeActionTypeQuery(
	context.Context, string, string, string,
) error {
	return nil
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeMetricQuery(
	context.Context, string, string, string,
) error {
	return nil
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeMetricDryRun(
	context.Context, string, string, *interfaces.MetricDefinition,
) error {
	return nil
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeSubgraphBySource(
	context.Context, *interfaces.SubGraphQueryBaseOnSource,
) error {
	return nil
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeSubgraphByTypePath(
	context.Context, *interfaces.SubGraphQueryBaseOnTypePath,
) error {
	return nil
}

func (s *authenticationDisabledQueryAuthorizationService) AuthorizeSubgraphByObjects(
	context.Context, *interfaces.SubGraphQueryBaseOnObjects,
) error {
	return nil
}

func (s *queryAuthorizationService) AuthorizeObjectTypeQuery(ctx context.Context,
	knID, branch, objectTypeID string) error {
	if err := validateQueryIdentity(ctx, knID, branch, objectTypeID); err != nil {
		return err
	}
	objectType, err := s.loadObjectType(ctx, knID, objectTypeID)
	if err != nil {
		return err
	}
	resources, err := objectTypeResources(ctx, knID, objectType)
	if err != nil {
		return err
	}
	return s.require(ctx, resources)
}

func (s *queryAuthorizationService) AuthorizeActionTypeQuery(ctx context.Context,
	knID, branch, actionTypeID string) error {
	if err := validateQueryIdentity(ctx, knID, branch, actionTypeID); err != nil {
		return err
	}
	if s == nil || s.models == nil {
		return dependencyResolutionFailed(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	actionType, _, exists, err := s.models.GetActionType(ctx, knID, interfaces.MAIN_BRANCH, actionTypeID)
	if err != nil {
		return dependencyResolutionFailed(ctx, err)
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionType_ActionTypeNotFound)
	}

	if strings.TrimSpace(actionType.ObjectTypeID) == "" {
		return s.require(ctx, []interfaces.PermissionResource{{
			Type: interfaces.PermissionResourceTypeKnowledgeNetwork,
			ID:   knID,
		}})
	}
	objectType, err := s.loadObjectType(ctx, knID, actionType.ObjectTypeID)
	if err != nil {
		return err
	}
	resources, err := objectTypeResources(ctx, knID, objectType)
	if err != nil {
		return err
	}
	return s.require(ctx, resources)
}

func (s *queryAuthorizationService) AuthorizeMetricQuery(ctx context.Context,
	knID, branch, metricID string) error {
	if err := validateQueryIdentity(ctx, knID, branch, metricID); err != nil {
		return err
	}
	if s == nil || s.models == nil {
		return dependencyResolutionFailed(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	definition, exists, err := s.models.GetMetricDefinition(ctx, knID, interfaces.MAIN_BRANCH, metricID)
	if err != nil {
		return dependencyResolutionFailed(ctx, err)
	}
	if !exists || definition == nil {
		return rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_Metric_NotFound)
	}
	if definition.ID != "" && definition.ID != metricID {
		return dependencyResolutionFailed(ctx, fmt.Errorf("published metric id does not match the requested metric"))
	}
	if err := validateMetricScope(ctx, knID, definition); err != nil {
		return err
	}
	objectType, err := s.loadObjectType(ctx, knID, definition.ScopeRef)
	if err != nil {
		return err
	}
	resources := []interfaces.PermissionResource{
		interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeMetric, knID, metricID),
	}
	dependencies, err := objectTypeResources(ctx, knID, objectType)
	if err != nil {
		return err
	}
	return s.require(ctx, append(resources, dependencies...))
}

func (s *queryAuthorizationService) AuthorizeMetricDryRun(ctx context.Context,
	knID, branch string, definition *interfaces.MetricDefinition) error {
	if err := validateQueryIdentity(ctx, knID, branch, "dry-run"); err != nil {
		return err
	}
	if err := validateMetricScope(ctx, knID, definition); err != nil {
		return err
	}
	objectType, err := s.loadObjectType(ctx, knID, definition.ScopeRef)
	if err != nil {
		return err
	}
	// A dry-run definition is caller-supplied and has no persisted metric
	// resource of its own, so #459 requires the containing KN as its root gate.
	resources := []interfaces.PermissionResource{
		{Type: interfaces.PermissionResourceTypeKnowledgeNetwork, ID: knID},
	}
	dependencies, err := objectTypeResources(ctx, knID, objectType)
	if err != nil {
		return err
	}
	return s.require(ctx, append(resources, dependencies...))
}

func (s *queryAuthorizationService) AuthorizeSubgraphBySource(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource) error {
	if query == nil {
		return invalidQuery(ctx, "subgraph query is required")
	}
	if err := validateQueryIdentity(ctx, query.KNID, query.Branch, query.SourceObjecTypeId); err != nil {
		return err
	}
	if s == nil || s.models == nil {
		return dependencyResolutionFailed(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	sourceObjectType, err := s.loadObjectType(ctx, query.KNID, query.SourceObjecTypeId)
	if err != nil {
		return err
	}
	mandatory, err := objectTypeResources(ctx, query.KNID, sourceObjectType)
	if err != nil {
		return err
	}
	paths, err := s.models.GetRelationTypePathsBaseOnSource(ctx, query.KNID, interfaces.MAIN_BRANCH,
		interfaces.PathsQueryBaseOnSource{
			ConceptGroups:     query.ConceptGroups,
			SourceObjecTypeId: query.SourceObjecTypeId,
			Direction:         query.Direction,
			PathLength:        query.PathLength,
		})
	if err != nil {
		return dependencyResolutionFailed(ctx, err)
	}
	resources := append([]interfaces.PermissionResource(nil), mandatory...)
	pathResources := make([][]interfaces.PermissionResource, len(paths))
	objectTypes := map[string]interfaces.ObjectType{query.SourceObjecTypeId: sourceObjectType}
	relationTypes := make(map[string]interfaces.RelationType)
	for i, path := range paths {
		for _, pathObjectType := range path.ObjectTypes {
			objectType, exists := objectTypes[pathObjectType.OTID]
			if !exists {
				objectType, err = s.loadObjectType(ctx, query.KNID, pathObjectType.OTID)
				if err != nil {
					return err
				}
				objectTypes[pathObjectType.OTID] = objectType
			}
			dependencies, err := objectTypeResources(ctx, query.KNID, objectType)
			if err != nil {
				return err
			}
			pathResources[i] = append(pathResources[i], dependencies...)
		}
		for _, edge := range path.TypeEdges {
			relationType, exists := relationTypes[edge.RelationTypeId]
			if !exists {
				relationType, err = s.loadRelationType(ctx, query.KNID, edge.RelationTypeId)
				if err != nil {
					return err
				}
				relationTypes[edge.RelationTypeId] = relationType
			}
			dependencies, err := relationTypeResources(ctx, query.KNID, edge.RelationTypeId, relationType)
			if err != nil {
				return err
			}
			pathResources[i] = append(pathResources[i], dependencies...)
		}
		resources = append(resources, pathResources[i]...)
	}
	allowed, err := s.filter(ctx, resources)
	if err != nil {
		return err
	}
	if !allResourcesAllowed(allowed, mandatory) {
		return queryPermissionDenied(ctx, "query_data was not granted for the source object type")
	}
	query.AuthorizedTypePaths = make([]interfaces.RelationTypePath, 0, len(paths))
	for i, path := range paths {
		if allResourcesAllowed(allowed, pathResources[i]) {
			query.AuthorizedTypePaths = append(query.AuthorizedTypePaths, path)
		}
	}
	return nil
}

func (s *queryAuthorizationService) AuthorizeSubgraphByTypePath(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnTypePath) error {
	if query == nil {
		return invalidQuery(ctx, "subgraph path query is required")
	}
	if err := validateQueryIdentity(ctx, query.KNID, query.Branch, "type-path"); err != nil {
		return err
	}
	resources := make([]interfaces.PermissionResource, 0)
	pathResources := make([][]interfaces.PermissionResource, len(query.Paths.TypePaths))
	for i, path := range query.Paths.TypePaths {
		for _, objectType := range path.ObjectTypes {
			publishedObjectType, err := s.loadObjectType(ctx, query.KNID, objectType.OTID)
			if err != nil {
				return err
			}
			dependencies, err := objectTypeResources(ctx, query.KNID, publishedObjectType)
			if err != nil {
				return err
			}
			pathResources[i] = append(pathResources[i], dependencies...)
		}
		for _, edge := range path.Edges {
			relationType, err := s.loadRelationType(ctx, query.KNID, edge.RelationTypeId)
			if err != nil {
				return err
			}
			if relationType.SourceObjectTypeID != edge.SourceObjectTypeId ||
				relationType.TargetObjectTypeID != edge.TargetObjectTypeId {
				return invalidQuery(ctx, "relation path does not match the published model")
			}
			dependencies, err := relationTypeResources(ctx, query.KNID, edge.RelationTypeId, relationType)
			if err != nil {
				return err
			}
			pathResources[i] = append(pathResources[i], dependencies...)
		}
		resources = append(resources, pathResources[i]...)
	}
	allowed, err := s.filter(ctx, resources)
	if err != nil {
		return err
	}
	retained := make([]interfaces.QueryRelationTypePath, 0, len(query.Paths.TypePaths))
	for i, path := range query.Paths.TypePaths {
		if allResourcesAllowed(allowed, pathResources[i]) {
			retained = append(retained, path)
		}
	}
	query.Paths.TypePaths = retained
	return nil
}

func (s *queryAuthorizationService) AuthorizeSubgraphByObjects(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects) error {
	if query == nil {
		return invalidQuery(ctx, "object subgraph query is required")
	}
	if err := validateQueryIdentity(ctx, query.KNID, query.Branch, "objects"); err != nil {
		return err
	}
	if s == nil || s.models == nil {
		return dependencyResolutionFailed(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	objectTypeIDs := make([]string, 0, len(query.Entries))
	resources := make([]interfaces.PermissionResource, 0, len(query.Entries)*2)
	for _, entry := range query.Entries {
		objectType, err := s.loadObjectType(ctx, query.KNID, entry.ObjectTypeID)
		if err != nil {
			return err
		}
		objectTypeIDs = append(objectTypeIDs, entry.ObjectTypeID)
		dependencies, err := objectTypeResources(ctx, query.KNID, objectType)
		if err != nil {
			return err
		}
		resources = append(resources, dependencies...)
	}
	objectTypeIDs = uniqueStrings(objectTypeIDs)
	relationTypes, err := s.models.ListRelationTypes(ctx, query.KNID, interfaces.MAIN_BRANCH,
		interfaces.RelationTypesQuery{
			SourceObjectTypeIDs: objectTypeIDs,
			TargetObjectTypeIDs: objectTypeIDs,
		})
	if err != nil {
		return dependencyResolutionFailed(ctx, err)
	}
	objectTypeSet := make(map[string]struct{}, len(objectTypeIDs))
	for _, id := range objectTypeIDs {
		objectTypeSet[id] = struct{}{}
	}
	mandatory := append([]interfaces.PermissionResource(nil), resources...)
	relationResources := make(map[string][]interfaces.PermissionResource, len(relationTypes))
	for _, relationType := range relationTypes {
		if _, ok := objectTypeSet[relationType.SourceObjectTypeID]; !ok {
			continue
		}
		if _, ok := objectTypeSet[relationType.TargetObjectTypeID]; !ok {
			continue
		}
		dependencies, err := relationTypeResources(ctx, query.KNID, relationType.RTID, relationType)
		if err != nil {
			return err
		}
		relationResources[relationType.RTID] = dependencies
		resources = append(resources, dependencies...)
	}
	allowed, err := s.filter(ctx, resources)
	if err != nil {
		return err
	}
	if !allResourcesAllowed(allowed, mandatory) {
		return queryPermissionDenied(ctx, "query_data was not granted for every requested object type")
	}
	query.AuthorizedRelationTypeIDs = make(map[string]struct{}, len(relationResources))
	for relationTypeID, dependencies := range relationResources {
		if allResourcesAllowed(allowed, dependencies) {
			query.AuthorizedRelationTypeIDs[relationTypeID] = struct{}{}
		}
	}
	return nil
}

func (s *queryAuthorizationService) loadObjectType(ctx context.Context, knID, objectTypeID string) (interfaces.ObjectType, error) {
	var empty interfaces.ObjectType
	if s == nil || s.models == nil {
		return empty, dependencyResolutionFailed(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	if err := validateResourceID(knID, objectTypeID); err != nil {
		return empty, invalidQuery(ctx, err.Error())
	}
	objectType, exists, err := s.models.GetObjectType(ctx, knID, interfaces.MAIN_BRANCH, objectTypeID)
	if err != nil {
		return empty, dependencyResolutionFailed(ctx, err)
	}
	if !exists {
		return empty, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
	}
	if objectType.KNID != "" && objectType.KNID != knID {
		return empty, invalidQuery(ctx, "object type belongs to another knowledge network")
	}
	if objectType.Branch != "" && objectType.Branch != interfaces.MAIN_BRANCH {
		return empty, invalidQuery(ctx, "object type is not from the published main model")
	}
	return objectType, nil
}

func (s *queryAuthorizationService) loadRelationType(ctx context.Context,
	knID, relationTypeID string) (interfaces.RelationType, error) {
	var empty interfaces.RelationType
	if s == nil || s.models == nil {
		return empty, dependencyResolutionFailed(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	if err := validateResourceID(knID, relationTypeID); err != nil {
		return empty, invalidQuery(ctx, err.Error())
	}
	relationType, exists, err := s.models.GetRelationType(ctx, knID, interfaces.MAIN_BRANCH, relationTypeID)
	if err != nil {
		return empty, dependencyResolutionFailed(ctx, err)
	}
	if !exists {
		return empty, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_KnowledgeNetwork_RelationTypeNotFound)
	}
	return relationType, nil
}

func (s *queryAuthorizationService) require(ctx context.Context, resources []interfaces.PermissionResource) error {
	if s == nil || s.permissions == nil {
		return dependencyResolutionFailed(ctx, fmt.Errorf("permission service is not configured"))
	}
	return s.permissions.RequireQueryData(ctx, resources)
}

func (s *queryAuthorizationService) filter(ctx context.Context,
	resources []interfaces.PermissionResource) (map[string]struct{}, error) {
	if s == nil || s.permissions == nil {
		return nil, dependencyResolutionFailed(ctx, fmt.Errorf("permission service is not configured"))
	}
	allowedResources, err := s.permissions.FilterQueryData(ctx, resources)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(allowedResources))
	for _, resource := range allowedResources {
		allowed[permissionResourceKey(resource)] = struct{}{}
	}
	return allowed, nil
}

func objectTypeResources(ctx context.Context, knID string,
	objectType interfaces.ObjectType) ([]interfaces.PermissionResource, error) {
	if err := validateResourceID(knID, objectType.OTID); err != nil {
		return nil, invalidQuery(ctx, err.Error())
	}
	if objectType.KNID != "" && objectType.KNID != knID {
		return nil, invalidQuery(ctx, "object type belongs to another knowledge network")
	}
	resources := []interfaces.PermissionResource{
		interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeObjectType, knID, objectType.OTID),
	}
	if objectType.DataSource == nil || strings.TrimSpace(objectType.DataSource.ID) == "" {
		return nil, dependencyResolutionFailed(ctx, fmt.Errorf("object type %s has no published data source", objectType.OTID))
	}
	if objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		return nil, dependencyResolutionFailed(ctx,
			fmt.Errorf("object type %s has unsupported data source type %s", objectType.OTID, objectType.DataSource.Type))
	}
	if err := validateStandaloneResourceID(objectType.DataSource.ID); err != nil {
		return nil, invalidQuery(ctx, err.Error())
	}
	// The published resource reference must be complete, but Vega owns the
	// resource-to-catalog authorization fallback because only Vega has the
	// trusted catalog relationship. It rechecks query_data as the same caller
	// immediately before reading the resource.
	return resources, nil
}

func relationTypeResources(ctx context.Context, knID, relationTypeID string,
	relationType interfaces.RelationType) ([]interfaces.PermissionResource, error) {
	if err := validateResourceID(knID, relationTypeID); err != nil {
		return nil, invalidQuery(ctx, err.Error())
	}
	if relationType.RTID != "" && relationType.RTID != relationTypeID {
		return nil, invalidQuery(ctx, "relation type id does not match the published model")
	}
	resources := []interfaces.PermissionResource{
		interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeRelationType, knID, relationTypeID),
	}

	backing, exists, err := indirectBackingResource(relationType)
	if err != nil {
		return nil, dependencyResolutionFailed(ctx, err)
	}
	if !exists {
		return resources, nil
	}
	if backing.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE || strings.TrimSpace(backing.ID) == "" {
		return nil, dependencyResolutionFailed(ctx,
			fmt.Errorf("relation type %s has an incomplete backing data source", relationTypeID))
	}
	if err := validateStandaloneResourceID(backing.ID); err != nil {
		return nil, invalidQuery(ctx, err.Error())
	}
	// Do not preempt Vega's resource-to-catalog fallback with a direct Safe
	// resource check. The downstream resource query carries the same caller and
	// enforces query_data before any physical read.
	return resources, nil
}

func indirectBackingResource(relationType interfaces.RelationType) (interfaces.ResourceInfo, bool, error) {
	switch rules := relationType.MappingRules.(type) {
	case *interfaces.InDirectMapping:
		if rules == nil || rules.BackingDataSource == nil {
			return interfaces.ResourceInfo{}, true, fmt.Errorf("indirect relation has no backing data source")
		}
		return *rules.BackingDataSource, true, nil
	case interfaces.InDirectMapping:
		if rules.BackingDataSource == nil {
			return interfaces.ResourceInfo{}, true, fmt.Errorf("indirect relation has no backing data source")
		}
		return *rules.BackingDataSource, true, nil
	case map[string]any:
		if _, exists := rules["backing_data_source"]; !exists {
			return interfaces.ResourceInfo{}, false, nil
		}
		var decoded interfaces.InDirectMapping
		data, err := sonic.Marshal(rules)
		if err != nil {
			return interfaces.ResourceInfo{}, true, err
		}
		if err := sonic.Unmarshal(data, &decoded); err != nil {
			return interfaces.ResourceInfo{}, true, err
		}
		if decoded.BackingDataSource == nil {
			return interfaces.ResourceInfo{}, true, fmt.Errorf("indirect relation has no backing data source")
		}
		return *decoded.BackingDataSource, true, nil
	default:
		return interfaces.ResourceInfo{}, false, nil
	}
}

func allResourcesAllowed(allowed map[string]struct{}, resources []interfaces.PermissionResource) bool {
	for _, resource := range resources {
		if _, ok := allowed[permissionResourceKey(resource)]; !ok {
			return false
		}
	}
	return true
}

func permissionResourceKey(resource interfaces.PermissionResource) string {
	return resource.Type + "\x00" + resource.ID
}

func validateMetricScope(ctx context.Context, knID string, definition *interfaces.MetricDefinition) error {
	if definition == nil {
		return invalidQuery(ctx, "metric definition is required")
	}
	if definition.KnID != "" && definition.KnID != knID {
		return invalidQuery(ctx, "metric belongs to another knowledge network")
	}
	if definition.Branch != "" && definition.Branch != interfaces.MAIN_BRANCH {
		return invalidQuery(ctx, "metric is not from the published main model")
	}
	if definition.ScopeType != interfaces.ScopeTypeObjectType || strings.TrimSpace(definition.ScopeRef) == "" {
		return invalidQuery(ctx, "metric must reference a published object type")
	}
	return nil
}

func validateQueryIdentity(ctx context.Context, knID, branch, childID string) error {
	if branch != interfaces.MAIN_BRANCH {
		return invalidQuery(ctx, "only the published main branch can be queried")
	}
	if err := validateResourceID(knID, childID); err != nil {
		return invalidQuery(ctx, err.Error())
	}
	return nil
}

func validateResourceID(knID, childID string) error {
	if strings.TrimSpace(knID) == "" || strings.TrimSpace(childID) == "" {
		return fmt.Errorf("knowledge-network and child resource ids are required")
	}
	if strings.ContainsAny(knID, "/*") || strings.ContainsAny(childID, "/*") {
		return fmt.Errorf("authorization resource ids cannot contain slash or wildcard")
	}
	return nil
}

func validateStandaloneResourceID(resourceID string) error {
	if strings.TrimSpace(resourceID) == "" {
		return fmt.Errorf("data resource id is required")
	}
	if strings.Contains(resourceID, "*") {
		return fmt.Errorf("authorization resource ids cannot contain wildcard")
	}
	return nil
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func invalidQuery(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest,
		oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).WithErrorDetails(detail)
}

func dependencyResolutionFailed(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
		oerrors.OntologyQuery_InternalError_CheckPermissionFailed).WithErrorDetails(err.Error())
}

func queryPermissionDenied(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).WithErrorDetails(detail)
}
