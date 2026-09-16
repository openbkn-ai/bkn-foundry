// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

func TestAuthorizationResourcesForwardsKnowledgeNetworkQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bkn-backend/in/v1/authorization-resources" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "supply" {
			t.Fatalf("name = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Fatalf("limit = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[{"id":"kn-1","name":"Supply"}],"total":1}`))
	}))
	defer backend.Close()
	catalog, err := NewAuthorizationResourceCatalog(config.UpstreamConfig{BaseURL: backend.URL, Timeout: time.Second}, config.UpstreamConfig{BaseURL: backend.URL, Timeout: time.Second}, config.UpstreamConfig{BaseURL: backend.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerAuthorizationResources(r.Group("/api/safe/v1/admin"), catalog)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=knowledge_network&name=supply&limit=50", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"entries":[{"id":"kn-1","name":"Supply"}],"total":1}` {
		t.Fatalf("response = %s", got)
	}
}

func TestAuthorizationResourcesRejectsUnsupportedResourceType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAuthorizationResources(r.Group("/api/safe/v1/admin"), &authorizationResourceCatalog{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=vega_resource", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAuthorizationResourcesForwardsExecutionFactoryQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	executionFactory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-operator-integration/internal-v1/authorization-resources" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("resource_type"); got != "skill" {
			t.Fatalf("resource_type = %q", got)
		}
		if got := r.URL.Query().Get("direction"); got != "desc" {
			t.Fatalf("direction = %q", got)
		}
		_, _ = w.Write([]byte(`{"entries":[{"id":"skill-1","name":"Runbook"}],"total":1}`))
	}))
	defer executionFactory.Close()
	catalog, err := NewAuthorizationResourceCatalog(
		config.UpstreamConfig{BaseURL: executionFactory.URL, Timeout: time.Second},
		config.UpstreamConfig{BaseURL: executionFactory.URL, Timeout: time.Second},
		config.UpstreamConfig{BaseURL: executionFactory.URL, Timeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerAuthorizationResources(r.Group("/api/safe/v1/admin"), catalog)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=skill&direction=desc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthorizationResourcesForwardsVegaQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vega := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vega-backend/in/v1/authorization-resources" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("resource_type"); got != "catalog" {
			t.Fatalf("resource_type = %q", got)
		}
		if got := r.URL.Query().Get("name"); got != "supply" {
			t.Fatalf("name = %q", got)
		}
		_, _ = w.Write([]byte(`{"entries":[{"id":"catalog-1","name":"Supply"}],"total":1}`))
	}))
	defer vega.Close()

	upstream := config.UpstreamConfig{BaseURL: vega.URL, Timeout: time.Second}
	catalog, err := NewAuthorizationResourceCatalog(upstream, upstream, upstream)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerAuthorizationResources(r.Group("/api/safe/v1/admin"), catalog)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=catalog&name=supply", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthorizationResourcesForwardsVegaResourceQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vega := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vega-backend/in/v1/authorization-resources" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("resource_type"); got != "resource" {
			t.Fatalf("resource_type = %q", got)
		}
		_, _ = w.Write([]byte(`{"entries":[{"id":"resource-1","name":"Orders"}],"total":1}`))
	}))
	defer vega.Close()

	upstream := config.UpstreamConfig{BaseURL: vega.URL, Timeout: time.Second}
	catalog, err := NewAuthorizationResourceCatalog(upstream, upstream, upstream)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerAuthorizationResources(r.Group("/api/safe/v1/admin"), catalog)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=resource", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}
