// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package object_type

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

type objectMetricAccessStub struct {
	record *interfaces.ObjectMetricRecordV1
	err    error
}

func (stub *objectMetricAccessStub) CreateObjectMetric(context.Context, *interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (stub *objectMetricAccessStub) UpdateObjectMetricDraft(context.Context, *interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (stub *objectMetricAccessStub) GetObjectMetric(context.Context, string, string, string, int) (*interfaces.ObjectMetricRecordV1, error) {
	return stub.record, stub.err
}
func (stub *objectMetricAccessStub) ListObjectMetrics(context.Context, interfaces.ObjectMetricListQueryV1) ([]*interfaces.ObjectMetricRecordV1, int, error) {
	return nil, 0, nil
}
func (stub *objectMetricAccessStub) ListObjectMetricIDs(context.Context, interfaces.ObjectMetricListQueryV1) ([]string, error) {
	return nil, nil
}
func (stub *objectMetricAccessStub) ListObjectMetricVersions(context.Context, string, string, string) ([]*interfaces.ObjectMetricRecordV1, error) {
	return nil, nil
}
func (stub *objectMetricAccessStub) DeleteObjectMetricDraft(context.Context, string, string, string, int) error {
	return nil
}
func (stub *objectMetricAccessStub) PublishObjectMetric(context.Context, *interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (stub *objectMetricAccessStub) DeprecateObjectMetric(context.Context, *interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (stub *objectMetricAccessStub) NextObjectMetricVersion(context.Context, string, string, string) (int, error) {
	return 1, nil
}
func (stub *objectMetricAccessStub) ObjectMetricCodeExists(context.Context, string, string, string, string) (bool, error) {
	return false, nil
}

func publishedInstanceMetric(ownerID string) *interfaces.ObjectMetricRecordV1 {
	return &interfaces.ObjectMetricRecordV1{Definition: interfaces.ObjectMetricDefinitionV1{
		ID:                "metric1",
		Name:              "订单履约耗时",
		Version:           2,
		OwnerObjectTypeID: ownerID,
		CalculationScope:  interfaces.ObjectMetricScopeInstance,
		Lifecycle:         interfaces.ObjectMetricLifecycleV1{Status: "published"},
	}}
}

func TestValidateLogicMetricPropertyUsesOnlyPublishedV1References(t *testing.T) {
	Convey("Object metric logical-property references", t, func() {
		original := logics.OMA
		defer func() { logics.OMA = original }()

		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		metricAccess := bmock.NewMockMetricAccess(mockCtrl)
		service := &objectTypeService{ma: metricAccess}
		objectType := &interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1", OTName: "Order"},
			KNID:                   "kn1",
			Branch:                 interfaces.MAIN_BRANCH,
		}

		Convey("skips an empty source", func() {
			So(service.validateLogicMetricProperty(ctx, objectType, nil), ShouldBeNil)
			So(service.validateLogicMetricProperty(ctx, objectType, &interfaces.LogicProperty{Name: "duration"}), ShouldBeNil)
		})

		Convey("keeps a legacy metric id readable before migration", func() {
			metricAccess.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "legacy_metric_id").
				Return(&interfaces.MetricDefinition{ID: "legacy_metric_id", ScopeRef: "ot1"}, nil)
			err := service.validateLogicMetricProperty(ctx, objectType, &interfaces.LogicProperty{
				Name:       "duration",
				DataSource: &interfaces.ResourceInfo{ID: "legacy_metric_id"},
			})
			So(err, ShouldBeNil)
		})

		Convey("rejects a missing immutable version", func() {
			logics.OMA = &objectMetricAccessStub{err: errors.New("not found")}
			err := service.validateLogicMetricProperty(ctx, objectType, &interfaces.LogicProperty{
				Name:       "duration",
				DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric1@2"},
			})
			So(err, ShouldNotBeNil)
		})

		Convey("rejects another owner object", func() {
			logics.OMA = &objectMetricAccessStub{record: publishedInstanceMetric("ot2")}
			err := service.validateLogicMetricProperty(ctx, objectType, &interfaces.LogicProperty{
				Name:       "duration",
				DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric1@2"},
			})
			So(err, ShouldNotBeNil)
		})

		Convey("accepts the published object-instance version owned by the object class", func() {
			logics.OMA = &objectMetricAccessStub{record: publishedInstanceMetric("ot1")}
			err := service.validateLogicMetricProperty(ctx, objectType, &interfaces.LogicProperty{
				Name:       "duration",
				DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric1@2"},
			})
			So(err, ShouldBeNil)
		})
	})
}

func TestEnrichLogicMetricPropertyUsesPinnedV1Version(t *testing.T) {
	original := logics.OMA
	defer func() { logics.OMA = original }()
	logics.OMA = &objectMetricAccessStub{record: publishedInstanceMetric("ot1")}

	property := &interfaces.LogicProperty{
		Name:       "duration",
		DataSource: &interfaces.ResourceInfo{ID: "object_metric_v1:metric1@2"},
	}
	objectType := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1", LogicProperties: []*interfaces.LogicProperty{property}},
		KNID:                   "kn1",
		Branch:                 interfaces.MAIN_BRANCH,
	}

	(&objectTypeService{}).enrichLogicMetricProperty(context.Background(), objectType, property, 0)
	if got := property.DataSource.Name; got != "订单履约耗时" {
		t.Fatalf("data source name = %q", got)
	}
}
