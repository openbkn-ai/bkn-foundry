// Package driveradapters defines driver adapters.
// @file rest_private_handler.go
// @description: Define rest private interface adapter.
package driveradapters

import (
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/business_domain"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type restPrivateHandler struct {
	OperatorRestHandler   OperatorRestHandler
	ToolBoxRestHandler    ToolBoxRestHandler
	MCPRestHandler        MCPRestHandler
	UpgradeHandler        common.UpgradeHandler
	UnifiedProxyHandler   common.UnifiedProxyHandler
	ImpexHandler          common.ImpexHandler
	Logger                interfaces.Logger
	SkillRestHandler      SkillRestHandler
	businessDomainService interfaces.IBusinessDomainService
	Hydra                 interfaces.Hydra
}

// NewRestPrivateHandler creates a restHandler instance.
func NewRestPrivateHandler() interfaces.HTTPRouterInterface {
	return &restPrivateHandler{
		OperatorRestHandler:   NewOperatorRestHandler(),
		ToolBoxRestHandler:    NewToolBoxRestHandler(),
		MCPRestHandler:        NewMCPRestHandler(),
		UpgradeHandler:        common.NewUpgradeHandler(),
		UnifiedProxyHandler:   common.NewUnifiedProxyHandler(),
		ImpexHandler:          common.NewImpexHandler(),
		Logger:                config.NewConfigLoader().GetLogger(),
		SkillRestHandler:      NewSkillRestHandler(),
		businessDomainService: business_domain.NewBusinessDomainService(),
		Hydra:                 drivenadapters.NewHydra(),
	}
}

// RegisterRouter internal interface register route.
func (r *restPrivateHandler) RegisterRouter(engine *gin.RouterGroup) {
	mws := []gin.HandlerFunc{}
	mws = append(mws, middlewareRequestLog(r.Logger), middlewareTrace, middlewareTraceContext, sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware(), middlewareHeaderAuthContext(r.Hydra))
	engine.Use(mws...)
	// Operator interface.
	r.OperatorRestHandler.RegisterPrivate(engine)
	// toolbox interface.
	r.ToolBoxRestHandler.RegisterPrivate(engine)
	// MCP related interfaces.
	r.MCPRestHandler.RegisterPrivate(engine)
	// Skill interface.
	r.SkillRestHandler.RegisterPrivate(engine)
	// Temporary upgrade interface - only used when upgrading from an older version to 5.0.0.3.
	engine.GET("/upgrade/v5003/migrate-history", r.UpgradeHandler.MigrateHistoryData)
	// V0.6.0 -> V0.7.0 upgrade interface.
	engine.POST("/upgrade/v070/migrate-history", r.UpgradeHandler.UpgradeSkillV070)
	// Function sandbox execution.
	engine.POST("/function/exec/:version", middlewareBusinessDomain(true, r.businessDomainService), r.UnifiedProxyHandler.FunctionExecuteProxy)
}
