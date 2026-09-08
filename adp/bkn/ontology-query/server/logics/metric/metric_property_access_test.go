// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package metric

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
)

type selectiveMetricPropertyAccessStub map[string]interfaces.PropertyAccessLevel

func (stub selectiveMetricPropertyAccessStub) ResolvePropertyLevels(_ context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	entries := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	for _, item := range items {
		entry := interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, interfaces.PropertyAccessDecision{Name: name, Level: stub[name]})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func TestMetricRejectsMaskedAggregationInputBeforeDataRead(t *testing.T) {
	service := &metricQueryService{propertyAccess: selectiveMetricPropertyAccessStub{
		"amount": interfaces.PropertyAccessMasked,
	}}
	objectType := metricAccessObjectType("amount")
	definition := &interfaces.MetricDefinition{CalculationFormula: &interfaces.MetricCalculationFormula{
		Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
	}}

	err := service.requireFullMetricInputs(context.Background(), objectType, definition, &interfaces.MetricQueryRequest{})
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusBadRequest {
		t.Fatalf("metric property access error = %#v", err)
	}
}

func TestMetricRequiresFullFilterGroupSortAndTimeInputs(t *testing.T) {
	service := &metricQueryService{propertyAccess: selectiveMetricPropertyAccessStub{
		"amount": interfaces.PropertyAccessFull,
		"region": interfaces.PropertyAccessFull,
		"status": interfaces.PropertyAccessFull,
		"time":   interfaces.PropertyAccessFull,
	}}
	objectType := metricAccessObjectType("amount", "region", "status", "time")
	definition := &interfaces.MetricDefinition{
		TimeDimension: &interfaces.MetricTimeDimension{Property: "time"},
		CalculationFormula: &interfaces.MetricCalculationFormula{
			Condition:   &cond.CondCfg{Name: "status", Operation: cond.OperationEq},
			Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
			GroupBy:     []interfaces.MetricGroupBy{{Property: "region"}},
			OrderBy:     []interfaces.MetricOrderBy{{Property: "region"}},
		},
	}
	query := &interfaces.MetricQueryRequest{
		Condition:          &cond.CondCfg{Name: "status", Operation: cond.OperationEq},
		AnalysisDimensions: []string{"region", "legacy_missing_dimension"},
		OrderBy:            []interfaces.MetricOrderBy{{Property: "__value"}},
		Time:               &interfaces.MetricTimeWindow{},
	}
	if err := service.requireFullMetricInputs(context.Background(), objectType, definition, query); err != nil {
		t.Fatalf("full metric inputs rejected: %v", err)
	}
}

func TestMetricConversionErrorsDoNotExposeRawValues(t *testing.T) {
	const watermark = "raw-property-watermark-1342"
	var values []any
	_, err := appendMetricValue(context.Background(), map[string]any{"value": watermark}, &values)
	if err == nil || strings.Contains(err.Error(), watermark) {
		t.Fatalf("aggregate conversion exposed raw value: %v", err)
	}
	_, err = entryTimeToMillis(watermark, nil)
	if err == nil || strings.Contains(err.Error(), watermark) {
		t.Fatalf("time conversion exposed raw value: %v", err)
	}
}

func metricAccessObjectType(properties ...string) interfaces.ObjectType {
	result := interfaces.ObjectType{KNID: "kn-1", ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders"}}
	for _, property := range properties {
		result.DataProperties = append(result.DataProperties, cond.DataProperty{Name: property, Type: "string"})
	}
	return result
}
