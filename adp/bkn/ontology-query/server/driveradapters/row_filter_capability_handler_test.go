// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
)

type rowFilterCapabilityOntologyManager struct {
	interfaces.OntologyManagerAccess
	account interfaces.AccountInfo
	called  bool
}

func (stub *rowFilterCapabilityOntologyManager) GetObjectType(ctx context.Context, knID, branch, objectTypeID string) (interfaces.ObjectType, bool, error) {
	stub.account, _ = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	stub.called = true
	return interfaces.ObjectType{KNID: knID}, true, nil
}

func rowFilterCapabilityStatus(t *testing.T, accountID, accountType string) (int, *rowFilterCapabilityOntologyManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	models := &rowFilterCapabilityOntologyManager{}
	handler := &restHandler{oma: models}
	router := gin.New()
	router.POST("/api/ontology-query/in/v1/row-filter-capabilities", handler.GetRowFilterCapabilities)
	request := httptest.NewRequest(http.MethodPost, "/api/ontology-query/in/v1/row-filter-capabilities", strings.NewReader(`{"object_type_ref":"kn-1/customer"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, accountID)
	request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, accountType)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code, models
}

func TestRowFilterCapabilityPropagatesOperatorIdentity(t *testing.T) {
	status, models := rowFilterCapabilityStatus(t, "operator-1", "user")
	if status != http.StatusOK || !models.called || models.account != (interfaces.AccountInfo{ID: "operator-1", Type: "user"}) {
		t.Fatalf("status = %d, called = %t, account = %+v", status, models.called, models.account)
	}
}

func TestRowFilterCapabilityRejectsMissingOrNonUserIdentity(t *testing.T) {
	for _, identity := range []struct{ id, kind string }{{"", "user"}, {"app-1", "app"}} {
		status, models := rowFilterCapabilityStatus(t, identity.id, identity.kind)
		if status != http.StatusForbidden || models.called {
			t.Fatalf("identity = %+v, status = %d, called = %t", identity, status, models.called)
		}
	}
}

func TestRowFilterCapabilityOnlyOffersPublishedMappedScalarDataProperties(t *testing.T) {
	capability := rowFilterCapabilityForObjectType("kn-1/customer", interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{DataProperties: []cond.DataProperty{
			{Name: "region", DisplayName: "Sales region", Type: "keyword", MappedField: cond.Field{Name: "region.keyword"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "amount", Type: "integer", MappedField: cond.Field{Name: "amount"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "active", Type: "boolean", MappedField: cond.Field{Name: "active"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "description", Type: "text", MappedField: cond.Field{Name: "description"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "ratio", Type: "double", MappedField: cond.Field{Name: "ratio"}, ConditionOperations: []string{cond.OperationIn}},
			{Name: "unmapped", Type: "keyword"},
			{Name: "when", Type: "datetime", MappedField: cond.Field{Name: "when"}},
		}},
		Status: &interfaces.ObjectTypeStatus{IndexAvailable: true},
	})
	if !capability.Published || len(capability.Properties) != 3 {
		t.Fatalf("capability = %+v", capability)
	}
	if capability.Properties["region"].Type != "string" || capability.Properties["amount"].Type != "integer" || capability.Properties["active"].Type != "boolean" {
		t.Fatalf("properties = %+v", capability.Properties)
	}
	if capability.Properties["region"].DisplayName != "Sales region" {
		t.Fatalf("region display name = %q", capability.Properties["region"].DisplayName)
	}
	if _, found := capability.Properties["description"]; found {
		t.Fatal("non-exact text field must not be exposed")
	}
	if _, found := capability.Properties["ratio"]; found {
		t.Fatal("floating-point field must not be exposed")
	}
}

func TestRowFilterCapabilityDoesNotTreatIndexStateAsPublication(t *testing.T) {
	capability := rowFilterCapabilityForObjectType("kn-1/customer", interfaces.ObjectType{})
	if !capability.Published {
		t.Fatal("an existing MAIN object model is published even before index status is available")
	}
}
