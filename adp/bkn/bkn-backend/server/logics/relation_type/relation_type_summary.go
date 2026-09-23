// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// Keep authorization-derived predicates below a conservative database
// parameter budget. Larger scopes use Safe's chunked candidate-filter path.
const relationSummaryAuthorizationPredicateLimit = 1000

func relationScopeChildIDs(knID string, scope interfaces.PermissionResourceScope) []string {
	if scope.Unrestricted {
		return nil
	}
	ids := make([]string, 0, len(scope.ResourceIDs))
	seen := make(map[string]struct{}, len(scope.ResourceIDs))
	for _, resourceID := range scope.ResourceIDs {
		childID, ok := interfaces.KNChildIDFromResourceID(knID, resourceID)
		if !ok {
			continue
		}
		if _, exists := seen[childID]; exists {
			continue
		}
		seen[childID] = struct{}{}
		ids = append(ids, childID)
	}
	return ids
}

func intersectRelationSummaryIDs(requested, allowed []string) []string {
	if requested == nil {
		return append([]string(nil), allowed...)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	result := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := allowedSet[id]; ok {
			result = append(result, id)
		}
	}
	return result
}

func applyRelationSummaryScopes(query *interfaces.RelationTypesQueryParams, knID string,
	relationScope, objectScope interfaces.PermissionResourceScope) bool {
	if !relationScope.Unrestricted {
		visibleRelationIDs := relationScopeChildIDs(knID, relationScope)
		if len(visibleRelationIDs) == 0 {
			return false
		}
		query.RTIDS = intersectRelationSummaryIDs(query.RTIDS, visibleRelationIDs)
		if len(query.RTIDS) == 0 {
			return false
		}
	}
	if !objectScope.Unrestricted {
		visibleObjectIDs := relationScopeChildIDs(knID, objectScope)
		if len(visibleObjectIDs) == 0 {
			return false
		}
		query.SourceObjectTypeIDs = intersectRelationSummaryIDs(query.SourceObjectTypeIDs, visibleObjectIDs)
		query.TargetObjectTypeIDs = intersectRelationSummaryIDs(query.TargetObjectTypeIDs, visibleObjectIDs)
		if len(query.SourceObjectTypeIDs) == 0 || len(query.TargetObjectTypeIDs) == 0 {
			return false
		}
		if query.BoundObjectTypeIDs != nil {
			query.BoundObjectTypeIDs = intersectRelationSummaryIDs(query.BoundObjectTypeIDs, visibleObjectIDs)
			if len(query.BoundObjectTypeIDs) == 0 {
				return false
			}
		}
	}
	return true
}

func relationSummaryAuthorizationPredicateCount(query interfaces.RelationTypesQueryParams,
	relationRestricted, objectRestricted bool) int {
	count := 0
	if relationRestricted {
		count += len(query.RTIDS)
	}
	if objectRestricted {
		count += len(query.SourceObjectTypeIDs) + len(query.TargetObjectTypeIDs) +
			2*len(query.BoundObjectTypeIDs)
	}
	return count
}

// ListRelationTypeSummaries resolves authorization scopes before storage count
// and pagination. Only the returned page is hydrated with operations, endpoint
// names, and account names.
func (rts *relationTypeService) ListRelationTypeSummaries(ctx context.Context,
	query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, int, error) {
	if query.Branch == "" {
		query.Branch = interfaces.MAIN_BRANCH
	}
	if interfaces.IsAuthorizationResourceCatalog(ctx) {
		return rts.listRelationSummaryPage(ctx, query, false)
	}
	query.ValidAuthorizationIDsOnly = true

	relationScope, err := rts.ps.ListAccessibleResources(ctx, interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, 0, err
	}
	objectScope, err := rts.ps.ListAccessibleResourcesWithAnyOperation(ctx,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE)
	if err != nil {
		return nil, 0, err
	}
	if relationScope.RequiresCandidateFilter || objectScope.RequiresCandidateFilter {
		return rts.listRelationSummaryFallback(ctx, query, relationScope, objectScope)
	}
	pageQuery := query
	if !applyRelationSummaryScopes(&pageQuery, query.KNID, relationScope, objectScope) {
		return []*interfaces.RelationType{}, 0, nil
	}
	if relationSummaryAuthorizationPredicateCount(pageQuery, !relationScope.Unrestricted,
		!objectScope.Unrestricted) > relationSummaryAuthorizationPredicateLimit {
		fallbackRelationScope := relationScope
		fallbackRelationScope.RequiresCandidateFilter = true
		fallbackObjectScope := objectScope
		fallbackObjectScope.RequiresCandidateFilter = true
		return rts.listRelationSummaryFallback(ctx, query, fallbackRelationScope, fallbackObjectScope)
	}
	return rts.listRelationSummaryPage(ctx, pageQuery, true)
}

