// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package concept_group

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

const conceptGroupSummaryAuthorizationPredicateLimit = 1000

func conceptGroupScopeChildIDs(knID string, scope interfaces.PermissionResourceScope) []string {
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

func intersectConceptGroupSummaryIDs(requested, allowed []string) []string {
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

func applyConceptGroupSummaryScope(query *interfaces.ConceptGroupsQueryParams, knID string,
	scope interfaces.PermissionResourceScope) bool {
	if scope.Unrestricted {
		return true
	}
	visibleIDs := conceptGroupScopeChildIDs(knID, scope)
	if len(visibleIDs) == 0 {
		return false
	}
	query.CGIDs = intersectConceptGroupSummaryIDs(query.CGIDs, visibleIDs)
	return len(query.CGIDs) > 0
}

// ListConceptGroupSummaries resolves authorization before storage count and pagination.
// It loads memberships once for the returned page instead of once per group.
func (cgs *conceptGroupService) ListConceptGroupSummaries(ctx context.Context,
	query interfaces.ConceptGroupsQueryParams) ([]*interfaces.ConceptGroup, int, []string, error) {
	if query.Branch == "" {
		query.Branch = interfaces.MAIN_BRANCH
	}
	if interfaces.IsAuthorizationResourceCatalog(ctx) {
		return cgs.listConceptGroupSummaryPage(ctx, query, false)
	}
	query.ValidAuthorizationIDsOnly = true
	scope, err := cgs.ps.ListAccessibleResources(ctx, interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, 0, nil, err
	}
	if scope.RequiresCandidateFilter {
		return cgs.listConceptGroupSummaryFallback(ctx, query)
	}
	pageQuery := query
	if !applyConceptGroupSummaryScope(&pageQuery, query.KNID, scope) {
		return []*interfaces.ConceptGroup{}, 0, []string{}, nil
	}
	if !scope.Unrestricted && len(pageQuery.CGIDs) > conceptGroupSummaryAuthorizationPredicateLimit {
		return cgs.listConceptGroupSummaryFallback(ctx, query)
	}
	return cgs.listConceptGroupSummaryPage(ctx, pageQuery, true)
}

func (cgs *conceptGroupService) listConceptGroupSummaryPage(ctx context.Context,
	query interfaces.ConceptGroupsQueryParams, hydrate bool) ([]*interfaces.ConceptGroup, int, []string, error) {
	total, err := cgs.cga.GetConceptGroupsTotal(ctx, query)
	if err != nil {
		return nil, 0, nil, cgs.conceptGroupSummaryError(ctx, err)
	}
	items, err := cgs.cga.ListConceptGroups(ctx, query)
	if err != nil {
		return nil, 0, nil, cgs.conceptGroupSummaryError(ctx, err)
	}
	tagQuery := query
	tagQuery.Tag = ""
	tags, err := cgs.cga.ListConceptGroupTags(ctx, tagQuery)
	if err != nil {
		return nil, 0, nil, cgs.conceptGroupSummaryError(ctx, err)
	}
	if !hydrate || len(items) == 0 {
		return items, total, tags, nil
	}
	items, err = cgs.filterConceptGroupSummaryOperations(ctx, query.KNID, items)
	if err != nil {
		return nil, 0, nil, err
	}
	if err := cgs.hydrateConceptGroupSummaries(ctx, query.KNID, query.Branch, items); err != nil {
		return nil, 0, nil, err
	}
	return items, total, tags, nil
}

func (cgs *conceptGroupService) listConceptGroupSummaryFallback(ctx context.Context,
	query interfaces.ConceptGroupsQueryParams) ([]*interfaces.ConceptGroup, int, []string, error) {
	candidateQuery := query
	candidateQuery.Tag = ""
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	candidates, err := cgs.cga.ListConceptGroups(ctx, candidateQuery)
	if err != nil {
		return nil, 0, nil, cgs.conceptGroupSummaryError(ctx, err)
	}
	candidates, err = cgs.filterConceptGroupSummaryOperations(ctx, query.KNID, candidates)
	if err != nil {
		return nil, 0, nil, err
	}
	tags := collectConceptGroupSummaryTags(candidates)
	if query.Tag != "" {
		candidates = filterConceptGroupSummariesByTag(candidates, query.Tag)
	}
	total := len(candidates)
	page := permission.PaginateKNChildCandidates(candidates, query.Offset, query.Limit)
	if err := cgs.hydrateConceptGroupSummaries(ctx, query.KNID, query.Branch, page); err != nil {
		return nil, 0, nil, err
	}
	return page, total, tags, nil
}

func filterConceptGroupSummariesByTag(items []*interfaces.ConceptGroup,
	tag string) []*interfaces.ConceptGroup {
	filtered := make([]*interfaces.ConceptGroup, 0, len(items))
	for _, item := range items {
		for _, itemTag := range item.Tags {
			if itemTag == tag {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func collectConceptGroupSummaryTags(items []*interfaces.ConceptGroup) []string {
	uniqueTags := make(map[string]struct{})
	for _, item := range items {
		for _, tag := range item.Tags {
			uniqueTags[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(uniqueTags))
	for tag := range uniqueTags {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func (cgs *conceptGroupService) filterConceptGroupSummaryOperations(ctx context.Context, knID string,
	items []*interfaces.ConceptGroup) ([]*interfaces.ConceptGroup, error) {
	validItems := make([]*interfaces.ConceptGroup, 0, len(items))
	childIDs := make([]string, 0, len(items))
	for _, item := range items {
		if !interfaces.IsValidAuthorizationID(item.CGID) {
			continue
		}
		validItems = append(validItems, item)
		childIDs = append(childIDs, item.CGID)
	}
	if len(validItems) == 0 {
		return []*interfaces.ConceptGroup{}, nil
	}
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, knID, childIDs); err != nil {
		return nil, err
	}
	operations, err := permission.FilterKNChildResourceIDsWithOperations(ctx, cgs.ps,
		interfaces.RESOURCE_TYPE_CONCEPT_GROUP, interfaces.KNChildResourceIDs(knID, childIDs),
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visible := make([]*interfaces.ConceptGroup, 0, len(validItems))
	for _, item := range validItems {
		resourceOps, ok := operations[interfaces.KNChildResourceID(knID, item.CGID)]
		if !ok {
			continue
		}
		item.Operations = resourceOps.Operations
		visible = append(visible, item)
	}
	return visible, nil
}

func (cgs *conceptGroupService) hydrateConceptGroupSummaries(ctx context.Context, knID, branch string,
	items []*interfaces.ConceptGroup) error {
	if len(items) == 0 {
		return nil
	}
	groupIDs := make([]string, 0, len(items))
	accountInfos := make([]*interfaces.AccountInfo, 0, len(items)*2)
	for _, item := range items {
		groupIDs = append(groupIDs, item.CGID)
		accountInfos = append(accountInfos, &item.Creator, &item.Updater)
	}
	if err := cgs.ums.GetAccountNames(ctx, accountInfos); err != nil {
		return cgs.conceptGroupSummaryError(ctx, err)
	}
	membersByGroup, err := cgs.cga.GetConceptIDsGroupedByConceptGroupIDs(ctx, knID, branch,
		groupIDs, interfaces.MODULE_TYPE_OBJECT_TYPE)
	if err != nil {
		return cgs.conceptGroupSummaryError(ctx, err)
	}
	allMembers := make([]string, 0)
	for _, groupID := range groupIDs {
		allMembers = append(allMembers, membersByGroup[groupID]...)
	}
	scope, err := cgs.loadMemberScope(ctx, knID, branch, allMembers)
	if err != nil {
		return err
	}
	for _, item := range items {
		item.ObjectTypeIDs, item.Statistics = scope.apply(membersByGroup[item.CGID])
	}
	return nil
}

func (cgs *conceptGroupService) conceptGroupSummaryError(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusInternalServerError,
		berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(fmt.Sprint(err))
}
