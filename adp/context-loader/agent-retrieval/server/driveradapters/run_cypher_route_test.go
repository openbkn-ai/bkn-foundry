// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
)

// TestBothFacesRegisterEveryKnRoute keeps the external and internal faces in
// step. A tool reachable on one face and missing on the other is the shape of
// the get_action_info regression (#291): the MCP surface keeps working while
// the toolbox entry 404s, or the reverse, and nothing else notices.
func TestBothFacesRegisterEveryKnRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	publicEngine := gin.New()
	public := &restPublicHandler{
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
	}
	public.RegisterRouter(publicEngine.Group("/api/agent-retrieval/v1"))

	privateEngine := gin.New()
	private := &restPrivateHandler{
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
	}
	private.RegisterRouter(privateEngine.Group("/api/agent-retrieval/internal-v1"))

	publicRoutes := knRoutesOf(publicEngine, "/api/agent-retrieval/v1")
	privateRoutes := knRoutesOf(privateEngine, "/api/agent-retrieval/internal-v1")

	if _, ok := publicRoutes["POST /kn/run_cypher"]; !ok {
		t.Fatal("public face does not register /kn/run_cypher")
	}
	if _, ok := privateRoutes["POST /kn/run_cypher"]; !ok {
		t.Fatal("private face does not register /kn/run_cypher")
	}
	// The private face carries a few endpoints that are deliberately internal
	// only, so the invariant runs one way: everything public must also exist
	// internally.
	for route := range publicRoutes {
		if _, ok := privateRoutes[route]; !ok {
			t.Fatalf("%s is public only; the internal face must register it too", route)
		}
	}
}

func knRoutesOf(engine *gin.Engine, prefix string) map[string]struct{} {
	routes := map[string]struct{}{}
	for _, route := range engine.Routes() {
		path := strings.TrimPrefix(route.Path, prefix)
		if !strings.HasPrefix(path, "/kn/") {
			continue
		}
		routes[route.Method+" "+path] = struct{}{}
	}
	return routes
}
