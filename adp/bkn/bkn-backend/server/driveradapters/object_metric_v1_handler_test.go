// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

type objectMetricBindServiceStub struct {
	interfaces.ObjectMetricServiceV1
	bindCalls int
}

type objectMetricUpdateServiceStub struct {
	interfaces.ObjectMetricServiceV1
	updateCalls int
}

func (s *objectMetricUpdateServiceStub) UpdateDraft(context.Context, string, string, string, *interfaces.ObjectMetricDefinitionV1) (*interfaces.ObjectMetricRecordV1, error) {
	s.updateCalls++
	return &interfaces.ObjectMetricRecordV1{}, nil
}

func TestUpdateObjectMetricDraftRejectsMismatchedVersion(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	service := &objectMetricUpdateServiceStub{}
	handler := &restHandler{oms: service}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics/m-1?version=3", strings.NewReader(`{"id":"m-1","version":2}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "m-1"}}

	handler.updateObjectMetricDraft(c, hydra.Visitor{})
	if recorder.Code != http.StatusBadRequest || service.updateCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.updateCalls, recorder.Body.String())
	}
}

type objectMetricListServiceStub struct {
	interfaces.ObjectMetricServiceV1
	account interfaces.AccountInfo
	query   interfaces.ObjectMetricListQueryV1
}

type objectMetricVersionsServiceStub struct {
	interfaces.ObjectMetricServiceV1
	account  interfaces.AccountInfo
	knID     string
	branch   string
	metricID string
	versions []*interfaces.ObjectMetricRecordV1
}

func (s *objectMetricVersionsServiceStub) ListVersions(ctx context.Context, knID, branch, metricID string) ([]*interfaces.ObjectMetricRecordV1, error) {
	s.account, _ = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	s.knID, s.branch, s.metricID = knID, branch, metricID
	return s.versions, nil
}

func TestGetObjectMetricProvenanceByInForwardsCallerIdentityAndExactVersion(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	service := &objectMetricVersionsServiceStub{versions: []*interfaces.ObjectMetricRecordV1{
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 1, Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "published"}}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 3, Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "draft"}}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 2, Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "published"}}},
	}}
	handler := &restHandler{oms: service}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn-a/object-metrics/m-1/provenance?branch=main&version=2", nil)
	c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "caller-1")
	c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "m-1"}}

	handler.GetObjectMetricProvenanceByIn(c)

	if recorder.Code != http.StatusOK || service.account.ID != "caller-1" || service.account.Type != "user" ||
		service.knID != "kn-a" || service.branch != "main" || service.metricID != "m-1" ||
		!strings.Contains(recorder.Body.String(), `"version":2`) || strings.Contains(recorder.Body.String(), `"version":3`) {
		t.Fatalf("status=%d account=%#v query=%s/%s/%s body=%s", recorder.Code,
			service.account, service.knID, service.branch, service.metricID, recorder.Body.String())
	}
}

func TestGetObjectMetricProvenanceByInHidesDraftVersion(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	service := &objectMetricVersionsServiceStub{versions: []*interfaces.ObjectMetricRecordV1{{
		Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 1, Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "draft"}},
	}}}
	handler := &restHandler{oms: service}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn-a/object-metrics/m-1/provenance?version=1", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "m-1"}}

	handler.GetObjectMetricProvenanceByIn(c)
	c.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusNotFound || recorder.Body.Len() != 0 {
		t.Fatalf("status=%d body=%s, want empty 404", recorder.Code, recorder.Body.String())
	}
}

func (s *objectMetricListServiceStub) List(ctx context.Context, query interfaces.ObjectMetricListQueryV1) (*interfaces.ObjectMetricListV1, error) {
	s.account, _ = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	s.query = query
	return &interfaces.ObjectMetricListV1{Entries: []*interfaces.ObjectMetricRecordV1{}, TotalCount: 0}, nil
}

func TestListObjectMetricsByInForwardsCallerIdentityAndPublishedFilter(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	service := &objectMetricListServiceStub{}
	handler := &restHandler{oms: service}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn-a/object-metrics?branch=main&status=published&offset=20&limit=500", nil)
	c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "caller-1")
	c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}}

	handler.ListObjectMetricsByIn(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if service.account.ID != "caller-1" || service.account.Type != "user" {
		t.Fatalf("account = %#v, want caller-1/user", service.account)
	}
	if service.query.KNID != "kn-a" || service.query.Branch != "main" || service.query.Status != "published" || service.query.Offset != 20 || service.query.Limit != 500 {
		t.Fatalf("query = %#v", service.query)
	}
}

func (s *objectMetricBindServiceStub) BindLogicProperty(
	_ context.Context,
	_, _, _ string,
	_ int,
	_ *interfaces.MetricPropertyBindingV1,
) (*interfaces.LogicProperty, error) {
	s.bindCalls++
	return &interfaces.LogicProperty{Name: "duration", DisplayName: "履约耗时"}, nil
}

func newBindObjectMetricContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics/metric-a/logic-properties?version=2&branch=main", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "kn_id", Value: "kn-a"},
		{Key: "metric_id", Value: "metric-a"},
	}
	return c, recorder
}

func TestBindObjectMetricLogicPropertyUsesObjectTypeAuditTargetName(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	objectTypes.EXPECT().GetObjectTypeByID(gomock.Any(), nil, "kn-a", "main", "orders").Return(&interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders", OTName: "订单"},
	}, nil)
	metrics := &objectMetricBindServiceStub{}
	handler := &restHandler{ots: objectTypes, oms: metrics}
	c, recorder := newBindObjectMetricContext(`{"object_type_id":"orders","property_name":"duration","display_name":"履约耗时"}`)

	handler.bindObjectMetricLogicProperty(c, hydra.Visitor{})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if metrics.bindCalls != 1 {
		t.Fatalf("BindLogicProperty calls = %d, want 1", metrics.bindCalls)
	}
	got, exists := c.Get("operation_audit_target_names")
	if !exists || !reflect.DeepEqual(got, []string{"订单"}) {
		t.Fatalf("operation_audit_target_names = %#v, want []string{\"订单\"}", got)
	}
}

func TestBindObjectMetricLogicPropertyStopsWhenObjectTypeLookupFails(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	notFound := rest.NewHTTPError(context.Background(), http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound)
	objectTypes.EXPECT().GetObjectTypeByID(gomock.Any(), nil, "kn-a", "main", "missing").Return(nil, notFound)
	metrics := &objectMetricBindServiceStub{}
	handler := &restHandler{ots: objectTypes, oms: metrics}
	c, recorder := newBindObjectMetricContext(`{"object_type_id":"missing","property_name":"duration","display_name":"履约耗时"}`)

	handler.bindObjectMetricLogicProperty(c, hydra.Visitor{})

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if metrics.bindCalls != 0 {
		t.Fatalf("BindLogicProperty calls = %d, want 0", metrics.bindCalls)
	}
}
