// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"vega-backend/common"
	"vega-backend/common/bkntrace"
	"vega-backend/interfaces"
)

func vegaTraceRequestContext(c *gin.Context, ctx context.Context) bkntrace.RequestContext {
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	accountInfo, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	businessDomain := strings.TrimSpace(c.GetHeader("x-business-domain"))
	if businessDomain == "" {
		businessDomain = strings.TrimSpace(traceContext.BusinessDomain)
	}
	return bkntrace.RequestContext{
		RequestID:          traceContext.RequestID,
		AccountID:          accountInfo.ID,
		AccountType:        accountInfo.Type,
		TenantID:           strings.TrimSpace(c.GetHeader("x-tenant-id")),
		BusinessDomain:     businessDomain,
		InteractionID:      traceContext.InteractionID,
		OperationID:        traceContext.OperationID,
		CausationEventID:   traceContext.CausationEventID,
		ClaimID:            traceContext.ClaimID,
		Attempt:            traceContext.Attempt,
		ObservedAt:         traceContext.ObservedAt,
		ObservedAtProvided: traceContext.ObservedAtProvided,
	}
}

func emitResourceReadEvidence(c *gin.Context, ctx context.Context, operation string, resources []*interfaces.Resource, total int64, queryShape any) {
	if !bkntrace.EvidenceEnabled() || len(resources) == 0 {
		return
	}
	subject := bkntrace.DataQuerySubject{
		Operation:     operation,
		QueryHash:     bkntrace.HashValue(queryShape),
		ReturnedCount: len(resources),
		TotalCount:    total,
	}
	if len(resources) == 1 && resources[0] != nil {
		subject.ResourceID = resources[0].ID
		subject.CatalogID = resources[0].CatalogID
	}
	if eventID := bkntrace.EmitDataQueryEvents(ctx, vegaTraceRequestContext(c, ctx), subject, bkntrace.ResourceRefs(resources)); eventID != "" {
		c.Header("bkn-evidence-event-id", eventID)
	}
}

func emitResourceDataEvidence(c *gin.Context, ctx context.Context, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams, result *interfaces.ResourceDataQueryResult) {
	if !bkntrace.EvidenceEnabled() || resource == nil || result == nil {
		return
	}
	refs := append(bkntrace.ResourceRefs([]*interfaces.Resource{resource}), bkntrace.ResourceRowRefs(resource, result.Entries)...)
	subject := bkntrace.DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    resource.ID,
		CatalogID:     resource.CatalogID,
		QueryHash:     bkntrace.HashValue(safeResourceDataQueryShape(params)),
		ReturnedCount: len(result.Entries),
		TotalCount:    result.TotalCount,
		Truncated:     resourceDataEvidenceTruncated(result),
	}
	queryContent, resultContent := resourceDataArtifactContent(resource, params, result)
	if eventID := bkntrace.EmitDataQueryEvidence(
		ctx, vegaTraceRequestContext(c, ctx), subject, refs, queryContent, resultContent,
	); eventID != "" {
		c.Header("bkn-evidence-event-id", eventID)
	}
}

func emitRawQueryEvidence(c *gin.Context, ctx context.Context, req *interfaces.RawQueryRequest, resp *interfaces.RawQueryResponse) {
	if !bkntrace.EvidenceEnabled() || req == nil || resp == nil {
		return
	}
	subject, refs := rawQueryEvidenceDetails(req, resp)
	if len(resp.ResourceIDs) == 1 {
		subject.ResourceID = strings.TrimSpace(resp.ResourceIDs[0])
	}
	queryContent, resultContent := rawQueryArtifactContent(req, resp)
	if eventID := bkntrace.EmitDataQueryEvidence(
		ctx, vegaTraceRequestContext(c, ctx), subject, refs, queryContent, resultContent,
	); eventID != "" {
		c.Header("bkn-evidence-event-id", eventID)
	}
}

func resourceDataArtifactContent(
	resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams,
	result *interfaces.ResourceDataQueryResult,
) (map[string]any, map[string]any) {
	queryContent := map[string]any{
		"resource_id": resource.ID,
		"catalog_id":  resource.CatalogID,
	}
	if params != nil {
		queryContent["offset"] = params.Offset
		queryContent["limit"] = params.Limit
		queryContent["paging"] = params.Paging
		queryContent["sort"] = params.Sort
		queryContent["filter_condition"] = params.FilterCondition
		queryContent["output_fields"] = params.OutputFields
		queryContent["need_total"] = params.NeedTotal
		queryContent["query_type"] = params.QueryType
		queryContent["aggregation"] = params.Aggregation
		queryContent["group_by"] = params.GroupBy
		queryContent["having"] = params.Having
	}
	resultContent := map[string]any{
		"entries":     result.Entries,
		"total_count": result.TotalCount,
		"paging":      result.Paging,
		"truncated":   resourceDataEvidenceTruncated(result),
	}
	return queryContent, resultContent
}

