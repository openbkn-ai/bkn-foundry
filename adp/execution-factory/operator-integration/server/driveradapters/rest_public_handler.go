// Package driveradapters defines driver adapters.
// @file rest_public_handler.go
// @description: Define rest public adapter.
package driveradapters

import (
	"context"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/common"
	sandboxdriver "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/sandbox"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/bknaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type restPublicHandler struct {
	Hydra               interfaces.Hydra
	AppKeys             interfaces.AppKeyVerifier
	SandboxHandler      sandboxdriver.ManagementHandler
	OperatorRestHandler OperatorRestHandler
	ToolBoxRestHandler  ToolBoxRestHandler
	MCPRestHandler      MCPRestHandler
	SkillRestHandler    SkillRestHandler
	ImpexHandler        common.ImpexHandler
	UnifiedProxyHandler common.UnifiedProxyHandler
	TemplateHandler     common.TemplateHandler
	AIGenerationHandler common.AIGenerationHandler
	Logger              interfaces.Logger
	auditRecorder       interface {
		Record(context.Context, operationaudit.Entry) error
	}
}

// NewRestPublicHandler creates a restHandler instance.
func NewRestPublicHandler() interfaces.HTTPRouterInterface {
	logger := config.NewConfigLoader().GetLogger()
	return &restPublicHandler{
		Hydra:               drivenadapters.NewHydra(),
		AppKeys:             drivenadapters.NewAppKeyVerifier(),
		SandboxHandler:      sandboxdriver.NewManagementHandler(),
		OperatorRestHandler: NewOperatorRestHandler(),
		ToolBoxRestHandler:  NewToolBoxRestHandler(),
		MCPRestHandler:      NewMCPRestHandler(),
		SkillRestHandler:    NewSkillRestHandler(),
		ImpexHandler:        common.NewImpexHandler(),
		UnifiedProxyHandler: common.NewUnifiedProxyHandler(),
		TemplateHandler:     common.NewTemplateHandler(),
		AIGenerationHandler: common.NewAIGenerationHandler(),
		Logger:              logger,
		auditRecorder:       operationaudit.NewKafkaRecorder(bknaudit.ConfiguredPublisher(logger), strings.TrimSpace(os.Getenv("BKN_AUDIT_ENVIRONMENT"))),
	}
}

// RegisterPublic registers public routes.
func (r *restPublicHandler) RegisterRouter(engine *gin.RouterGroup) {
	mws := []gin.HandlerFunc{}
	mws = append(mws,
		middlewareRequestLog(r.Logger),
		middlewareTrace,
		middlewareTraceContext,
		sharedrest.LanguageMiddleware(),
		sharedrest.PrivateNoCacheMiddleware(),
		stripProxyExecutionHeaders(),
		OperationAudit(r.auditRecorder),
		middlewareIntrospectVerify(r.Hydra, r.AppKeys),
	)
	engine.Use(mws...)
	// Operator registration related interfaces.
	r.OperatorRestHandler.RegisterPublic(engine)
	// Toolbox related interfaces.
	r.ToolBoxRestHandler.RegisterPublic(engine)
	// MCP related interfaces.
	r.MCPRestHandler.RegisterPublic(engine)
	// Skill related interfaces.
	r.SkillRestHandler.RegisterPublic(engine)
	// Read-only observation interface when running in the sandbox (visible to super pipe, see #326)
	r.SandboxHandler.RegisterPublic(engine)
	// Import and export.
	engine.GET("/impex/export/:type/:id", r.ImpexHandler.Export)
	engine.POST("/impex/import/:type", r.ImpexHandler.Import)
	// function execution.
	engine.POST("/function/execute", r.UnifiedProxyHandler.FunctionExecute)

	// Deducing parameter definitions from function code (the signature of the @tool function is the parameter definition)
	engine.POST("/function/infer-schema", r.UnifiedProxyHandler.FunctionInferSchema)
	// Query PyPI dependency version.
	engine.GET("/function/dependency-versions/:package_name", r.UnifiedProxyHandler.QueryPypiVersions)
	// Get the list of dependent libraries.
	engine.GET("/function/dependencies", r.UnifiedProxyHandler.GetDependencies)
	// Get Python template.
	engine.GET("/template/:template_type", r.TemplateHandler.GetTemplate)
	// AI-assisted generation.
	engine.POST("/ai_generate/function/:type", r.AIGenerationHandler.FunctionAIGeneration)
	// Get prompt word template.
	engine.GET("/ai_generate/prompt/:type", r.AIGenerationHandler.GetPromptTemplate)
}
