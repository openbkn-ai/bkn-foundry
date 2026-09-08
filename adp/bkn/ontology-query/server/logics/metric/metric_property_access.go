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
		required = append(required, query.AnalysisDimensions...)
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

func metricPropertyAccessError(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_Metric_InvalidParameter).
		WithErrorDetails(detail)
}
