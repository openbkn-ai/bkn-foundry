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
		businessDomain = strings.TrimSpace(traceContext.Baggage["business_domain"])
	}
	return bkntrace.RequestContext{
		RequestID:      traceContext.RequestID,
		AccountID:      accountInfo.ID,
		AccountType:    accountInfo.Type,
		BusinessDomain: businessDomain,
	}
}

func emitResourceReadEvidence(c *gin.Context, ctx context.Context, operation string, resources []*interfaces.Resource, total int64, queryShape any) {
	if len(resources) == 0 {
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
	bkntrace.EmitDataQueryEvents(ctx, vegaTraceRequestContext(c, ctx), subject, bkntrace.ResourceRefs(resources))
}

func emitResourceDataEvidence(c *gin.Context, ctx context.Context, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams, result *interfaces.ResourceDataQueryResult) {
	if resource == nil || result == nil {
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
	bkntrace.EmitDataQueryEvents(ctx, vegaTraceRequestContext(c, ctx), subject, refs)
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
		"catalog_id":              params.CatalogID,
		"category":                params.Category,
		"status":                  params.Status,
		"database":                params.Database,
		"offset":                  params.Offset,
		"limit":                   params.Limit,
		"sort":                    params.Sort,
		"direction":               params.Direction,
		"extension_keys_hash":     bkntrace.HashValue(params.ExtensionKeys),
		"extension_values_hash":   bkntrace.HashValue(params.ExtensionValues),
		"include_extensions":      params.IncludeExtensions,
		"include_extension_keys":  params.IncludeExtensionKeys,
		"name_filter_present":     strings.TrimSpace(params.Name) != "",
		"catalog_filter_present":  strings.TrimSpace(params.CatalogID) != "",
		"database_filter_present": strings.TrimSpace(params.Database) != "",
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
