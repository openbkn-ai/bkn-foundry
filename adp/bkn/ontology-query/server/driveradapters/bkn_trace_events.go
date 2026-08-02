// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"

	"ontology-query/common"
	"ontology-query/common/bkntrace"
	"ontology-query/interfaces"
)

const headerBKNEvidenceEventID = "bkn-evidence-event-id"

func ontologyTraceRequestContext(c *gin.Context, ctx context.Context, visitor hydra.Visitor) bkntrace.RequestContext {
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	businessDomain := strings.TrimSpace(c.GetHeader("x-business-domain"))
	if businessDomain == "" {
		businessDomain = strings.TrimSpace(traceContext.BusinessDomain)
	}
	return bkntrace.RequestContext{
		RequestID:              traceContext.RequestID,
		AccountID:              visitor.ID,
		AccountType:            string(visitor.Type),
		BusinessDomain:         businessDomain,
		TenantID:               strings.TrimSpace(c.GetHeader("x-tenant-id")),
		ApplicationPrincipalID: strings.TrimSpace(os.Getenv("BKN_TRACE_APPLICATION_PRINCIPAL_ID")),
		EffectiveSubjectID:     visitor.ID,
		EffectiveSubjectType:   ontologyTraceSubjectType(string(visitor.Type)),
		DelegationID:           strings.TrimSpace(c.GetHeader("x-bkn-delegation-id")),
		InteractionID:          traceContext.InteractionID,
		OperationID:            traceContext.OperationID,
		CausationEventID:       traceContext.CausationEventID,
		ClaimID:                traceContext.ClaimID,
		Attempt:                traceContext.Attempt,
		ObservedAt:             traceContext.ObservedAt,
	}
}

func ontologyTraceSubjectType(accountType string) string {
	if strings.EqualFold(accountType, "service") || strings.EqualFold(accountType, "app") {
		return "service"
	}
	return "user"
}

func emitObjectQueryEvidence(c *gin.Context, ctx context.Context, visitor hydra.Visitor, query *interfaces.ObjectQueryBaseOnObjectType, result *interfaces.Objects) {
	if !bkntrace.EvidenceEnabled() || result == nil {
		return
	}
	subject := bkntrace.DataQuerySubject{
		EntityKind:    bkntrace.EntityKindObjectInstance,
		Operation:     "bkn.object.query",
		KNID:          query.KNID,
		Branch:        query.Branch,
		SubjectID:     query.ObjectTypeID,
		QueryHash:     bkntrace.HashValue(safeObjectQueryShape(query)),
		ReturnedCount: len(result.Datas),
		TotalCount:    result.TotalCount,
		Truncated:     query.Limit > 0 && len(result.Datas) >= query.Limit,
	}
	eventID := bkntrace.EmitDataQueryEvents(ctx, ontologyTraceRequestContext(c, ctx, visitor), subject,
		bkntrace.ObjectRowRefs(query.KNID, query.Branch, query.ObjectTypeID, result.Datas))
	setEvidenceEventHeader(c, eventID)
}

func emitSubgraphEvidence(c *gin.Context, ctx context.Context, visitor hydra.Visitor, knID, branch, operation string, queryShape any, result *interfaces.ObjectSubGraph) {
	if !bkntrace.EvidenceEnabled() || result == nil {
		return
	}
	subject := bkntrace.DataQuerySubject{
		EntityKind:    bkntrace.EntityKindRelationPath,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		SubjectID:     "subgraph",
		QueryHash:     bkntrace.HashValue(queryShape),
		ReturnedCount: len(result.RelationPaths),
		TotalCount:    result.TotalCount,
		Truncated:     result.TotalCount > 0 && int64(len(result.RelationPaths)) < result.TotalCount,
	}
	eventID := bkntrace.EmitDataQueryEvents(ctx, ontologyTraceRequestContext(c, ctx, visitor), subject,
		bkntrace.SubgraphRefs(knID, branch, result))
	setEvidenceEventHeader(c, eventID)
}

