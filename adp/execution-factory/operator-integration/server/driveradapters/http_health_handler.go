// Package driveradapters defines driver adapters.
// @file http_health_handler.go
// @description: Define HTTP health check adapter.
package driveradapters

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// health check.
type httpHealthHandler struct{}

var (
	httpHealthOnce sync.Once
	httpHealthHand interfaces.HTTPRouterInterface
)

func NewHTTPHealthHandler() interfaces.HTTPRouterInterface {
	httpHealthOnce.Do(func() {
		httpHealthHand = &httpHealthHandler{}
	})

	return httpHealthHand
}

// RegisterRouter register route.
func (h *httpHealthHandler) RegisterRouter(router *gin.RouterGroup) {
	router.GET("/ready", h.getReady)
	router.GET("/alive", h.getAlive)
}

func (h *httpHealthHandler) getReady(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.String(http.StatusOK, "ready")
}

func (h *httpHealthHandler) getAlive(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.String(http.StatusOK, "alive")
}
