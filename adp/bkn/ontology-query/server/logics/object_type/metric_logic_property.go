// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/rest"

	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

func filtersToCondition(filters []interfaces.Filter) *cond.CondCfg {
	if len(filters) == 0 {
		return nil
	}
	leaves := make([]*cond.CondCfg, 0, len(filters))
	for _, f := range filters {
		op := strings.TrimSpace(f.Operation)
		if op == "" || op == "=" {
			op = cond.OperationEq
		}
		leaves = append(leaves, &cond.CondCfg{
			Operation: op,
			Name:      f.Name,
			ValueOptCfg: cond.ValueOptCfg{
				Value: f.Value,
			},
		})
	}
	if len(leaves) == 1 {
		return leaves[0]
	}
	return &cond.CondCfg{
		Operation: cond.OperationAnd,
		SubConds:  leaves,
	}
}

func orderFieldsToMetricOrderBy(fields []interfaces.OrderField) []interfaces.MetricOrderBy {
	if len(fields) == 0 {
		return nil
	}
	out := make([]interfaces.MetricOrderBy, 0, len(fields))
	for _, f := range fields {
		out = append(out, interfaces.MetricOrderBy{
			Property:  f.Name,
			Direction: f.Direction,
		})
	}
	return out
}

func havingToMetricHaving(h *interfaces.HavingCondition) *interfaces.MetricHaving {
	if h == nil {
		return nil
	}
	return &interfaces.MetricHaving{
		Field:     h.Field,
		Operation: h.Operation,
		Value:     h.Value,
	}
}

func buildMetricQueryRequestFromLogicProperty(
	filters []interfaces.Filter,
	metricParams interfaces.MetricPropertyDynamicParams,
	start, end int64,
	isInstant bool,
	step string,
) *interfaces.MetricQueryRequest {
	instant := isInstant
	req := &interfaces.MetricQueryRequest{
		Time: &interfaces.MetricTimeWindow{
			Start:   &start,
			End:     &end,
			Instant: &instant,
		},
		Condition:          filtersToCondition(filters),
		AnalysisDimensions: metricParams.AnalysisDimensions,
		OrderBy:            orderFieldsToMetricOrderBy(metricParams.OrderByFields),
		Having:             havingToMetricHaving(metricParams.HavingCondition),
		Metrics:            metricParams.Metrics,
	}
	if step != "" {
		req.Time.Step = &step
	}
	return req
}

func (ots *objectTypeService) queryLogicMetricViaKN(
	ctx context.Context,
	knID, branch, otID string,
	logicProp *interfaces.LogicProperty,
	filters []interfaces.Filter,
	metricParams interfaces.MetricPropertyDynamicParams,
	start, end int64,
	isInstant bool,
	step string,
) (interfaces.MetricData, error) {

	_, exists, err := ots.omAccess.GetObjectType(ctx, knID, branch, otID)
	if err != nil {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).
			WithErrorDetails(err.Error())
	}
	if !exists {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusNotFound,
			oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
	}

	def, ok, err := ots.omAccess.GetMetricDefinition(ctx, knID, branch, logicProp.DataSource.ID)
	if err != nil {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_Metric_InternalError_QueryFailed).
			WithErrorDetails(err.Error())
	}
	if !ok || def == nil {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusNotFound,
			oerrors.OntologyQuery_Metric_NotFound).
			WithErrorDetails(fmt.Sprintf("KN 指标[%s]不存在", logicProp.DataSource.ID))
	}
	if strings.TrimSpace(def.ScopeRef) != strings.TrimSpace(otID) {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("指标[%s]的 scope_ref[%s]须等于对象类[%s]",
				logicProp.DataSource.ID, def.ScopeRef, otID))
	}

	metricQuery := buildMetricQueryRequestFromLogicProperty(filters, metricParams, start, end, isInstant, step)
	return ots.mqs.QueryMetricData(ctx, knID, branch, logicProp.DataSource.ID, metricQuery)
}
