// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package metric

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/batchindex"
	"bkn-backend/logics/permission"
)

type metricDataDependencies struct {
	ScopeType          string
	ScopeRef           string
	TimeDimension      *interfaces.MetricTimeDimension
	CalculationFormula *interfaces.MetricCalculationFormula
	AnalysisDimensions []interfaces.MetricAnalysisDimension
}

func metricDependenciesChanged(previous, current *interfaces.MetricDefinition) bool {
	if previous == nil || current == nil {
		return true
	}
	project := func(metric *interfaces.MetricDefinition) metricDataDependencies {
		return metricDataDependencies{
			ScopeType:          strings.TrimSpace(metric.ScopeType),
			ScopeRef:           strings.TrimSpace(metric.ScopeRef),
			TimeDimension:      metric.TimeDimension,
			CalculationFormula: metric.CalculationFormula,
			AnalysisDimensions: metric.AnalysisDimensions,
		}
	}
	return !reflect.DeepEqual(project(previous), project(current))
}

func (ms *metricService) authorizeMetricDependencies(ctx context.Context, tx *sql.Tx,
	metric *interfaces.MetricDefinition) error {
	scopeRef := strings.TrimSpace(metric.ScopeRef)
	resource := interfaces.KNChildPermissionResource(interfaces.RESOURCE_TYPE_OBJECT_TYPE, metric.KnID, scopeRef)
	if err := ms.ps.CheckPermission(ctx, resource, []string{
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
		interfaces.OPERATION_TYPE_QUERY_DATA,
	}); err != nil {
		return err
	}
	ot, _, err := ms.resolveMetricObjectType(ctx, tx, metric)
	if err != nil {
		return err
	}
	return ms.ps.RequireFullPropertyAccess(ctx, resource.ID, metricReferencedProperties(metric, ot))
}

func (ms *metricService) hydrateMetricDependencyProperties(ctx context.Context,
	metric *interfaces.MetricDefinition) error {
	if metric == nil || ms.ots == nil {
		return nil
	}
	scopeRef := strings.TrimSpace(metric.ScopeRef)
	if scopeRef == "" {
		return nil
	}
	ot, err := ms.ots.GetObjectTypeByID(ctx, nil, metric.KnID, metric.Branch, scopeRef)
	if err != nil {
		var httpErr *rest.HTTPError
		if errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	if ot == nil {
		return nil
	}
	metric.ScopeName = ot.OTName
	referenced := make(map[string]struct{})
	for _, property := range metricReferencedProperties(metric, ot) {
		referenced[strings.TrimSpace(property)] = struct{}{}
	}
	metric.DependencyProperties = metricDependencyPropertyMetadata(ot, referenced)
	return nil
}

func (ms *metricService) GetMetricDependencyProperties(ctx context.Context, knID, branch,
	objectTypeID string) ([]interfaces.MetricDependencyProperty, error) {
	metric := &interfaces.MetricDefinition{
		KnID: knID, Branch: branch, ScopeType: interfaces.ScopeTypeObjectType, ScopeRef: objectTypeID,
	}
	scopeRef := strings.TrimSpace(objectTypeID)
	resource := interfaces.KNChildPermissionResource(interfaces.RESOURCE_TYPE_OBJECT_TYPE, knID, scopeRef)
	if err := ms.ps.CheckPermission(ctx, resource, []string{
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
		interfaces.OPERATION_TYPE_QUERY_DATA,
	}); err != nil {
		return nil, err
	}
	ot, _, err := ms.resolveMetricObjectType(ctx, nil, metric)
	if err != nil {
		return nil, err
	}
	propertyNames := make([]string, 0, len(ot.DataProperties))
	for _, property := range ot.DataProperties {
		if property != nil {
			propertyNames = append(propertyNames, property.Name)
		}
	}
	full, err := ms.ps.FilterFullPropertyAccess(ctx, resource.ID, propertyNames)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(full))
	for _, property := range full {
		allowed[property] = struct{}{}
	}
	return metricDependencyPropertyMetadata(ot, allowed), nil
}

// GetMetricExecutionContext returns only the persisted definition and the
// backing object fields it captured. It intentionally authorizes the metric,
// not a second object-type read, so published metrics remain independently
// queryable without exposing unrelated object-type schema.
func (ms *metricService) GetMetricExecutionContext(ctx context.Context, knID, branch,
	metricID string) (*interfaces.MetricExecutionContext, error) {

	if err := permission.ValidateKNChildAuthorizationIDs(ctx, knID, []string{metricID}); err != nil {
		return nil, err
	}
	metricResource := interfaces.KNChildPermissionResource(interfaces.RESOURCE_TYPE_METRIC, knID, metricID)
	if err := ms.ps.CheckPermission(ctx, metricResource, []string{interfaces.OPERATION_TYPE_QUERY_DATA}); err != nil {
		return nil, err
	}
	definition, err := ms.ma.GetMetricByID(ctx, knID, branch, metricID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_Metric_NotFound)
		}
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}
	if definition == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_Metric_NotFound)
	}
	objectType, _, err := ms.resolveMetricObjectType(ctx, nil, definition)
	if err != nil {
		return nil, err
	}

	referenced := make(map[string]struct{})
	for _, property := range metricReferencedProperties(definition, objectType) {
		if property = strings.TrimSpace(property); property != "" {
			referenced[property] = struct{}{}
		}
	}
	properties := make([]*interfaces.DataProperty, 0, len(referenced))
	for _, property := range objectType.DataProperties {
		if property == nil {
			continue
		}
		if _, ok := referenced[strings.TrimSpace(property.Name)]; ok {
			properties = append(properties, &interfaces.DataProperty{
				Name: property.Name, Type: property.Type, MappedField: property.MappedField,
				ConditionOperations: append([]string(nil), property.ConditionOperations...),
			})
		}
	}
	executionObjectType := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:           objectType.OTID,
			DataSource:     objectType.DataSource,
			DataProperties: properties,
		},
		KNID:   knID,
		Branch: branch,
	}
	return &interfaces.MetricExecutionContext{
		Definition: definition,
		ObjectType: executionObjectType,
	}, nil
}

