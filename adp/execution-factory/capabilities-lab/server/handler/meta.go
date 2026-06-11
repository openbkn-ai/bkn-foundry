package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/config"
)

type MetaResponse struct {
	Service  string              `json:"service"`
	Version  string              `json:"version"`
	Features config.FeatureFlags `json:"features"`
}

func (h *CapabilitiesHandler) Meta(c *gin.Context) {
	c.JSON(http.StatusOK, MetaResponse{
		Service:  "capabilities-lab",
		Version:  h.ServiceVersion,
		Features: h.Features,
	})
}

func (h *CapabilitiesHandler) Metrics(c *gin.Context) {
	if h.MetricsCollector == nil {
		c.Status(http.StatusNotFound)
		return
	}

	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(http.StatusOK, h.MetricsCollector.RenderPrometheus())
}
