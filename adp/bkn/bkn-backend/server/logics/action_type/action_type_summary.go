// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

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
// parameter budget. Larger or wildcard scopes use Safe's candidate filter.
const actionSummaryAuthorizationPredicateLimit = 1000

func actionScopeChildIDs(knID string, scope interfaces.PermissionResourceScope) []string {
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

func intersectActionSummaryIDs(requested, allowed []string) []string {
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

func applyActionSummaryScope(query *interfaces.ActionTypesQueryParams, knID string,
	scope interfaces.PermissionResourceScope) bool {
	if scope.Unrestricted {
		return true
	}
	visibleIDs := actionScopeChildIDs(knID, scope)
	if len(visibleIDs) == 0 {
		return false
	}
	query.ATIDs = intersectActionSummaryIDs(query.ATIDs, visibleIDs)
	return len(query.ATIDs) > 0
}

// ListActionTypeSummaries resolves authorization before storage count and
// pagination. Only the returned page is hydrated with operations and names.
func (ats *actionTypeService) ListActionTypeSummaries(ctx context.Context,
	query interfaces.ActionTypesQueryParams) ([]*interfaces.ActionType, int, error) {
	if query.Branch == "" {
		query.Branch = interfaces.MAIN_BRANCH
	}
	if interfaces.IsAuthorizationResourceCatalog(ctx) {
		return ats.listActionSummaryPage(ctx, query, false)
	}
	query.ValidAuthorizationIDsOnly = true

	scope, err := ats.ps.ListAccessibleResources(ctx, interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, 0, err
	}
	if scope.RequiresCandidateFilter {
		return ats.listActionSummaryFallback(ctx, query)
	}
	pageQuery := query
	if !applyActionSummaryScope(&pageQuery, query.KNID, scope) {
		return []*interfaces.ActionType{}, 0, nil
	}
	if !scope.Unrestricted && len(pageQuery.ATIDs) > actionSummaryAuthorizationPredicateLimit {
		return ats.listActionSummaryFallback(ctx, query)
	}
	return ats.listActionSummaryPage(ctx, pageQuery, true)
}

func (ats *actionTypeService) listActionSummaryPage(ctx context.Context,
	query interfaces.ActionTypesQueryParams, hydrate bool) ([]*interfaces.ActionType, int, error) {
	total, err := ats.ata.GetActionTypesTotal(ctx, query)
	if err != nil {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}
	items, err := ats.ata.ListActionTypeSummaries(ctx, query)
	if err != nil {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}
	if !hydrate || len(items) == 0 {
		return items, total, nil
	}
	items, err = ats.filterActionSummaryOperations(ctx, query.KNID, items)
	if err != nil {
		return nil, 0, err
	}
	if err := ats.hydrateActionSummaries(ctx, query.KNID, query.Branch, items); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (ats *actionTypeService) listActionSummaryFallback(ctx context.Context,
	query interfaces.ActionTypesQueryParams) ([]*interfaces.ActionType, int, error) {
	candidateQuery := query
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	candidates, err := ats.ata.ListActionTypeSummaries(ctx, candidateQuery)
	if err != nil {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}
	candidates, err = ats.filterActionSummaryOperations(ctx, query.KNID, candidates)
	if err != nil {
		return nil, 0, err
	}
	total := len(candidates)
	page := permission.PaginateKNChildCandidates(candidates, query.Offset, query.Limit)
	if err := ats.hydrateActionSummaries(ctx, query.KNID, query.Branch, page); err != nil {
		return nil, 0, err
	}
	return page, total, nil
}

func (ats *actionTypeService) filterActionSummaryOperations(ctx context.Context, knID string,
	items []*interfaces.ActionType) ([]*interfaces.ActionType, error) {
	validItems := make([]*interfaces.ActionType, 0, len(items))
	childIDs := make([]string, 0, len(items))
	for _, item := range items {
		if !interfaces.IsValidAuthorizationID(item.ATID) {
			continue
		}
		validItems = append(validItems, item)
		childIDs = append(childIDs, item.ATID)
	}
	if len(validItems) == 0 {
		return []*interfaces.ActionType{}, nil
	}
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, err
	}
	operations, err := permission.FilterKNChildResourceIDsWithOperations(ctx, ats.ps,
		interfaces.RESOURCE_TYPE_ACTION_TYPE, interfaces.KNChildResourceIDs(knID, childIDs),
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visible := make([]*interfaces.ActionType, 0, len(validItems))
	for _, item := range validItems {
		resourceID := interfaces.KNChildResourceID(knID, item.ATID)
		resourceOps, ok := operations[resourceID]
		if !ok {
			continue
		}
		item.Operations = resourceOps.Operations
		visible = append(visible, item)
	}
	return visible, nil
}

func (ats *actionTypeService) hydrateActionSummaries(ctx context.Context, knID, branch string,
	items []*interfaces.ActionType) error {
	if len(items) == 0 {
		return nil
	}
	objectTypeIDs := make([]string, 0, len(items))
	for _, item := range items {
		objectTypeIDs = append(objectTypeIDs, item.ObjectTypeID)
	}
	objectTypeMap, err := ats.ots.GetObjectTypesMapByIDs(ctx, knID, branch,
		common.DuplicateSlice(objectTypeIDs), false)
	if err != nil {
		return err
	}
	accounts := make([]*interfaces.AccountInfo, 0, len(items)*2)
	for _, item := range items {
		if objectType := objectTypeMap[item.ObjectTypeID]; objectType != nil {
			item.ObjectType = interfaces.SimpleObjectType{
				OTID: objectType.OTID, OTName: objectType.OTName,
				Icon: objectType.Icon, Color: objectType.Color,
			}
		}
		accounts = append(accounts, &item.Creator, &item.Updater)
	}
	if err := ats.ums.GetAccountNames(ctx, accounts); err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}
	return nil
}
