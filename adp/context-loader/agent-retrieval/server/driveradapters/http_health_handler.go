// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// 健康检查
type httpHealthHandler struct {
	lifecycle *bkntrace.LifecycleClient
}

var (
	httpHealthOnce sync.Once
	httpHealthHand interfaces.HTTPRouterInterface
)

func NewHTTPHealthHandler() interfaces.HTTPRouterInterface {
	httpHealthOnce.Do(func() {
		httpHealthHand = newHTTPHealthHandler(bkntrace.NewLifecycleClientFromEnv())
	})

	return httpHealthHand
}

func newHTTPHealthHandler(client *bkntrace.LifecycleClient) interfaces.HTTPRouterInterface {
	return &httpHealthHandler{lifecycle: client}
}

// RegisterRouter 注册路由
func (h *httpHealthHandler) RegisterRouter(router *gin.RouterGroup) {
	router.GET("/ready", h.getReady)
	router.GET("/alive", h.getAlive)
}

func (h *httpHealthHandler) getReady(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if h.lifecycle == nil || h.lifecycle.Ready(ctx) != nil {
		c.String(http.StatusServiceUnavailable, "lifecycle core unavailable")
		return
	}
	c.String(http.StatusOK, "ready")
}

func (h *httpHealthHandler) getAlive(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.String(http.StatusOK, "alive")
}
