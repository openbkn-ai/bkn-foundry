// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
)

func TestMiddlewareCallerScopedAuthorizationEnablesAuthorizationBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/caller", middlewareCallerScopedAuthorization(), func(c *gin.Context) {
		if !common.IsPublicAPIFromCtx(c.Request.Context()) {
			t.Error("caller-scoped request must use the regular authorization branches")
		}
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/caller", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want %d", recorder.Code, http.StatusNoContent)
	}
}
