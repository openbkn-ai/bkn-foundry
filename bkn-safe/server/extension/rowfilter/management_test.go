// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rowfilter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type managementServicesStub struct{}

func (managementServicesStub) AuthorizeUserRead(context.Context, string) (bool, error) {
	return true, nil
}
func (managementServicesStub) AuthorizeUserWrite(context.Context, string) (bool, error) {
	return true, nil
}
func (managementServicesStub) AuthorizeRoleRead(context.Context, string) (bool, error) {
	return true, nil
}
func (managementServicesStub) AuthorizeRoleWrite(context.Context, string) (bool, error) {
	return true, nil
}
func (managementServicesStub) ResolveCaller(context.Context, string) (Caller, error) {
	return Caller{}, nil
}
func (managementServicesStub) ResolvePublishedObjectType(context.Context, string) (PublishedObjectType, error) {
	return PublishedObjectType{}, nil
}

func managementTestRouter(t *testing.T, enterprise bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ResetForTest()
	if enterprise {
		Register(licverify.EditionEnterprise, &fakeResolver{})
		RegisterManagementHandler(licverify.EditionEnterprise, func(ManagementServices, OperatorIDResolver) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				operator, ok := operatorIDFromRequest(r)
				if !ok || operator != "operator-1" {
					http.Error(w, "missing operator", http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})
	}
	router := gin.New()
	group := router.Group("/api/safe/v1/admin", ManagementGate())
	MountManagement(group, managementServicesStub{}, func(*gin.Context) (string, bool) { return "operator-1", true })
	return router
}

func managementResponse(t *testing.T, router *gin.Engine) (int, string) {
	t.Helper()
	server := httptest.NewServer(router)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/safe/v1/admin/row-filter-policies")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func TestUnlicensedManagementRouteMatchesUnmountedRoute(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(func() { ResetForTest(); entitlement.ResetForTest() })
	communityStatus, communityBody := managementResponse(t, managementTestRouter(t, false))
	enterpriseStatus, enterpriseBody := managementResponse(t, managementTestRouter(t, true))
	if communityStatus != http.StatusNotFound || enterpriseStatus != communityStatus || enterpriseBody != communityBody {
		t.Fatalf("community=(%d,%q), enterprise=(%d,%q)", communityStatus, communityBody, enterpriseStatus, enterpriseBody)
	}
}

func TestLicensedManagementRouteReceivesAuthenticatedOperator(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() { ResetForTest(); entitlement.ResetForTest() })
	status, body := managementResponse(t, managementTestRouter(t, true))
	if status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
}