func metricDependencyPropertyMetadata(ot *interfaces.ObjectType,
	allowed map[string]struct{}) []interfaces.MetricDependencyProperty {
	result := make([]interfaces.MetricDependencyProperty, 0, len(allowed))
	for _, property := range ot.DataProperties {
		if property == nil {
			continue
		}
		if _, exists := allowed[property.Name]; !exists {
			continue
		}
		result = append(result, interfaces.MetricDependencyProperty{
			Name:                property.Name,
			DisplayName:         property.DisplayName,
			Type:                property.Type,
			Comment:             property.Comment,
			ConditionOperations: property.ConditionOperations,
		})
	}
	return result
}

func (ms *metricService) resolveMetricObjectType(ctx context.Context, tx *sql.Tx,
	metric *interfaces.MetricDefinition) (*interfaces.ObjectType, string, error) {
	scopeRef := strings.TrimSpace(metric.ScopeRef)
	if scopeRef == "" {
		return nil, scopeRef, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeRefRequired", nil))
	}
	ot, err := ms.ots.GetObjectTypeByID(ctx, tx, metric.KnID, metric.Branch, scopeRef)
	if err != nil {
		return nil, scopeRef, err
	}
	if ot == nil {
		return nil, scopeRef, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeObjectTypeNotFound", map[string]any{"metricID": metric.ID, "scopeRef": scopeRef}))
	}
	batchindex.EnsureObjectTypePropertyMap(ot)
	return ot, scopeRef, nil
}

func metricReferencedProperties(metric *interfaces.MetricDefinition, objectType *interfaces.ObjectType) []string {
	if metric == nil {
		return nil
	}
	properties := make([]string, 0)
	add := func(property string) {
		if property = strings.TrimSpace(property); property != "" && property != interfaces.MetricHavingFieldValue {
			properties = append(properties, property)
		}
	}
	if metric.TimeDimension != nil {
		add(metric.TimeDimension.Property)
	}
	for _, dimension := range metric.AnalysisDimensions {
		add(dimension.Name)
	}
	if formula := metric.CalculationFormula; formula != nil {
		propertyNames := make([]string, 0)
		if objectType != nil {
			for _, property := range objectType.DataProperties {
				if property != nil {
					propertyNames = append(propertyNames, property.Name)
				}
			}
		}
		properties = append(properties, collectMetricConditionFields(formula.Condition, propertyNames)...)
		add(formula.Aggregation.Property)
		for _, group := range formula.GroupBy {
			add(group.Property)
		}
		for _, order := range formula.OrderBy {
			add(order.Property)
		}
	}
	return properties
}

func collectMetricConditionFields(condition *cond.CondCfg, propertyNames []string) []string {
	result := map[string]struct{}{}
	var collect func(*cond.CondCfg)
	addAll := func() {
		for _, property := range propertyNames {
			result[property] = struct{}{}
		}
	}
	collect = func(current *cond.CondCfg) {
		if current == nil {
			return
		}
		if current.Operation == cond.OperationMultiMatch {
			fields, exists := current.RemainCfg["fields"]
			values, valid := metricConditionStringValues(fields)
			if !exists || !valid {
				addAll()
			} else {
				for _, field := range values {
					if field == "*" {
						addAll()
					} else {
						result[field] = struct{}{}
					}
				}
			}
		} else if field := strings.TrimSpace(current.Field); field == "*" {
			addAll()
		} else if field != "" {
			result[field] = struct{}{}
		}
		for _, child := range current.SubConds {
			collect(child)
		}
	}
	collect(condition)
	fields := make([]string, 0, len(result))
	for field := range result {
		fields = append(fields, field)
	}
	return fields
}

func metricConditionStringValues(value any) ([]string, bool) {
	var raw []string
	switch values := value.(type) {
	case []string:
		raw = values
	case []any:
		raw = make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			raw = append(raw, text)
		}
	case string:
		raw = []string{values}
	default:
		return nil, false
	}
	result := make([]string, 0, len(raw))
	for _, field := range raw {
		if field = strings.TrimSpace(field); field != "" {
			result = append(result, field)
		}
	}
	return result, len(result) > 0
}
