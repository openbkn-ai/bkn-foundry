// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"go.uber.org/mock/gomock"
)

func objectInstanceBatchFixture() (interfaces.Objects, []map[string]any,
	map[string]*interfaces.LogicProperty, *interfaces.ObjectPropertyValueQuery) {
	objects := interfaces.Objects{
		ObjectType: &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "order", PrimaryKeys: []string{"id"},
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
		}},
		Datas: []map[string]any{},
	}
	for _, id := range []string{"order-a", "order-b"} {
		objects.Datas = append(objects.Datas, map[string]any{"metric": interfaces.MetricProperty{
			Parameters: interfaces.MetricFilters{Filters: []interfaces.Filter{{Name: "id", Operation: "==", Value: id}}},
		}})
	}
	logical := map[string]*interfaces.LogicProperty{"metric": {
		Name: "metric", Type: interfaces.PROPERTY_TYPE_METRIC,
		DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric-1@1"},
	}}
	query := &interfaces.ObjectPropertyValueQuery{KNID: "kn", Branch: "main", ObjectTypeID: "order"}
	return objects, []map[string]any{{}, {}}, logical, query
}

func objectInstanceBatchDefinition() *interfaces.ObjectMetricExecutionContextV1 {
	return &interfaces.ObjectMetricExecutionContextV1{Root: &interfaces.ObjectMetricRecordV1{
		Definition: interfaces.ObjectMetricDefinitionV1{
			ID: "metric-1", Version: 1, MetricType: "atomic", CalculationScope: "object_instance",
			OwnerObjectTypeID: "order",
			Specification: interfaces.ObjectMetricSpecificationV1{Measure: &interfaces.ObjectMetricMeasureV1{
				Aggregation: "sum",
			}},
		},
	}}
}

func TestBatchObjectInstanceMetricPropertiesMapsReorderedResults(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	t.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
		Return(objectInstanceBatchDefinition(), nil)
	metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
			query *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
			if len(query.InstanceIdentities) != 2 || query.InstanceIdentities[0]["id"] != "order-a" ||
				query.InstanceIdentities[1]["id"] != "order-b" {
				t.Fatalf("batch identities = %#v", query.InstanceIdentities)
			}
			return interfaces.ObjectMetricDataV1{
				Model: interfaces.ObjectMetricResultModelV1{OutputShape: "object_value"},
				Entries: []interfaces.ObjectMetricDataEntryV1{
					{InstanceIdentity: map[string]any{"id": "order-b"}, Value: 20, Status: "ok"},
					{InstanceIdentity: map[string]any{"id": "order-a"}, Value: 10, Status: "ok"},
				},
			}, nil
		})
	objects, datas, logical, query := objectInstanceBatchFixture()
	service := &objectTypeService{omAccess: models}
	completed := service.batchObjectInstanceMetricProperties(context.Background(), query, objects, datas,
		logical, map[string]bool{"metric": true})
	for index, want := range []int{10, 20} {
		got, ok := datas[index]["metric"].(interfaces.MetricData)
		if !completed[index]["metric"] || !ok || len(got.Datas) != 1 || got.Datas[0].Values[0] != want {
			t.Fatalf("object %d metric=%#v completed=%v", index, datas[index]["metric"], completed[index])
		}
	}
}

func TestBatchObjectInstanceMetricPropertiesRejectsUncertainResults(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*interfaces.ObjectMetricDataV1)
	}{
		{name: "partial", mutate: func(result *interfaces.ObjectMetricDataV1) { result.Status = "partial" }},
		{name: "warning", mutate: func(result *interfaces.ObjectMetricDataV1) { result.Warnings = []string{"incomplete"} }},
		{name: "next page", mutate: func(result *interfaces.ObjectMetricDataV1) { result.NextCursor = "more" }},
		{name: "duplicate identity", mutate: func(result *interfaces.ObjectMetricDataV1) {
			result.Entries[1].InstanceIdentity = map[string]any{"id": "order-a"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
			logics.SetObjectMetricQueryService(metrics)
			t.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
			models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
				Return(objectInstanceBatchDefinition(), nil)
			result := interfaces.ObjectMetricDataV1{
				Model:  interfaces.ObjectMetricResultModelV1{OutputShape: "object_value"},
				Status: "ok",
				Entries: []interfaces.ObjectMetricDataEntryV1{
					{InstanceIdentity: map[string]any{"id": "order-a"}, Value: 10},
					{InstanceIdentity: map[string]any{"id": "order-b"}, Value: 20},
				},
			}
			test.mutate(&result)
			metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
				Return(result, nil)
			objects, datas, logical, query := objectInstanceBatchFixture()
			service := &objectTypeService{omAccess: models}
			completed := service.batchObjectInstanceMetricProperties(context.Background(), query, objects, datas,
				logical, map[string]bool{"metric": true})
			for index := range datas {
				if completed[index]["metric"] || len(datas[index]) != 0 {
					t.Fatalf("uncertain batch must use the original path: object=%d completed=%v data=%v",
						index, completed[index], datas[index])
				}
			}
		})
	}
}

