package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestObservabilityRouteOmitsQueryAndObjectIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var logged string
	router.GET("/objects/:object_id", func(c *gin.Context) {
		logged = observabilityRoute(c)
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/objects/private-object?token=secret", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)
	if logged != "/objects/:object_id" {
		t.Fatalf("logged route = %q, want template without object identity or query", logged)
	}
}
