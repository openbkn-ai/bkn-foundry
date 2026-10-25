// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/authzresource"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

type extensionProvider struct {
	queries []authzresource.Query
}

func (p *extensionProvider) List(_ context.Context, query authzresource.Query) (authzresource.Page, error) {
	p.queries = append(p.queries, query)
	return authzresource.Page{Entries: []authzresource.Resource{{ID: "7", Name: "合同库"}}, Total: 1}, nil
}

func (p *extensionProvider) Exists(context.Context, string) (bool, error) { return true, nil }

func registerKnowledgeBase(t *testing.T, edition licverify.Edition) *extensionProvider {
	t.Helper()
	entitlement.ResetForTest()
	authzresource.ResetForTest()
	t.Cleanup(func() {
		authzresource.ResetForTest()
		entitlement.ResetForTest()
	})
	entitlement.SetGateForTest(entitlement.FixedGate(edition))
	provider := &extensionProvider{}
	authzresource.Register("knowledge_base", "knowledge_base", licverify.EditionProfessional, provider)
	return provider
}

func TestAuthorizationResourcesServeAnExtensionType(t *testing.T) {
	provider := registerKnowledgeBase(t, licverify.EditionProfessional)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAuthorizationResources(r.Group("/api/safe/v1/admin"), &authorizationResourceCatalog{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=knowledge_base&name=合同&offset=20&limit=10", nil))
	if w.Code != http.StatusOK || w.Body.String() != `{"entries":[{"id":"7","name":"合同库"}],"total":1}` {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if got := provider.queries[0]; got.Name != "合同" || got.Offset != 20 || got.Limit != 10 || got.Sort != "name" {
		t.Fatalf("forwarded query = %+v", got)
	}

	// Extension types are roots.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/admin/authorization-resources?resource_type=knowledge_base&parent_type=catalog&parent_id=c1", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("parent filter status = %d", w.Code)
	}

	resource, err := (&authorizationResourceCatalog{}).Resolve(t.Context(), "knowledge_base", "7")
	if err != nil || resource.Name != "合同库" {
		t.Fatalf("Resolve() = (%+v, %v)", resource, err)
	}
}

func TestAuthorizationResourcesHideAnUnlicensedExtensionType(t *testing.T) {
	registerKnowledgeBase(t, licverify.EditionCommunity)
	_, err := (&authorizationResourceCatalog{}).List(t.Context(), "knowledge_base", AuthorizationResourceQuery{Limit: 10})
	if !errors.Is(err, errUnsupportedResourceType) {
		t.Fatalf("List() error = %v, want unsupported", err)
	}
}