func emitSubgraphEntriesEvidence(c *gin.Context, ctx context.Context, visitor hydra.Visitor, knID, branch string, queryShape any, result interfaces.PathsEntries) {
	if !bkntrace.EvidenceEnabled() {
		return
	}
	refs := make([]bkntrace.EvidenceRef, 0)
	for i := range result.Entries {
		refs = append(refs, bkntrace.SubgraphRefs(knID, branch, &result.Entries[i])...)
	}
	eventID := bkntrace.EmitDataQueryEvents(ctx, ontologyTraceRequestContext(c, ctx, visitor), bkntrace.DataQuerySubject{
		EntityKind:    bkntrace.EntityKindRelationPath,
		Operation:     "bkn.relation.query",
		KNID:          knID,
		Branch:        branch,
		SubjectID:     "subgraph_type_path",
		QueryHash:     bkntrace.HashValue(queryShape),
		ReturnedCount: len(result.Entries),
	}, refs)
	setEvidenceEventHeader(c, eventID)
}

func emitMetricEvidence(c *gin.Context, traceCtx context.Context, visitor hydra.Visitor, knID, branch, metricID, operation string, queryShape any, result *interfaces.MetricData) {
	if !bkntrace.EvidenceEnabled() || result == nil {
		return
	}
	subject := bkntrace.DataQuerySubject{
		EntityKind:    bkntrace.EntityKindMetric,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		SubjectID:     metricID,
		QueryHash:     bkntrace.HashValue(queryShape),
		ReturnedCount: len(result.Datas),
	}
	eventID := bkntrace.EmitDataQueryEvents(traceCtx, ontologyTraceRequestContext(c, traceCtx, visitor), subject,
		bkntrace.MetricDataRefs(knID, branch, metricID, result.Datas))
	setEvidenceEventHeader(c, eventID)
}

func setEvidenceEventHeader(c *gin.Context, eventID string) {
	if strings.TrimSpace(eventID) != "" {
		c.Header(headerBKNEvidenceEventID, eventID)
	}
}

func safeObjectQueryShape(query *interfaces.ObjectQueryBaseOnObjectType) map[string]any {
	if query == nil {
		return nil
	}
	return map[string]any{
		"kn_id":                   query.KNID,
		"branch":                  query.Branch,
		"object_type_id":          query.ObjectTypeID,
		"condition_hash":          bkntrace.HashValue(query.Condition),
		"properties_hash":         bkntrace.HashValue(query.Properties),
		"offset":                  query.Offset,
		"limit":                   query.Limit,
		"include_type_info":       query.IncludeTypeInfo,
		"include_logic_params":    query.IncludeLogicParams,
		"ignoring_store":          query.IgnoringStore,
		"exclude_props_hash":      bkntrace.HashValue(query.ExcludeSystemProperties),
		"has_actual_condition":    query.ActualCondition != nil,
		"has_object_query_info":   query.ObjectQueryInfo != nil,
		"search_after_value_hash": bkntrace.HashValue(query.SearchAfter),
	}
}

func safeSubgraphSourceQueryShape(query *interfaces.SubGraphQueryBaseOnSource) map[string]any {
	if query == nil {
		return nil
	}
	return map[string]any{
		"kn_id":                   query.KNID,
		"branch":                  query.Branch,
		"source_object_type_id":   query.SourceObjecTypeId,
		"concept_groups_hash":     bkntrace.HashValue(query.ConceptGroups),
		"condition_hash":          bkntrace.HashValue(query.Condition),
		"direction":               query.Direction,
		"path_length":             query.PathLength,
		"include_incomplete_path": query.IncludeIncompletePath,
		"offset":                  query.Offset,
		"limit":                   query.Limit,
	}
}

func safeSubgraphByObjectsQueryShape(query *interfaces.SubGraphQueryBaseOnObjects) map[string]any {
	if query == nil {
		return nil
	}
	return map[string]any{
		"kn_id":        query.KNID,
		"branch":       query.Branch,
		"entries_hash": bkntrace.HashValue(query.Entries),
		"entry_count":  len(query.Entries),
	}
}

func safeMetricQueryShape(knID, branch, metricID string, query any) map[string]any {
	return map[string]any{
		"kn_id":      knID,
		"branch":     branch,
		"metric_id":  metricID,
		"query_hash": bkntrace.HashValue(query),
	}
}
