// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"fmt"
	"strconv"
	"time"

	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/locale"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

const objectMetricV1SourcePrefix = "object_metric_v1:"

func parseObjectMetricV1Source(source string) (string, int, bool) {
	if !strings.HasPrefix(source, objectMetricV1SourcePrefix) {
		return "", 0, false
	}
	metricID, rawVersion, found := strings.Cut(strings.TrimPrefix(source, objectMetricV1SourcePrefix), "@")
	if !found || strings.TrimSpace(metricID) == "" {
		return "", 0, false
	}
	version, err := strconv.Atoi(rawVersion)
	return metricID, version, err == nil && version > 0
}

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

// logicMetricTimeWindow decides the window one logic property call asks for.
//
// Nothing supplied means no window: a metric without a time_dimension has
// nothing to filter on, and one with a time_dimension applies its own
// default_range_policy. One end supplied derives the other, because the metric
// layer uses a request window only when both ends are present and otherwise
// falls back to that policy - a caller that gives one end and silently gets no
// filter at all reads a number computed over everything.
func logicMetricTimeWindow(params interfaces.MetricPropertyDynamicParams, now int64) (start, end *int64) {
	if params.Start != nil {
		supplied := *params.Start
		start = &supplied
	}
	if params.End != nil {
		supplied := *params.End
		end = &supplied
	}
	switch {
	case start != nil && end == nil:
		derived := now
		end = &derived
	case end != nil && start == nil:
		derived := *end - 30*time.Minute.Milliseconds()
		start = &derived
	}
	return start, end
}

// buildMetricQueryRequestFromLogicProperty turns one logic property call into
// a metric query. start and end are nil when the caller asked for no time
// range, and stay nil in the query: a metric without a time_dimension has
// nothing to filter on, and one with a time_dimension applies its own
// default_range_policy. Passing a fabricated window instead made both cases
// wrong - the first was refused outright, the second answered for a window
// nobody chose.
func buildMetricQueryRequestFromLogicProperty(
	filters []interfaces.Filter,
	metricParams interfaces.MetricPropertyDynamicParams,
	start, end *int64,
	isInstant bool,
	step string,
) *interfaces.MetricQueryRequest {
	instant := isInstant
	req := &interfaces.MetricQueryRequest{
		Time: &interfaces.MetricTimeWindow{
			Start:   start,
			End:     end,
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
	start, end *int64,
	isInstant bool,
	step string,
) (interfaces.MetricData, error) {

	objectType, exists, err := ots.omAccess.GetObjectType(ctx, knID, branch, otID)
	if err != nil {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).
			WithErrorDetails(err.Error())
	}
	if !exists {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusNotFound,
			oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
	}
	if objectType.DataSource == nil || objectType.DataSource.ID == "" {
		return interfaces.MetricData{}, logics.MissingObjectTypeDataSourceError(ctx, otID)
	}
	if objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		return interfaces.MetricData{}, logics.UnsupportedObjectTypeDataSourceError(ctx, otID, objectType.DataSource.Type)
	}
	if metricID, version, isObjectMetric := parseObjectMetricV1Source(logicProp.DataSource.ID); isObjectMetric {
		query, err := buildObjectMetricQueryFromLogicProperty(objectType, filters, metricParams, start, end)
		if err != nil {
			return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails(err.Error())
		}
		service := logics.ObjectMetricQueryService()
		if service == nil {
			return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
				oerrors.OntologyQuery_Metric_InternalError_QueryFailed).
				WithErrorDetails("object metric query capability is not assembled")
		}
		result, err := service.QueryObjectMetricDataV1(ctx, knID, branch, metricID, version, query)
		if err != nil {
			return interfaces.MetricData{}, err
		}
		return objectMetricDataToLogicPropertyValue(result, end), nil
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
			WithErrorDetails(locale.ValidationDetail(ctx, "KNMetricNotFound", map[string]any{"metricID": logicProp.DataSource.ID}))
	}
	if strings.TrimSpace(def.ScopeRef) != strings.TrimSpace(otID) {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "MetricScopeMismatch", map[string]any{
				"metricID": logicProp.DataSource.ID, "scopeRef": def.ScopeRef, "objectTypeID": otID,
			}))
	}

	metricQuery := buildMetricQueryRequestFromLogicProperty(filters, metricParams, start, end, isInstant, step)
	return ots.mqs.QueryMetricData(ctx, knID, branch, logicProp.DataSource.ID, metricQuery)
}

