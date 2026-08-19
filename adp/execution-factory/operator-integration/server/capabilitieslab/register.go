// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package capabilitieslab assembles the routes for the original capabilities-lab service.
//
// The service was originally an independent process and relied on the public API of this service through HTTP. After merging it becomes.
// A routing group for this service. The path remains /api/capabilities-lab/v1 and the consumer only needs to change it.
// The host of the base URL.
//
// This merger deliberately does not change its internal implementation: the logic layer still accesses the service through the client package via HTTP.
// Public API (default OPERATOR_INTEGRATION_URL=http://127.0.0.1:9000, which is itself).
// This kind of merger is a pure code relocation, the behavior remains unchanged byte by byte, and the regression surface converges to "whether the route is registered correctly.".
// This one thing. Replacing the client's HTTP calls with direct logic is a subsequent internal reconstruction, which is very important to the consumer.
// Invisible, can be advanced by subdomain.
package capabilitieslab

import (
	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/handler"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/logic"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/observability"
)

// RegisterRouter assembles capabilities-lab's middleware chain with all routes on a given route group.
//
// The middleware was originally hung on the engine of the independent service (engine.Use of main.go). Change here to hang on.
// On the routing group: the health / meta / metrics of the original service are also registered in the same group, so the group level.
// The mount is equivalent to the original engine-level mount and will not affect other routing groups of this service.
func RegisterRouter(group *gin.RouterGroup) {
	cfg := config.Load()
	metrics := &observability.Metrics{}

	oiClient := client.NewOperatorIntegrationClient(cfg.OperatorIntegrationURL)
	service := &logic.Service{
		Client:        oiClient,
		DefaultUserID: cfg.DefaultUserID,
	}

	group.Use(
		sharedrest.LanguageMiddleware(),
		sharedrest.PrivateNoCacheMiddleware(),
		handler.RequestIDMiddleware(),
		handler.AuthMiddleware(cfg.DefaultUserID),
		handler.MetricsMiddleware(metrics),
		handler.AuditMiddleware(),
		handler.FeatureGateMiddleware(cfg.Features),
	)
	handler.NewCapabilitiesHandler(cfg, service, metrics).RegisterRoutes(group)
}
