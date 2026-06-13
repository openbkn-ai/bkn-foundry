package main

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/client"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/config"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/handler"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/logic"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/observability"
)

func main() {
	cfg := config.Load()
	gin.SetMode(gin.ReleaseMode)

	metrics := &observability.Metrics{}

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(handler.RequestIDMiddleware())
	engine.Use(handler.AuthMiddleware(cfg.DefaultUserID))
	engine.Use(handler.MetricsMiddleware(metrics))
	engine.Use(handler.AuditMiddleware())

	oiClient := client.NewOperatorIntegrationClient(cfg.OperatorIntegrationURL)
	service := &logic.Service{
		Client:        oiClient,
		DefaultUserID: cfg.DefaultUserID,
	}
	capabilitiesHandler := handler.NewCapabilitiesHandler(cfg, service, metrics)

	api := engine.Group("/api/capabilities-lab/v1")
	api.Use(handler.FeatureGateMiddleware(cfg.Features))
	capabilitiesHandler.RegisterRoutes(api)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	if err := engine.Run(addr); err != nil {
		panic(err)
	}
}