func (rts *relationTypeService) listRelationSummaryPage(ctx context.Context,
	query interfaces.RelationTypesQueryParams, hydrate bool) ([]*interfaces.RelationType, int, error) {
	total, err := rts.rta.GetRelationTypesTotal(ctx, query)
	if err != nil {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}
	items, err := rts.rta.ListRelationTypeSummaries(ctx, query)
	if err != nil {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}
	if !hydrate || len(items) == 0 {
		return items, total, nil
	}
	items, err = rts.filterRelationSummaryOperations(ctx, query.KNID, items)
	if err != nil {
		return nil, 0, err
	}
	if err := rts.hydrateRelationSummaries(ctx, query.KNID, query.Branch, items); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (rts *relationTypeService) listRelationSummaryFallback(ctx context.Context,
	query interfaces.RelationTypesQueryParams, relationScope,
	objectScope interfaces.PermissionResourceScope) ([]*interfaces.RelationType, int, error) {
	candidateQuery := query
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	if !relationScope.RequiresCandidateFilter || !objectScope.RequiresCandidateFilter {
		boundedRelationScope := relationScope
		boundedObjectScope := objectScope
		if relationScope.RequiresCandidateFilter {
			boundedRelationScope = interfaces.PermissionResourceScope{Unrestricted: true}
		}
		if objectScope.RequiresCandidateFilter {
			boundedObjectScope = interfaces.PermissionResourceScope{Unrestricted: true}
		}
		if !applyRelationSummaryScopes(&candidateQuery, query.KNID, boundedRelationScope, boundedObjectScope) {
			return []*interfaces.RelationType{}, 0, nil
		}
	}
	candidates, err := rts.rta.ListRelationTypeSummaries(ctx, candidateQuery)
	if err != nil {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}
	if relationScope.RequiresCandidateFilter {
		candidates, err = rts.filterRelationSummaryOperations(ctx, query.KNID, candidates)
		if err != nil {
			return nil, 0, err
		}
	}
	if objectScope.RequiresCandidateFilter {
		candidates, err = rts.withVisibleEndpoints(ctx, query.KNID, candidates)
		if err != nil {
			return nil, 0, err
		}
	}
	total := len(candidates)
	page := permission.PaginateKNChildCandidates(candidates, query.Offset, query.Limit)
	if !relationScope.RequiresCandidateFilter {
		page, err = rts.filterRelationSummaryOperations(ctx, query.KNID, page)
		if err != nil {
			return nil, 0, err
		}
	}
	if err := rts.hydrateRelationSummaries(ctx, query.KNID, query.Branch, page); err != nil {
		return nil, 0, err
	}
	return page, total, nil
}

func (rts *relationTypeService) filterRelationSummaryOperations(ctx context.Context, knID string,
	items []*interfaces.RelationType) ([]*interfaces.RelationType, error) {
	validItems := make([]*interfaces.RelationType, 0, len(items))
	childIDs := make([]string, 0, len(items))
	for _, item := range items {
		if !interfaces.IsValidAuthorizationID(item.RTID) {
			continue
		}
		validItems = append(validItems, item)
		childIDs = append(childIDs, item.RTID)
	}
	if len(validItems) == 0 {
		return []*interfaces.RelationType{}, nil
	}
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, err
	}
	operations, err := permission.FilterKNChildResourceIDsWithOperations(ctx, rts.ps,
		interfaces.RESOURCE_TYPE_RELATION_TYPE, interfaces.KNChildResourceIDs(knID, childIDs),
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visible := make([]*interfaces.RelationType, 0, len(validItems))
	for _, item := range validItems {
		resourceID := interfaces.KNChildResourceID(knID, item.RTID)
		resourceOps, ok := operations[resourceID]
		if !ok {
			continue
		}
		item.Operations = resourceOps.Operations
		visible = append(visible, item)
	}
	return visible, nil
}

func (rts *relationTypeService) hydrateRelationSummaries(ctx context.Context, knID, branch string,
	items []*interfaces.RelationType) error {
	if len(items) == 0 {
		return nil
	}
	objectTypeIDs := make([]string, 0, len(items)*2)
	for _, item := range items {
		objectTypeIDs = append(objectTypeIDs, item.SourceObjectTypeID, item.TargetObjectTypeID)
	}
	objectTypeMap, err := rts.ots.GetObjectTypesMapByIDs(ctx, knID, branch,
		common.DuplicateSlice(objectTypeIDs), false)
	if err != nil {
		return err
	}
	accounts := make([]*interfaces.AccountInfo, 0, len(items)*2)
	for _, item := range items {
		if source := objectTypeMap[item.SourceObjectTypeID]; source != nil {
			item.SourceObjectType = interfaces.SimpleObjectType{
				OTID: source.OTID, OTName: source.OTName, Icon: source.Icon, Color: source.Color,
			}
		}
		if target := objectTypeMap[item.TargetObjectTypeID]; target != nil {
			item.TargetObjectType = interfaces.SimpleObjectType{
				OTID: target.OTID, OTName: target.OTName, Icon: target.Icon, Color: target.Color,
			}
		}
		accounts = append(accounts, &item.Creator, &item.Updater)
	}
	if err := rts.ums.GetAccountNames(ctx, accounts); err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}
	return nil
}
