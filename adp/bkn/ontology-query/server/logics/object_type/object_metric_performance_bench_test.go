// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"go.uber.org/mock/gomock"
)

// A reproducible, in-memory benchmark of the fan-out from one object page.
// The downstream metric stub is intentionally cheap: metric_calls/op is the
// useful signal here, not a substitute for production latency measurements.
func BenchmarkGetObjectPropertyValueObjectSetMetric(b *testing.B) {
	ctrl := gomock.NewController(b)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := omock.NewMockVegaBackendAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	b.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{},
	}
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "shipment", PrimaryKeys: []string{"id"},
		DataProperties: []cond.DataProperty{{Name: "id", Type: "string", MappedField: cond.Field{Name: "id"}}},
		LogicProperties: []*interfaces.LogicProperty{{
			Name: "metric", Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
			DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric-1@1"},
		}},
		DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
	}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "shipment").Return(objectType, true, nil).AnyTimes()
	entries := make([]map[string]any, 32)
	identities := make([]map[string]any, len(entries))
	for index := range entries {
		id := fmt.Sprintf("shipment-%02d", index)
		entries[index] = map[string]any{"id": id}
		identities[index] = map[string]any{"id": id}
	}
	var sourceCalls, metricCalls atomic.Int64
	vega.EXPECT().QueryResourceData(gomock.Any(), "resource-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
			sourceCalls.Add(1)
			return &interfaces.DatasetQueryResponse{Entries: entries, TotalCount: int64(len(entries))}, nil
		}).AnyTimes()
	metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
			_ *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
			metricCalls.Add(1)
			return interfaces.ObjectMetricDataV1{Entries: []interfaces.ObjectMetricDataEntryV1{{Value: 42}}}, nil
		}).AnyTimes()
	query := &interfaces.ObjectPropertyValueQuery{KNID: "kn", Branch: "main", ObjectTypeID: "shipment",
		Properties: []string{"metric"}, InstanceIdentities: identities}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		objects, err := service.GetObjectPropertyValue(context.Background(), query)
		if err != nil || len(objects.Datas) != len(entries) {
			b.Fatalf("objects=%d err=%v", len(objects.Datas), err)
		}
	}
	b.ReportMetric(float64(sourceCalls.Load())/float64(b.N), "source_calls/op")
	b.ReportMetric(float64(metricCalls.Load())/float64(b.N), "metric_calls/op")
}

// Models a page with many declared logical properties but only one returned
// property. This measures in-memory projection overhead, not backend latency.
func BenchmarkObjectLogicPropertySelection(b *testing.B) {
	logicProperties := make([]*interfaces.LogicProperty, 100)
	for index := range logicProperties {
		logicProperties[index] = &interfaces.LogicProperty{
			Name: fmt.Sprintf("logical-%d", index), Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
			DataSource: &interfaces.ResourceInfo{ID: "metric"},
		}
	}
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		LogicProperties: logicProperties,
	}}
	plan := &propertyAccessPlan{returnLogic: map[string]struct{}{"logical-99": {}}}
	service := &objectTypeService{}
	ctx := context.Background()
	for _, scenario := range []struct {
		name   string
		perRow bool
	}{{name: "select-per-row", perRow: true}, {name: "select-per-page"}} {
		b.Run(scenario.name, func(b *testing.B) {
			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				selected := selectedObjectLogicProperties(objectType, plan)
				for rowIndex := 0; rowIndex < 1000; rowIndex++ {
					if scenario.perRow {
						selected = selectedObjectLogicProperties(objectType, plan)
					}
					row := make(map[string]any)
					if err := service.addLogicProperties(ctx, row, selected); err != nil || len(row) != 1 {
						b.Fatalf("row=%#v err=%v", row, err)
					}
				}
			}
			b.ReportMetric(1000, "rows/op")
		})
	}
}
