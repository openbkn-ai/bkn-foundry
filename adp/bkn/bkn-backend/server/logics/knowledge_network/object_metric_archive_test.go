// Copyright openbkn.ai

package knowledge_network

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func objectMetricArchiveFixture() *NormalizedImportPlan {
	return &NormalizedImportPlan{
		ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order"}}},
		ObjectMetrics: []*interfaces.ObjectMetricRecordV1{{Definition: interfaces.ObjectMetricDefinitionV1{
			ID: "metric-1", Version: 1, Code: "order_count", Name: "Order Count",
			OwnerObjectTypeID: "order", MetricType: interfaces.ObjectMetricTypeAtomic,
			Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "published"},
		}}},
	}
}

func TestValidateObjectMetricArchive(t *testing.T) {
	plan := objectMetricArchiveFixture()
	if err := validateObjectMetricArchive(plan); err != nil {
		t.Fatal(err)
	}
	plan.ObjectMetrics = append(plan.ObjectMetrics, plan.ObjectMetrics[0])
	if err := validateObjectMetricArchive(plan); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate version should fail, got %v", err)
	}
	plan = objectMetricArchiveFixture()
	plan.ObjectMetrics[0].Definition.Specification.BaseMetric = &interfaces.ObjectMetricRefV1{MetricID: "missing", Version: 1}
	if err := validateObjectMetricArchive(plan); err == nil || !strings.Contains(err.Error(), "missing version") {
		t.Fatalf("missing dependency should fail, got %v", err)
	}
	plan = objectMetricArchiveFixture()
	plan.ObjectMetrics[0].Definition.OwnerObjectTypeID = "missing"
	if err := validateObjectMetricArchive(plan); err == nil || !strings.Contains(err.Error(), "owner object type") {
		t.Fatalf("missing owner should fail, got %v", err)
	}
	plan = objectMetricArchiveFixture()
	plan.ObjectMetrics[0].Definition.Specification.BaseMetric = &interfaces.ObjectMetricRefV1{MetricID: "metric-1", Version: 1}
	if err := validateObjectMetricArchive(plan); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("dependency cycle should fail, got %v", err)
	}
	plan = objectMetricArchiveFixture()
	plan.ObjectMetrics[0].Definition.Lifecycle.Status = "draft"
	plan.ObjectMetrics[0].Definition.Specification.BaseMetric = &interfaces.ObjectMetricRefV1{MetricID: "unfinished", Version: 1}
	if err := validateObjectMetricArchive(plan); err != nil {
		t.Fatalf("an incomplete draft should remain importable: %v", err)
	}
	plan = objectMetricArchiveFixture()
	plan.ObjectTypes = append(plan.ObjectTypes, &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer"}})
	plan.ObjectMetrics[0].Definition.Specification.BusinessQualifiers = &interfaces.ObjectMetricBusinessQualifiersV1{
		RelationPath: &interfaces.ObjectMetricRelationPathV1{Steps: []interfaces.ObjectMetricRelationStepV1{{
			RelationTypeID: "order-customer", Direction: "outgoing", TargetObjectTypeID: "customer",
		}}},
	}
	if err := validateObjectMetricArchive(plan); err == nil || !strings.Contains(err.Error(), "relation type") {
		t.Fatalf("a published metric with a missing relation must fail: %v", err)
	}
	plan.RelationTypes = []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: "order-customer", SourceObjectTypeID: "order", TargetObjectTypeID: "customer",
	}}}
	if err := validateObjectMetricArchive(plan); err != nil {
		t.Fatalf("a valid relation path should import: %v", err)
	}
	plan.ObjectMetrics[0].Definition.Specification.BusinessQualifiers.RelationPath.Steps[0].Direction = "incoming"
	if err := validateObjectMetricArchive(plan); err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("a disconnected relation path must fail: %v", err)
	}
}

func TestObjectMetricV1ProxyProjectionIncludesReferencedResource(t *testing.T) {
	kn := &interfaces.KN{
		KNID: "kn-1",
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "order-source"}}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "customer-source"}}},
		},
		ObjectMetrics: []*interfaces.ObjectMetricRecordV1{{Definition: interfaces.ObjectMetricDefinitionV1{
			ID: "metric-1", Version: 1, OwnerObjectTypeID: "order",
			Lifecycle:         interfaces.ObjectMetricLifecycleV1{Status: "published"},
			QueryCapabilities: interfaces.ObjectMetricQueryCapability{AnalysisDimensions: []interfaces.ObjectMetricPropertyRef{{ObjectTypeID: "customer", PropertyName: "region"}}},
		}}},
	}
	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatal(err)
	}
	metricResources := map[string]bool{}
	for _, source := range sources {
		if source.BindingType == interfaces.MODULE_TYPE_METRIC {
			metricResources[source.ResourceID] = true
		}
	}
	if !metricResources["order-source"] || !metricResources["customer-source"] {
		t.Fatalf("V1 metric sources missing referenced backing resources: %#v", metricResources)
	}
}

func TestObjectMetricV1MutationRequiresFullProxyProjection(t *testing.T) {
	objectChange := &interfaces.KN{KNID: "kn-1", Branch: "main", ObjectTypes: []*interfaces.ObjectType{{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order"},
	}}}
	if !proxyChangesRequireFullProjection(objectChange, interfaces.ImportMode_Overwrite) {
		t.Fatal("object type mutations must include dependent V1 metric grants")
	}
	changes := &interfaces.KN{KNID: "kn-1", Branch: "main", ObjectMetrics: []*interfaces.ObjectMetricRecordV1{{
		Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-1", Version: 1},
	}}}
	if !proxyChangesRequireFullProjection(changes, interfaces.ImportMode_Overwrite) {
		t.Fatal("object metric lifecycle mutation must use the complete proxy projection")
	}
}
