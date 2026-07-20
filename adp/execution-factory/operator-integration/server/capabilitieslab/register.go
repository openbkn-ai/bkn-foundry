// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package capabilitieslab 装配原 capabilities-lab 服务的路由。
//
// 该服务原是独立进程，通过 HTTP 反向依赖本服务的公开 API。合并进来之后它成为
// 本服务的一个路由组，路径保持 /api/capabilities-lab/v1 不变，消费方只需改
// base URL 的 host。
//
// 本次合并刻意不改动其内部实现：logic 层仍经 client 包以 HTTP 访问本服务的
// 公开 API（默认 OPERATOR_INTEGRATION_URL=http://127.0.0.1:9000，即自身）。
// 这样合并是纯粹的代码搬迁，行为逐字节不变，回归面收敛到「路由是否注册正确」
// 这一件事。把 client 的 HTTP 调用换成直调 logics 是后续的内部重构，对消费方
// 不可见，可逐子域推进。
package capabilitieslab

import (
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/handler"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/logic"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/capabilitieslab/observability"
)

// RegisterRouter 在给定路由组上装配 capabilities-lab 的中间件链与全部路由。
//
// 中间件原先挂在独立服务的 engine 上（main.go 的 engine.Use）。此处改为挂在
// 路由组上：原服务的 health / meta / metrics 三条也注册在同一个组内，因此组级
// 挂载与原先的 engine 级挂载等价，且不会波及本服务其余路由组。
func RegisterRouter(group *gin.RouterGroup) {
	cfg := config.Load()
	metrics := &observability.Metrics{}

	oiClient := client.NewOperatorIntegrationClient(cfg.OperatorIntegrationURL)
	service := &logic.Service{
		Client:        oiClient,
		DefaultUserID: cfg.DefaultUserID,
	}

	group.Use(
		handler.RequestIDMiddleware(),
		handler.AuthMiddleware(cfg.DefaultUserID),
		handler.MetricsMiddleware(metrics),
		handler.AuditMiddleware(),
		handler.FeatureGateMiddleware(cfg.Features),
	)
	handler.NewCapabilitiesHandler(cfg, service, metrics).RegisterRoutes(group)
}
