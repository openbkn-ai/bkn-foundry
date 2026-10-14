// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package object_type

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

type objectMetricAccessStub struct {
	record  *interfaces.ObjectMetricRecordV1
	err     error
	lookups []objectMetricLookup
}

type objectMetricLookup struct {
	knID     string
	branch   string
	metricID string
	version  int
}

func (stub *objectMetricAccessStub) CreateObjectMetric(context.Context, *interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (stub *objectMetricAccessStub) UpdateObjectMetricDraft(context.Context, *interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (stub *objectMetricAccessStub) GetObjectMetric(_ context.Context, knID, branch, metricID string, version int) (*interfaces.ObjectMetricRecordV1, error) {
	stub.lookups = append(stub.lookups, objectMetricLookup{knID: knID, branch: branch, metricID: metricID, version: version})
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

func objectMetricProperty(sourceID string) *interfaces.LogicProperty {
	return &interfaces.LogicProperty{
		Name:       "duration",
		DataSource: &interfaces.ResourceInfo{ID: sourceID},
	}
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

		Convey("rejects a legacy metric owned by another object class", func() {
			metricAccess.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "legacy_metric_id").
				Return(&interfaces.MetricDefinition{ID: "legacy_metric_id", ScopeRef: "ot2"}, nil)
			err := service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("legacy_metric_id"))
			So(err, ShouldNotBeNil)
			So(err.(*rest.HTTPError).HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("rejects a V1 reference when the object-metric extension is unavailable", func() {
			logics.OMA = nil
			err := service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2"))
			So(err, ShouldNotBeNil)
			So(err.(*rest.HTTPError).HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("rejects a missing immutable version", func() {
			access := &objectMetricAccessStub{err: errors.New("not found")}
			logics.OMA = access
			err := service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2"))
			So(err, ShouldNotBeNil)
			So(access.lookups, ShouldResemble, []objectMetricLookup{{
				knID: "kn1", branch: interfaces.MAIN_BRANCH, metricID: "metric1", version: 2,
			}})
		})

		Convey("rejects another owner object", func() {
			logics.OMA = &objectMetricAccessStub{record: publishedInstanceMetric("ot2")}
			err := service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2"))
			So(err, ShouldNotBeNil)
		})

		Convey("rejects a draft version", func() {
			record := publishedInstanceMetric("ot1")
			record.Definition.Lifecycle.Status = "draft"
			logics.OMA = &objectMetricAccessStub{record: record}
			So(service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2")), ShouldNotBeNil)
		})

		Convey("rejects an object-set metric", func() {
			record := publishedInstanceMetric("ot1")
			record.Definition.CalculationScope = interfaces.ObjectMetricScopeSet
			logics.OMA = &objectMetricAccessStub{record: record}
			So(service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2")), ShouldNotBeNil)
		})

		Convey("accepts a deprecated pinned object-instance version", func() {
			record := publishedInstanceMetric("ot1")
			record.Definition.Lifecycle.Status = "deprecated"
			logics.OMA = &objectMetricAccessStub{record: record}
			So(service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2")), ShouldBeNil)
		})

		Convey("accepts the published object-instance version owned by the object class", func() {
			access := &objectMetricAccessStub{record: publishedInstanceMetric("ot1")}
			logics.OMA = access
			err := service.validateLogicMetricProperty(ctx, objectType, objectMetricProperty("object_metric_v1:metric1@2"))
			So(err, ShouldBeNil)
			So(access.lookups, ShouldResemble, []objectMetricLookup{{
				knID: "kn1", branch: interfaces.MAIN_BRANCH, metricID: "metric1", version: 2,
			}})
		})
	})
}

func TestEnrichLogicMetricPropertyUsesPinnedV1Version(t *testing.T) {
	original := logics.OMA
	defer func() { logics.OMA = original }()
	access := &objectMetricAccessStub{record: publishedInstanceMetric("ot1")}
	logics.OMA = access

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
	if got, want := access.lookups, []objectMetricLookup{{
		knID: "kn1", branch: interfaces.MAIN_BRANCH, metricID: "metric1", version: 2,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("object metric lookups = %#v, want %#v", got, want)
	}
}

func TestEnrichLegacyLogicMetricProperty(t *testing.T) {
	Convey("Legacy metric enrichment remains backward compatible", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		metricAccess := bmock.NewMockMetricAccess(mockCtrl)
		service := &objectTypeService{ma: metricAccess}
		property := &interfaces.LogicProperty{
			Name:       "risk_score",
			DataSource: &interfaces.ResourceInfo{ID: "metric1"},
			Parameters: []interfaces.Parameter{{Name: "region"}, {Name: "instant"}},
		}
		objectType := &interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "ot1", LogicProperties: []*interfaces.LogicProperty{property},
			},
			KNID: "kn1", Branch: interfaces.MAIN_BRANCH,
		}
		metricAccess.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
			Return(&interfaces.MetricDefinition{
				ID: "metric1", Name: "Risk Hit Rate", ScopeRef: "ot1",
				AnalysisDimensions: []interfaces.MetricAnalysisDimension{{Name: "region", DisplayName: "Region"}},
			}, nil)

		service.enrichLogicMetricProperty(ctx, objectType, property, 0)

		So(property.DataSource.Name, ShouldEqual, "Risk Hit Rate")
		So(property.AnalysisDims, ShouldResemble, []interfaces.Field{{Name: "region", DisplayName: "Region"}})
		So(property.Parameters[0].Comment, ShouldNotBeNil)
		So(*property.Parameters[0].Comment, ShouldEqual, "Region")
		So(property.Parameters[1].Comment, ShouldNotBeNil)
		So(*property.Parameters[1].Comment, ShouldContainSubstring, "instant")
	})
}
