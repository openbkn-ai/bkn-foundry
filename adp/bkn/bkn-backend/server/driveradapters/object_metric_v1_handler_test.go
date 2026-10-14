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
	c.Request = httptest.NewRequest(http.MethodPost, "/api/bkn-backend/v2/knowledge-networks/kn-a/object-metrics/metric-a/logic-properties?version=2&branch=main", strings.NewReader(body))
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