func rawQueryArtifactContent(
	req *interfaces.RawQueryRequest,
	resp *interfaces.RawQueryResponse,
) (map[string]any, map[string]any) {
	queryContent := map[string]any{}
	if req != nil {
		queryContent["query"] = req.Query
		queryContent["query_format"] = req.QueryFormat
		queryContent["input_dialect"] = req.InputDialect
		queryContent["paging"] = req.Paging
		queryContent["query_timeout_sec"] = req.QueryTimeoutSec
		queryContent["need_total"] = req.NeedTotal
	}
	resultContent := map[string]any{}
	if resp != nil {
		resultContent["columns"] = resp.Columns
		resultContent["entries"] = resp.Entries
		resultContent["total_count"] = resp.TotalCount
		resultContent["warnings"] = resp.Warnings
		resultContent["paging"] = resp.Paging
	}
	return queryContent, resultContent
}

func rawQueryEvidenceDetails(
	req *interfaces.RawQueryRequest,
	resp *interfaces.RawQueryResponse,
) (bkntrace.DataQuerySubject, []bkntrace.EvidenceRef) {
	subject := bkntrace.DataQuerySubject{Operation: "data.raw_query"}
	if req != nil {
		subject.QueryHash = bkntrace.HashValue(req.Query)
	}
	if resp == nil {
		return subject, nil
	}
	subject.ReturnedCount = len(resp.Entries)
	if resp.TotalCount != nil {
		subject.TotalCount = *resp.TotalCount
	}
	subject.Truncated = resp.Paging != nil && resp.Paging.NextCursor != nil
	if !subject.Truncated && subject.TotalCount > 0 {
		subject.Truncated = int64(subject.ReturnedCount) < subject.TotalCount
	}
	refs := make([]bkntrace.EvidenceRef, 0, len(resp.ResourceIDs))
	for _, resourceID := range resp.ResourceIDs {
		if resourceID = strings.TrimSpace(resourceID); resourceID != "" {
			refs = append(refs, bkntrace.EvidenceRef{
				RefID:   "resource:" + resourceID,
				RefType: bkntrace.RefTypeResource,
			})
		}
	}
	return subject, refs
}

func resourceDataEvidenceTruncated(result *interfaces.ResourceDataQueryResult) bool {
	if result == nil {
		return false
	}
	if result.Paging != nil && result.Paging.NextCursor != nil {
		return true
	}
	return result.TotalCount > 0 && int64(len(result.Entries)) < result.TotalCount
}

func safeResourceListQueryShape(params interfaces.ResourcesQueryParams) map[string]any {
	return map[string]any{
		"catalog_id":             params.CatalogID,
		"category":               params.Category,
		"status":                 params.Status,
		"schema":                 params.Schema,
		"offset":                 params.Offset,
		"limit":                  params.Limit,
		"sort":                   params.Sort,
		"direction":              params.Direction,
		"name_filter_present":    strings.TrimSpace(params.Name) != "",
		"catalog_filter_present": strings.TrimSpace(params.CatalogID) != "",
		"schema_filter_present":  strings.TrimSpace(params.Schema) != "",
	}
}

func safeResourceIDsQueryShape(ids []string, ignoreMissing bool) map[string]any {
	return map[string]any{
		"ids_hash":       bkntrace.HashValue(ids),
		"id_count":       len(ids),
		"ignore_missing": ignoreMissing,
	}
}

func safeResourceDataQueryShape(params *interfaces.ResourceDataQueryParams) map[string]any {
	if params == nil {
		return nil
	}
	return map[string]any{
		"offset":                params.Offset,
		"limit":                 params.Limit,
		"paging_hash":           bkntrace.HashValue(params.Paging),
		"sort_hash":             bkntrace.HashValue(params.Sort),
		"filter_hash":           bkntrace.HashValue(params.FilterCondition),
		"output_fields_hash":    bkntrace.HashValue(params.OutputFields),
		"need_total":            params.NeedTotal,
		"format":                params.Format,
		"search_after_hash":     bkntrace.HashValue(params.SearchAfter),
		"query_type":            params.QueryType,
		"aggregation_hash":      bkntrace.HashValue(params.Aggregation),
		"group_by_hash":         bkntrace.HashValue(params.GroupBy),
		"having_hash":           bkntrace.HashValue(params.Having),
		"has_filter_condition":  params.FilterCondition != nil,
		"has_actual_filter_cfg": params.FilterCondCfg != nil,
	}
}
