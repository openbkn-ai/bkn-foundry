// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// run_sql, list_resources and describe_resource answered with raw physical
// columns under the caller's own data-resource grants, outside the
// property-level rules the knowledge network applies to every other query.
// They were removed rather than hidden, so neither router may serve them.
func TestPhysicalQueryRoutesAreNotServed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	publicEngine := gin.New()
	(&restPublicHandler{
		Hydra:                          stubPublicHydra{},
		KnLogicPropertyResolverHandler: stubLogicPropertyResolverHandler{},
		KnActionRecallHandler:          stubActionRecallHandler{},
		KnQueryObjectInstanceHandler:   stubQueryObjectInstanceHandler{},
		KnQuerySubgraphHandler:         stubQuerySubgraphHandler{},
		KnSearchHandler:                stubKnSearchHandler{},
		KnQueryToolsHandler:            stubKnQueryToolsHandler{},
		KnSkillsHandler:                stubKnSkillsHandler{},
		KnToolsHandler:                 stubKnToolsHandler{},
		LifecycleClient:                bkntrace.NewLifecycleClient("", nil),
		Logger:                         logger.DefaultLogger(),
	}).RegisterRouter(publicEngine.Group("/api/agent-retrieval/v1"))

	privateEngine := gin.New()
	(&restPrivateHandler{
		KnLogicPropertyResolverHandler: stubLogicPropertyResolverHandler{},
		KnActionRecallHandler:          stubActionRecallHandler{},
		KnQueryObjectInstanceHandler:   stubQueryObjectInstanceHandler{},
		KnQuerySubgraphHandler:         stubQuerySubgraphHandler{},
		KnSearchHandler:                stubKnSearchHandler{},
		KnQueryToolsHandler:            stubKnQueryToolsHandler{},
		KnSkillsHandler:                stubKnSkillsHandler{},
		KnToolsHandler:                 stubKnToolsHandler{},
		MCPProxyHandler:                &countingMCPProxyHandler{},
		LifecycleClient:                bkntrace.NewLifecycleClient("", nil),
		Logger:                         logger.DefaultLogger(),
	}).RegisterRouter(privateEngine.Group("/api/agent-retrieval/internal-v1"))

	post := func(engine *gin.Engine, path string) int {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
		request.Header.Set("Authorization", "Bearer token")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(string(interfaces.HeaderXAccountID), "user-1")
		request.Header.Set(string(interfaces.HeaderXAccountType), string(interfaces.AccessorTypeUser))
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response.Code
	}

	for _, router := range []struct {
		name   string
		engine *gin.Engine
		prefix string
	}{
		{"public", publicEngine, "/api/agent-retrieval/v1"},
		{"private", privateEngine, "/api/agent-retrieval/internal-v1"},
	} {
		// The control proves the prefix is right: a 404 below then means the
		// route is gone, not that the request missed the router.
		if code := post(router.engine, router.prefix+"/kn/run_cypher"); code == http.StatusNotFound {
			t.Fatalf("%s router: run_cypher answered 404, so the removed-route checks would prove nothing", router.name)
		}
		for _, tool := range []string{"run_sql", "list_resources", "describe_resource"} {
			if code := post(router.engine, router.prefix+"/kn/"+tool); code != http.StatusNotFound {
				t.Errorf("%s router still serves /kn/%s: status=%d, want 404", router.name, tool, code)
			}
		}
	}
}
