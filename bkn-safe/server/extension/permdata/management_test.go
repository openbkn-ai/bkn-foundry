// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permdata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

type managementServicesStub struct{}

func (managementServicesStub) AuthorizeUserGrants(context.Context, string, string) (UserGrantAuthority, error) {
	return UserGrantAuthority{Allowed: true, Unrestricted: true}, nil
}

func (managementServicesStub) AuthorizePlatformRoleGrants(context.Context, string) (bool, error) {
	return true, nil
}

func (managementServicesStub) EffectivePropertyLevels(context.Context, string, string, []string) (map[string]propertyaccess.Level, error) {
	return map[string]propertyaccess.Level{}, nil
}

func registerManagementTestHandler(t *testing.T) {
	t.Helper()
	Register(licverify.EditionEnterprise, &fakeResolver{})
	RegisterManagementHandler(licverify.EditionEnterprise, func(_ ManagementServices, operatorID OperatorIDResolver) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			operator, ok := operatorID(request)
			if !ok {
				http.Error(response, "missing operator", http.StatusUnauthorized)
				return
			}
			response.Header().Set("X-Operator-ID", operator)
			response.WriteHeader(http.StatusNoContent)
		})
	})
}

func managementRouter(t *testing.T, withEnterpriseHandler bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ResetForTest()
	if withEnterpriseHandler {
		registerManagementTestHandler(t)
	}
	router := gin.New()
	group := router.Group("/api/safe/v1/admin", ManagementGate())
	MountManagement(group, managementServicesStub{}, func(*gin.Context) (string, bool) {
		return "operator-1", true
	})
	return router
}

type managementWireResponse struct {
	status int
	header http.Header
	body   string
}

func serveManagement(t *testing.T, router *gin.Engine) managementWireResponse {
	t.Helper()
	server := httptest.NewServer(router)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/safe/v1/admin/property-grants", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	header := response.Header.Clone()
	header.Del("Date")
	return managementWireResponse{status: response.StatusCode, header: header, body: string(body)}
}

func TestUnlicensedManagementResponseMatchesCommunity(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(func() {
		ResetForTest()
		entitlement.ResetForTest()
	})
	community := serveManagement(t, managementRouter(t, false))
	enterprise := serveManagement(t, managementRouter(t, true))

	if community.status != http.StatusNotFound {
		t.Fatalf("community status = %d, want 404", community.status)
	}
	if enterprise.status != community.status || enterprise.body != community.body {
		t.Fatalf("enterprise response = %d %q, community = %d %q",
			enterprise.status, enterprise.body, community.status, community.body)
	}
	for key, values := range community.header {
		if strings.Join(enterprise.header.Values(key), ",") != strings.Join(values, ",") {
			t.Fatalf("header %s = %v, community = %v", key, enterprise.header.Values(key), values)
		}
	}
	for key, values := range enterprise.header {
		if _, exists := community.header[key]; !exists {
			t.Fatalf("enterprise response has extra header %s=%v", key, values)
		}
	}
}

func TestLicensedManagementRouteReceivesAuthenticatedOperator(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() {
		ResetForTest()
		entitlement.ResetForTest()
	})
	router := managementRouter(t, true)
	request := httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/property-grants", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if operator := response.Header().Get("X-Operator-ID"); operator != "operator-1" {
		t.Fatalf("operator = %q, want operator-1", operator)
	}
}

func TestManagementRegistrationRequiresMatchingResolver(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	assertPanics := func(name string, call func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			call()
		})
	}
	assertPanics("resolver missing", func() {
		RegisterManagementHandler(licverify.EditionEnterprise, func(ManagementServices, OperatorIDResolver) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		})
	})
	Register(licverify.EditionEnterprise, &fakeResolver{})
	assertPanics("edition mismatch", func() {
		RegisterManagementHandler(licverify.EditionProfessional, func(ManagementServices, OperatorIDResolver) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		})
	})
}
