// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type routePropertyResolver struct{}

func (routePropertyResolver) Resolve(context.Context, permdata.Request) (permdata.Resolution, error) {
	return permdata.Resolution{}, nil
}

func propertyGrantRouter(t *testing.T, register bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	permdata.ResetForTest()
	if register {
		permdata.Register(licverify.EditionEnterprise, routePropertyResolver{})
		permdata.RegisterManagementHandler(licverify.EditionEnterprise, func(
			_ permdata.ManagementServices,
			operatorID permdata.OperatorIDResolver,
		) http.Handler {
			return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				operator, ok := operatorID(request)
				if !ok {
					response.WriteHeader(http.StatusUnauthorized)
					return
				}
				response.Header().Set("X-Operator-ID", operator)
				response.WriteHeader(http.StatusNoContent)
			})
		})
	}
	if err := db.Create(&model.User{ID: "operator-1", Account: "operator-1", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	return New(Deps{
		Enforcer: enforcer, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		TokenVerifier: stubVerifier{},
	})
}

func TestPropertyGrantGateRunsBeforeAuthentication(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(func() {
		permdata.ResetForTest()
		entitlement.ResetForTest()
	})
	type wireResponse struct {
		status int
		header http.Header
		body   string
	}
	probe := func(router *gin.Engine) wireResponse {
		server := httptest.NewServer(router)
		defer server.Close()
		response, err := server.Client().Get(server.URL + "/api/safe/v1/admin/property-grants")
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
		return wireResponse{status: response.StatusCode, header: header, body: string(body)}
	}
	community := probe(propertyGrantRouter(t, false))
	enterprise := probe(propertyGrantRouter(t, true))
	if enterprise.status != community.status || enterprise.body != community.body ||
		!reflect.DeepEqual(enterprise.header, community.header) {
		t.Fatalf("unauthenticated probe distinguishes unavailable Enterprise route:\nenterprise=%d %q headers=%v\ncommunity=%d %q headers=%v",
			enterprise.status, enterprise.body, enterprise.header,
			community.status, community.body, community.header)
	}
	if enterprise.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", enterprise.status)
	}
}

func TestLicensedPropertyGrantRouteAuthenticatesAndBridgesOperator(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() {
		permdata.ResetForTest()
		entitlement.ResetForTest()
	})
	router := propertyGrantRouter(t, true)
	request := httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/property-grants", nil)
	request.Header.Set("Authorization", "Bearer operator-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if operator := response.Header().Get("X-Operator-ID"); operator != "operator-1" {
		t.Fatalf("operator = %q, want operator-1", operator)
	}
}