func TestBatchObjectInstanceMetricPropertiesDoesNotBypassMetadataFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	t.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
		Return(nil, errors.New("metric metadata unavailable"))
	objects, datas, logical, query := objectInstanceBatchFixture()
	service := &objectTypeService{omAccess: models}
	completed := service.batchObjectInstanceMetricProperties(context.Background(), query, objects, datas,
		logical, map[string]bool{"metric": true})
	for index := range datas {
		if completed[index]["metric"] || len(datas[index]) != 0 {
			t.Fatalf("metadata failure must retain the original per-object path: object=%d completed=%v data=%v",
				index, completed[index], datas[index])
		}
	}
}

func TestObjectInstanceBatchableRejectsTimeAndObjectSetMetrics(t *testing.T) {
	context := objectInstanceBatchDefinition()
	if !objectInstanceMetricBatchable(context, "order", "metric-1", 1) {
		t.Fatal("simple instance metric should be batchable")
	}
	context.Root.Definition.Specification.BusinessQualifiers = &interfaces.ObjectMetricBusinessQualifiersV1{
		TimeWindow: &interfaces.ObjectMetricTimeWindowV1{Mode: "rolling", Length: 1, Unit: "day"},
	}
	if objectInstanceMetricBatchable(context, "order", "metric-1", 1) {
		t.Fatal("rolling window cannot be merged across calls")
	}
	context.Root.Definition.Specification.BusinessQualifiers = nil
	context.Root.Definition.CalculationScope = "object_set"
	if objectInstanceMetricBatchable(context, "order", "metric-1", 1) {
		t.Fatal("object-set metric must retain its existing execution path")
	}
}

func TestBatchObjectInstanceMetricPropertiesBoundsBatchSize(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	t.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
		Return(objectInstanceBatchDefinition(), nil)
	metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
		Times(2).DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
		query *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
		if len(query.InstanceIdentities) != maxObjectInstanceMetricBatch {
			t.Fatalf("batch size = %d", len(query.InstanceIdentities))
		}
		entries := make([]interfaces.ObjectMetricDataEntryV1, 0, len(query.InstanceIdentities))
		for _, identity := range query.InstanceIdentities {
			entries = append(entries, interfaces.ObjectMetricDataEntryV1{InstanceIdentity: identity, Value: 1})
		}
		return interfaces.ObjectMetricDataV1{
			Model: interfaces.ObjectMetricResultModelV1{OutputShape: "object_value"}, Entries: entries,
		}, nil
	})
	objects, _, logical, query := objectInstanceBatchFixture()
	objects.Datas = nil
	datas := make([]map[string]any, maxObjectInstanceMetricBatch*2+1)
	for index := range datas {
		id := fmt.Sprintf("order-%03d", index)
		objects.Datas = append(objects.Datas, map[string]any{"metric": interfaces.MetricProperty{
			Parameters: interfaces.MetricFilters{Filters: []interfaces.Filter{{Name: "id", Operation: "==", Value: id}}},
		}})
		datas[index] = map[string]any{}
	}
	service := &objectTypeService{omAccess: models}
	completed := service.batchObjectInstanceMetricProperties(context.Background(), query, objects, datas,
		logical, map[string]bool{"metric": true})
	for index := range datas {
		want := index < maxObjectInstanceMetricBatch*2
		if completed[index]["metric"] != want {
			t.Fatalf("object %d completed=%v want=%v", index, completed[index], want)
		}
	}
}

func TestBatchObjectInstanceMetricPropertiesRunsChunksConcurrently(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	t.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
		Return(objectInstanceBatchDefinition(), nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBatch := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBatch()
	metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
		Times(2).DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
		request *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
		started <- struct{}{}
		<-release
		entries := make([]interfaces.ObjectMetricDataEntryV1, 0, len(request.InstanceIdentities))
		for _, identity := range request.InstanceIdentities {
			entries = append(entries, interfaces.ObjectMetricDataEntryV1{InstanceIdentity: identity, Value: 1})
		}
		return interfaces.ObjectMetricDataV1{
			Model: interfaces.ObjectMetricResultModelV1{OutputShape: "object_value"}, Entries: entries,
		}, nil
	})
	objects, _, logical, query := objectInstanceBatchFixture()
	objects.Datas = nil
	datas := make([]map[string]any, maxObjectInstanceMetricBatch*2)
	for index := range datas {
		id := fmt.Sprintf("order-%03d", index)
		objects.Datas = append(objects.Datas, map[string]any{"metric": interfaces.MetricProperty{
			Parameters: interfaces.MetricFilters{Filters: []interfaces.Filter{{Name: "id", Operation: "==", Value: id}}},
		}})
		datas[index] = map[string]any{}
	}
	service := &objectTypeService{omAccess: models}
	done := make(chan []map[string]bool, 1)
	go func() {
		done <- service.batchObjectInstanceMetricProperties(context.Background(), query, objects, datas,
			logical, map[string]bool{"metric": true})
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("metric batches did not overlap")
		}
	}
	releaseBatch()
	completed := <-done
	for index := range completed {
		if !completed[index]["metric"] {
			t.Fatalf("object %d was not completed", index)
		}
	}
}

