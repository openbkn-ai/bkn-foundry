// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func runKNVersionMiddleware(t *testing.T, target string) (*httptest.ResponseRecorder, *interfaces.KNVersionPin) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var pin *interfaces.KNVersionPin
	engine := gin.New()
	engine.Use((&restHandler{}).KNVersionMiddleware())
	engine.GET("/q", func(c *gin.Context) {
		pin = interfaces.KNVersionPinFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder, pin
}

func TestKNVersionMiddlewarePinsTheRequestedVersion(t *testing.T) {
	for target, want := range map[string]string{
		"/q":                                   "",
		"/q?kn_version=running":                "&kn_version=running",
		"/q?kn_version=3":                      "&kn_version=3",
		"/q?kn_version=draft":                  "&kn_version=draft",
		"/q?kn_version=draft&draft_revision=7": "&kn_version=draft&draft_revision=7",
	} {
		recorder, pin := runKNVersionMiddleware(t, target)
		if recorder.Code != http.StatusNoContent || pin == nil {
			t.Fatalf("%s: status %d, pin %v", target, recorder.Code, pin)
		}
		if got := pin.QueryString(); got != want {
			t.Errorf("%s: QueryString() = %q, want %q", target, got, want)
		}
	}
}

func TestKNVersionMiddlewareRejectsInvalidVersions(t *testing.T) {
	for _, target := range []string{
		"/q?kn_version=0", "/q?kn_version=abc", "/q?kn_version=3&draft_revision=1", "/q?draft_revision=1",
	} {
		recorder, pin := runKNVersionMiddleware(t, target)
		if recorder.Code != http.StatusBadRequest || pin != nil {
			t.Errorf("%s: status %d, want 400 before the handler", target, recorder.Code)
		}
	}
}
