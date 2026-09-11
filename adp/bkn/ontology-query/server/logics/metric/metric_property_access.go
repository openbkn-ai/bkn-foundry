// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package metric

import (
	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	propertyaccess "ontology-query/logics/property_access"
)

// requireFullMetricInputs applies the same request-scoped property snapshot as
// object queries before filters, grouping, sorting, or aggregation reach Vega.
func (s *metricQueryService) requireFullMetricInputs(ctx context.Context, objectType interfaces.ObjectType,
	definition *interfaces.MetricDefinition, query *interfaces.MetricQueryRequest) error {
	if definition == nil || definition.CalculationFormula == nil {
		return metricPropertyAccessError(ctx, "metric calculation_formula is required")
	}
	plan, err := propertyaccess.Build(ctx, s.propertyAccess, []interfaces.ObjectType{objectType})
	if err != nil {
		return err
	}
	propertyNames := make([]string, 0, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		propertyNames = append(propertyNames, property.Name)
	}

	formula := definition.CalculationFormula
	required := propertyaccess.CollectConditionFields(formula.Condition, propertyNames)
	required = append(required, formula.Aggregation.Property)
	for _, group := range formula.GroupBy {
		required = append(required, group.Property)
	}
	for _, order := range formula.OrderBy {
		if strings.TrimSpace(order.Property) != "__value" {
			required = append(required, order.Property)
		}
	}
	if query != nil {
		required = append(required, propertyaccess.CollectConditionFields(query.Condition, propertyNames)...)
		definedDimensions := metricDefinedDimensions(definition)
		for _, dimension := range query.AnalysisDimensions {
			dimension = strings.TrimSpace(dimension)
			if _, defined := definedDimensions[dimension]; defined {
				required = append(required, dimension)
			}
		}
		for _, order := range query.OrderBy {
			if strings.TrimSpace(order.Property) != "__value" {
				required = append(required, order.Property)
			}
		}
		if query.Time != nil && definition.TimeDimension != nil {
			required = append(required, definition.TimeDimension.Property)
		}
	}

	ref := propertyaccess.ObjectTypeRef(objectType.KNID, objectType.OTID)
	if !plan.AllFull(ref, required) {
		return metricPropertyAccessError(ctx, "the metric depends on a property unavailable for this operation")
	}
	return nil
}

// validatePublishedMetricInputs keeps caller-supplied query options within the
// fields captured by the trusted, persisted metric definition. Dependency
// access itself is performed through the metric's server-side proxy binding.
func validatePublishedMetricInputs(ctx context.Context, objectType interfaces.ObjectType,
	definition *interfaces.MetricDefinition, query *interfaces.MetricQueryRequest) error {
	if definition == nil || definition.CalculationFormula == nil {
		return metricPropertyAccessError(ctx, "metric calculation_formula is required")
	}
	propertyNames := make([]string, 0, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		propertyNames = append(propertyNames, property.Name)
	}
	allowed := metricDefinitionDependencies(definition, propertyNames)
	if query == nil {
		return nil
	}
	for _, field := range propertyaccess.CollectConditionFields(query.Condition, propertyNames) {
		if _, ok := allowed[strings.TrimSpace(field)]; !ok {
			return metricPropertyAccessError(ctx, "query condition exceeds the published metric definition")
		}
	}
	definedDimensions := metricDefinedDimensions(definition)
	for _, dimension := range query.AnalysisDimensions {
		if _, ok := definedDimensions[strings.TrimSpace(dimension)]; !ok {
			return metricPropertyAccessError(ctx, "analysis dimension exceeds the published metric definition")
		}
	}
	for _, order := range query.OrderBy {
		field := strings.TrimSpace(order.Property)
		if field == "__value" {
			continue
		}
		if _, ok := allowed[field]; !ok {
			return metricPropertyAccessError(ctx, "order_by exceeds the published metric definition")
		}
	}
	return nil
}

func metricDefinitionDependencies(definition *interfaces.MetricDefinition, propertyNames []string) map[string]struct{} {
	dependencies := map[string]struct{}{}
	add := func(value string) {
		if value = strings.TrimSpace(value); value != "" && value != "__value" {
			dependencies[value] = struct{}{}
		}
	}
	if definition == nil {
		return dependencies
	}
	if definition.TimeDimension != nil {
		add(definition.TimeDimension.Property)
	}
	for _, dimension := range definition.AnalysisDimensions {
		add(dimension.Name)
	}
	if formula := definition.CalculationFormula; formula != nil {
		for _, field := range propertyaccess.CollectConditionFields(formula.Condition, propertyNames) {
			add(field)
		}
		add(formula.Aggregation.Property)
		for _, group := range formula.GroupBy {
			add(group.Property)
		}
		for _, order := range formula.OrderBy {
			add(order.Property)
		}
	}
	return dependencies
}

func metricDefinedDimensions(definition *interfaces.MetricDefinition) map[string]struct{} {
	defined := map[string]struct{}{}
	if definition == nil {
		return defined
	}
	if definition.CalculationFormula != nil {
		for _, group := range definition.CalculationFormula.GroupBy {
			if name := strings.TrimSpace(group.Property); name != "" {
				defined[name] = struct{}{}
			}
		}
	}
	for _, dimension := range definition.AnalysisDimensions {
		if name := strings.TrimSpace(dimension.Name); name != "" {
			defined[name] = struct{}{}
		}
	}
	return defined
}

func metricPropertyAccessError(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_Metric_InvalidParameter).
		WithErrorDetails(detail)
}