func buildObjectMetricQueryFromLogicProperty(objectType interfaces.ObjectType, filters []interfaces.Filter,
	params interfaces.MetricPropertyDynamicParams, start, end *int64) (*interfaces.ObjectMetricQueryRequestV1, error) {
	query := &interfaces.ObjectMetricQueryRequestV1{}
	if start != nil || end != nil {
		if start == nil || end == nil {
			return nil, fmt.Errorf("object metric time range requires both start and end")
		}
		query.Time = &interfaces.ObjectMetricQueryTimeRangeV1{Start: *start, End: *end}
	}
	identity := map[string]any{}
	primaryKeys := map[string]struct{}{}
	for _, key := range objectType.PrimaryKeys {
		primaryKeys[key] = struct{}{}
	}
	predicates := make([]interfaces.ObjectMetricConditionV1, 0)
	for _, filter := range filters {
		operation := strings.TrimSpace(filter.Operation)
		if operation == "" || operation == "=" || operation == "==" {
			if _, isPrimaryKey := primaryKeys[filter.Name]; isPrimaryKey {
				identity[filter.Name] = filter.Value
				continue
			}
			operation = "eq"
		}
		mappedOperation := map[string]string{
			"!=": "neq", ">": "gt", ">=": "gte", "<": "lt", "<=": "lte",
			"in": "in", "not_in": "not_in", "contain": "contains", "not_contain": "not_contains",
			"prefix": "starts_with", "null": "is_null", "not_null": "is_not_null",
		}[operation]
		if operation == "eq" {
			mappedOperation = operation
		}
		if mappedOperation == "" {
			return nil, fmt.Errorf("logic-property filter operation %q is not supported by object metrics", filter.Operation)
		}
		right := []interfaces.ObjectMetricOperandV1{}
		if mappedOperation != "is_null" && mappedOperation != "is_not_null" {
			right = append(right, interfaces.ObjectMetricOperandV1{Source: "literal", Value: filter.Value, ValueType: objectMetricLiteralType(filter.Value)})
		}
		predicates = append(predicates, interfaces.ObjectMetricConditionV1{
			NodeType: "predicate",
			Left: &interfaces.ObjectMetricOperandV1{Source: "property", PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
				ObjectTypeID: objectType.OTID, PropertyName: filter.Name,
			}},
			ComparisonOperator: mappedOperation,
			Right:              right,
		})
	}
	if len(identity) > 0 {
		if len(identity) != len(primaryKeys) {
			return nil, fmt.Errorf("logic-property call must supply the complete object instance ID")
		}
		query.InstanceIdentities = []map[string]any{identity}
	}
	if len(predicates) == 1 {
		query.Filter = &predicates[0]
	} else if len(predicates) > 1 {
		query.Filter = &interfaces.ObjectMetricConditionV1{NodeType: "group", BooleanOperator: "and", Children: predicates}
	}
	for _, dimension := range params.AnalysisDimensions {
		query.AnalysisDimensions = append(query.AnalysisDimensions, interfaces.ObjectMetricPropertyRefV1{
			ObjectTypeID: objectType.OTID, PropertyName: dimension,
		})
	}
	return query, nil
}

func objectMetricLiteralType(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "int64"
	case float32, float64:
		return "decimal"
	default:
		return "string"
	}
}

func objectMetricDataToLogicPropertyValue(result interfaces.ObjectMetricDataV1, requestedEnd *int64) interfaces.MetricData {
	data := make([]interfaces.Data, 0, len(result.Entries))
	stamp := time.Now().UnixMilli()
	if requestedEnd != nil {
		stamp = *requestedEnd
	}
	for _, entry := range result.Entries {
		labels := make(map[string]string, len(entry.Dimensions)+len(entry.InstanceIdentity))
		for key, value := range entry.Dimensions {
			labels[key] = fmt.Sprint(value)
		}
		for key, value := range entry.InstanceIdentity {
			labels[key] = fmt.Sprint(value)
		}
		entryTime := stamp
		if entry.Time != nil {
			entryTime = *entry.Time
		}
		data = append(data, interfaces.Data{Labels: labels, Times: []any{entryTime}, Values: []any{entry.Value}})
	}
	unit := ""
	if result.Model.Value.Unit != nil {
		unit = result.Model.Value.Unit.DisplaySymbol
		if unit == "" {
			unit = result.Model.Value.Unit.Code
		}
	}
	return interfaces.MetricData{
		Model: interfaces.MetricModel{UnitType: result.Model.Value.SemanticType, Unit: unit},
		Datas: data,
	}
}
