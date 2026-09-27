package visitor

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGenerateVisitorPreservesVerifiedInternalApplicationPrincipal(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/ontology-query/in/v1/knowledge-networks/kn/object-types/ot", nil)
	request.Header.Set("x-account-id", "user-1")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("X-BKN-Application-Principal-ID", "openbkn-sdk")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = request

	got := GenerateVisitor(ctx)
	if got.ID != "user-1" || got.ClientID != "openbkn-sdk" {
		t.Fatalf("visitor identity = (%q, %q), want (user-1, openbkn-sdk)", got.ID, got.ClientID)
	}
}

func TestGenerateVisitorIgnoresApplicationPrincipalOnPublicRoute(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/ontology-query/v1/knowledge-networks/kn/object-types/ot", nil)
	request.Header.Set("x-account-id", "user-1")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("X-BKN-Application-Principal-ID", "spoofed-client")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = request

	if got := GenerateVisitor(ctx).ClientID; got != "" {
		t.Fatalf("public application principal = %q, want empty", got)
	}
}
