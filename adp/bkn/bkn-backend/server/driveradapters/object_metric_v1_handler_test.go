// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestBindObjectMetricDefinitionPreservesExactLiterals(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		`{"specification":{"measure":{"expression":{"node_type":"literal","value_type":"decimal","value":0.1234567890123456789012345}}}}`))
	var definition interfaces.ObjectMetricDefinitionV1
	if err := bindObjectMetricDefinitionV1(ctx, &definition); err != nil {
		t.Fatal(err)
	}
	value := definition.Specification.Measure.Expression.Value
	if got, ok := value.(json.Number); !ok || got.String() != "0.1234567890123456789012345" {
		t.Fatalf("literal = %#v, want exact json.Number", value)
	}
}

func TestBindObjectMetricDefinitionRejectsRemovedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, field := range []string{"calculation_scope", "logic_property"} {
		t.Run(field, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"`+field+`":{}}`))
			var definition interfaces.ObjectMetricDefinitionV1
			err := bindObjectMetricDefinitionV1(ctx, &definition)
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("error = %v, want unknown-field rejection", err)
			}
		})
	}
}

func TestBindObjectMetricDefinitionRejectsTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{} {}`, `{} invalid`} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var definition interfaces.ObjectMetricDefinitionV1
		if err := bindObjectMetricDefinitionV1(ctx, &definition); err == nil {
			t.Fatalf("body %q: expected trailing JSON to be rejected", body)
		}
	}
}

type objectMetricUpdateServiceStub struct {
	interfaces.ObjectMetricServiceV1
	updateCalls int
}

type objectMetricDeleteServiceStub struct {
	interfaces.ObjectMetricServiceV1
	called  bool
	version int
	err     error
}

func (s *objectMetricDeleteServiceStub) DeleteVersion(_ context.Context, _, _, _ string, version int) (*interfaces.ObjectMetricRecordV1, error) {
	s.called = true
	s.version = version
	if s.err != nil {
		return nil, s.err
	}
	return &interfaces.ObjectMetricRecordV1{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Name: "Metric one", Version: version}}, nil
}

func TestDeleteObjectMetricVersionRequiresExplicitVersion(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	service := &objectMetricDeleteServiceStub{}
	handler := &restHandler{oms: service}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics/m-1", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "m-1"}}

	handler.deleteObjectMetricVersion(c, hydra.Visitor{})
	if recorder.Code != http.StatusBadRequest || service.called {
		t.Fatalf("status=%d called=%v body=%s", recorder.Code, service.called, recorder.Body.String())
	}
}

func TestDeleteObjectMetricVersionAuditsExactVersionOnFailure(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	service := &objectMetricDeleteServiceStub{err: errors.New("blocked")}
	handler := &restHandler{oms: service}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics/m-1?version=3", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "m-1"}}

	handler.deleteObjectMetricVersion(c, hydra.Visitor{})
	targets, _ := c.Get("operation_audit_target_ids")
	if !service.called || service.version != 3 || !reflect.DeepEqual(targets, []string{"m-1@3"}) {
		t.Fatalf("called=%v version=%d audit targets=%#v", service.called, service.version, targets)
	}
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
	result  *interfaces.ObjectMetricListV1
}

type objectMetricGetServiceStub struct {
	interfaces.ObjectMetricServiceV1
	record *interfaces.ObjectMetricRecordV1
}

func (s *objectMetricGetServiceStub) Get(context.Context, string, string, string, int) (*interfaces.ObjectMetricRecordV1, error) {
	return s.record, nil
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
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	service := &objectMetricVersionsServiceStub{versions: []*interfaces.ObjectMetricRecordV1{
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 1, Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "published"}}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 3, Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "draft"}}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", Version: 2, OwnerObjectTypeID: "orders", Lifecycle: interfaces.ObjectMetricLifecycleV1{Status: "published"}}},
	}}
	handler := &restHandler{oms: service, ots: objectTypes}
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
	if s.result != nil {
		return s.result, nil
	}
	return &interfaces.ObjectMetricListV1{Entries: []*interfaces.ObjectMetricRecordV1{}, TotalCount: 0}, nil
}

