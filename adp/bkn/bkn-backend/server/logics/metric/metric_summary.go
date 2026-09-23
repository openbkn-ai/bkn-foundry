// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package metric

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

const metricSummaryAuthorizationPredicateLimit = 1000

func metricScopeChildIDs(knID string, scope interfaces.PermissionResourceScope) []string {
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

func applyMetricSummaryScope(query *interfaces.MetricsListQueryParams, knID string,
	scope interfaces.PermissionResourceScope) bool {
	if scope.Unrestricted {
		return true
	}
	allowed := metricScopeChildIDs(knID, scope)
	if len(allowed) == 0 {
		return false
	}
	if query.MetricIDs == nil {
		query.MetricIDs = allowed
		return true
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	filtered := make([]string, 0, len(query.MetricIDs))
	for _, id := range query.MetricIDs {
		if _, ok := allowedSet[id]; ok {
			filtered = append(filtered, id)
		}
	}
	query.MetricIDs = filtered
	return len(filtered) > 0
}

func (ms *metricService) listMetricSummaries(ctx context.Context,
	query interfaces.MetricsListQueryParams) (*interfaces.MetricsList, error) {
	if query.Branch == "" {
		query.Branch = interfaces.MAIN_BRANCH
	}
	if interfaces.IsAuthorizationResourceCatalog(ctx) {
		return ms.listMetricSummaryPage(ctx, query, false)
	}
	query.ValidAuthorizationIDsOnly = true
	scope, err := ms.ps.ListAccessibleResources(ctx, interfaces.RESOURCE_TYPE_METRIC,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	if scope.RequiresCandidateFilter {
		return ms.listMetricSummaryFallback(ctx, query)
	}
	pageQuery := query
	if !applyMetricSummaryScope(&pageQuery, query.KNID, scope) {
		return &interfaces.MetricsList{Entries: []*interfaces.MetricDefinition{}}, nil
	}
	if !scope.Unrestricted && len(pageQuery.MetricIDs) > metricSummaryAuthorizationPredicateLimit {
		return ms.listMetricSummaryFallback(ctx, query)
	}
	return ms.listMetricSummaryPage(ctx, pageQuery, true)
}

func (ms *metricService) listMetricSummaryPage(ctx context.Context,
	query interfaces.MetricsListQueryParams, hydrate bool) (*interfaces.MetricsList, error) {
	total, err := ms.ma.GetMetricsTotal(ctx, query)
	if err != nil {
		return nil, ms.metricSummaryError(ctx, err)
	}
	items, err := ms.ma.ListMetrics(ctx, query)
	if err != nil {
		return nil, ms.metricSummaryError(ctx, err)
	}
	if hydrate {
		items, err = ms.filterMetricSummaryOperations(ctx, query.KNID, items)
		if err != nil {
			return nil, err
		}
		ms.hydrateMetricSummaryAccounts(ctx, items)
	}
	return &interfaces.MetricsList{Entries: items, TotalCount: int64(total)}, nil
}

func (ms *metricService) listMetricSummaryFallback(ctx context.Context,
	query interfaces.MetricsListQueryParams) (*interfaces.MetricsList, error) {
	candidateQuery := query
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	candidates, err := ms.ma.ListMetrics(ctx, candidateQuery)
	if err != nil {
		return nil, ms.metricSummaryError(ctx, err)
	}
	candidates, err = ms.filterMetricSummaryOperations(ctx, query.KNID, candidates)
	if err != nil {
		return nil, err
	}
	total := len(candidates)
	page := permission.PaginateKNChildCandidates(candidates, query.Offset, query.Limit)
	ms.hydrateMetricSummaryAccounts(ctx, page)
	return &interfaces.MetricsList{Entries: page, TotalCount: int64(total)}, nil
}

func (ms *metricService) filterMetricSummaryOperations(ctx context.Context, knID string,
	items []*interfaces.MetricDefinition) ([]*interfaces.MetricDefinition, error) {
	validItems := make([]*interfaces.MetricDefinition, 0, len(items))
	childIDs := make([]string, 0, len(items))
	for _, item := range items {
		if !interfaces.IsValidAuthorizationID(item.ID) {
			continue
		}
		validItems = append(validItems, item)
		childIDs = append(childIDs, item.ID)
	}
	if len(validItems) == 0 {
		return []*interfaces.MetricDefinition{}, nil
	}
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, err
	}
	operations, err := permission.FilterKNChildResourceIDsWithOperations(ctx, ms.ps,
		interfaces.RESOURCE_TYPE_METRIC, interfaces.KNChildResourceIDs(knID, childIDs),
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visible := make([]*interfaces.MetricDefinition, 0, len(validItems))
	for _, item := range validItems {
		resourceOps, ok := operations[interfaces.KNChildResourceID(knID, item.ID)]
		if !ok {
			continue
		}
		item.Operations = resourceOps.Operations
		visible = append(visible, item)
	}
	return visible, nil
}

func (ms *metricService) hydrateMetricSummaryAccounts(ctx context.Context,
	items []*interfaces.MetricDefinition) {
	if len(items) == 0 || ms.uma == nil {
		return
	}
	infos := make([]*interfaces.AccountInfo, 0, len(items)*2)
	for _, item := range items {
		infos = append(infos, &item.Creator, &item.Updater)
	}
	_ = ms.uma.GetAccountNames(ctx, infos)
}

func (ms *metricService) metricSummaryError(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusInternalServerError,
		berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
}