func TestGetObjectPropertyValueFallsBackAfterPartialInstanceBatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := omock.NewMockVegaBackendAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	t.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{},
	}
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "order", PrimaryKeys: []string{"id"},
		DataProperties: []cond.DataProperty{{Name: "id", Type: "string", MappedField: cond.Field{Name: "id"}}},
		LogicProperties: []*interfaces.LogicProperty{{
			Name: "metric", Type: interfaces.PROPERTY_TYPE_METRIC,
			Parameters: []interfaces.Parameter{{Name: "id", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "id"}},
			DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric-1@1"},
		}},
		DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
	}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "order").Return(objectType, true, nil).AnyTimes()
	models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
		Return(objectInstanceBatchDefinition(), nil)
	vega.EXPECT().QueryResourceData(gomock.Any(), "resource-1", gomock.Any()).Return(
		&interfaces.DatasetQueryResponse{Entries: []map[string]any{{"id": "order-a"}, {"id": "order-b"}}, TotalCount: 2}, nil)
	var batched, individual atomic.Int32
	metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
		Times(3).DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
		query *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
		if len(query.InstanceIdentities) == 2 {
			batched.Add(1)
			return interfaces.ObjectMetricDataV1{Status: "partial", Model: interfaces.ObjectMetricResultModelV1{
				OutputShape: "object_value",
			}}, nil
		}
		individual.Add(1)
		id := query.InstanceIdentities[0]["id"]
		value := 10
		if id == "order-b" {
			value = 20
		}
		return interfaces.ObjectMetricDataV1{Entries: []interfaces.ObjectMetricDataEntryV1{{
			InstanceIdentity: map[string]any{"id": id}, Value: value, Status: "ok",
		}}}, nil
	})
	result, err := service.GetObjectPropertyValue(context.Background(), &interfaces.ObjectPropertyValueQuery{
		KNID: "kn", Branch: "main", ObjectTypeID: "order", Properties: []string{"metric"},
		InstanceIdentities: []map[string]any{{"id": "order-a"}, {"id": "order-b"}},
	})
	if err != nil || len(result.Datas) != 2 || batched.Load() != 1 || individual.Load() != 2 {
		t.Fatalf("fallback failed: rows=%#v batch=%d individual=%d err=%v",
			result.Datas, batched.Load(), individual.Load(), err)
	}
	for index, want := range []int{10, 20} {
		value, ok := result.Datas[index]["metric"].(interfaces.MetricData)
		if !ok || len(value.Datas) != 1 || value.Datas[0].Values[0] != want {
			t.Fatalf("object %d metric = %#v", index, result.Datas[index]["metric"])
		}
	}
}

// This isolates the fan-out reduction for a page of distinct instance metrics.
// It does not represent deployed end-to-end latency.
func BenchmarkBatchObjectInstanceMetricProperties(b *testing.B) {
	ctrl := gomock.NewController(b)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	metrics := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	logics.SetObjectMetricQueryService(metrics)
	b.Cleanup(func() { logics.SetObjectMetricQueryService(nil) })
	models.EXPECT().GetObjectMetricExecutionContext(gomock.Any(), "kn", "main", "metric-1", 1).
		Return(objectInstanceBatchDefinition(), nil).AnyTimes()
	var metricCalls atomic.Int64
	metrics.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric-1", 1, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
			query *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
			metricCalls.Add(1)
			entries := make([]interfaces.ObjectMetricDataEntryV1, 0, len(query.InstanceIdentities))
			for _, identity := range query.InstanceIdentities {
				entries = append(entries, interfaces.ObjectMetricDataEntryV1{
					InstanceIdentity: identity, Value: 1, Status: "ok",
				})
			}
			return interfaces.ObjectMetricDataV1{
				Model: interfaces.ObjectMetricResultModelV1{OutputShape: "object_value"}, Entries: entries,
			}, nil
		}).AnyTimes()
	objects, _, logical, query := objectInstanceBatchFixture()
	objects.Datas = make([]map[string]any, maxObjectInstanceMetricBatch)
	for index := range objects.Datas {
		objects.Datas[index] = map[string]any{"metric": interfaces.MetricProperty{
			Parameters: interfaces.MetricFilters{Filters: []interfaces.Filter{{
				Name: "id", Operation: "==", Value: fmt.Sprintf("order-%03d", index),
			}}},
		}}
	}
	service := &objectTypeService{omAccess: models}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		datas := make([]map[string]any, len(objects.Datas))
		for index := range datas {
			datas[index] = map[string]any{}
		}
		completed := service.batchObjectInstanceMetricProperties(context.Background(), query, objects, datas,
			logical, map[string]bool{"metric": true})
		for index := range datas {
			if !completed[index]["metric"] {
				b.Fatalf("instance %d was not batched", index)
			}
		}
	}
	b.ReportMetric(float64(metricCalls.Load())/float64(b.N), "metric_calls/op")
}