func TestListObjectMetricsByInForwardsCallerIdentityAndPublishedFilter(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	service := &objectMetricListServiceStub{result: &interfaces.ObjectMetricListV1{Entries: []*interfaces.ObjectMetricRecordV1{{
		Definition: interfaces.ObjectMetricDefinitionV1{ID: "m-1", OwnerObjectTypeID: "orders"},
	}}, TotalCount: 1}}
	handler := &restHandler{oms: service, ots: objectTypes}
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
	if strings.Contains(recorder.Body.String(), `"owner_object_type_name"`) {
		t.Fatalf("internal list unexpectedly projected owner name: %s", recorder.Body.String())
	}
}

func TestListObjectMetricsProjectsAuthorizedMetricOwnerNames(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	objectTypes.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), "kn-a", "main", []string{"orders", "customers", "deleted"}, false).Return(map[string]*interfaces.ObjectType{
		"orders": {
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders", OTName: "销售订单"},
		},
		"customers": {
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customers", OTName: "客户"},
		},
	}, nil)
	service := &objectMetricListServiceStub{result: &interfaces.ObjectMetricListV1{
		Entries: []*interfaces.ObjectMetricRecordV1{
			{Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-1", OwnerObjectTypeID: "orders"}},
			{Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-2", OwnerObjectTypeID: "orders"}},
			{Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-3", OwnerObjectTypeID: "customers"}},
			{Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-4", OwnerObjectTypeID: "deleted"}, OwnerObjectTypeName: "stale owner name"},
		},
		TotalCount: 4,
	}}
	handler := &restHandler{oms: service, ots: objectTypes}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics?branch=main", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}}

	handler.listObjectMetrics(c, hydra.Visitor{}, true)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	var response interfaces.ObjectMetricListV1
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, recorder.Body.String())
	}
	if len(response.Entries) != len(service.result.Entries) {
		t.Fatalf("response entries = %d, want %d", len(response.Entries), len(service.result.Entries))
	}
	for index, want := range []string{"销售订单", "销售订单", "客户"} {
		if got := service.result.Entries[index].OwnerObjectTypeName; got != "" {
			t.Fatalf("source entry[%d].OwnerObjectTypeName = %q, want empty", index, got)
		}
		entry := response.Entries[index]
		if entry == nil || entry.Definition.ID != service.result.Entries[index].Definition.ID {
			t.Fatalf("response entry[%d] = %#v, want metric %q", index, entry, service.result.Entries[index].Definition.ID)
		}
		if got := entry.OwnerObjectTypeName; got != want {
			t.Fatalf("response entry[%d].OwnerObjectTypeName = %q, want %q", index, got, want)
		}
	}
	if service.result.Entries[3].OwnerObjectTypeName != "stale owner name" ||
		response.Entries[3] == nil ||
		response.Entries[3].Definition.ID != "metric-4" ||
		response.Entries[3].OwnerObjectTypeName != "" {
		t.Fatalf("stale owner name leaked or source changed: %s", recorder.Body.String())
	}
}

func TestGetObjectMetricProjectsAuthorizedMetricOwnerName(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	objectTypes.EXPECT().GetObjectTypesMapByIDs(gomock.Any(), "kn-a", "main", []string{"orders"}, false).Return(map[string]*interfaces.ObjectType{
		"orders": {
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders", OTName: "销售订单"},
		},
	}, nil)
	service := &objectMetricGetServiceStub{record: &interfaces.ObjectMetricRecordV1{
		Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-1", OwnerObjectTypeID: "orders"},
	}}
	handler := &restHandler{oms: service, ots: objectTypes}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn-a/object-metrics/metric-1?branch=main", nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}, {Key: "metric_id", Value: "metric-1"}}

	handler.getObjectMetric(c, hydra.Visitor{})

	if recorder.Code != http.StatusOK || service.record.OwnerObjectTypeName != "" ||
		!strings.Contains(recorder.Body.String(), `"owner_object_type_name":"销售订单"`) {
		t.Fatalf("status=%d record=%#v body=%s", recorder.Code, service.record, recorder.Body.String())
	}
}
