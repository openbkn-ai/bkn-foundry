// Package driveradapters defines driver adapters.
// @file rest_public_handler.go
// @description: Define rest public adapter.
package driveradapters

import (
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/common"
	sandboxdriver "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/sandbox"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/db"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
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
	auditStore          *operationaudit.Store
	auditQueryStore     operationAuditQueryStore
	auditAuthorization  interfaces.IAuthorizationService
}

// NewRestPublicHandler creates a restHandler instance.
func NewRestPublicHandler() interfaces.HTTPRouterInterface {
	auditStore := operationaudit.NewStore(db.NewDBPool())
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
		Logger:              config.NewConfigLoader().GetLogger(),
		auditStore:          auditStore,
		auditQueryStore:     auditStore,
		auditAuthorization:  auth.NewAuthServiceImpl(),
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
		middlewareIntrospectVerify(r.Hydra, r.AppKeys),
		OperationAudit(r.auditStore),
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
	engine.GET("/operation-audits", r.ListOperationAudits)
	engine.GET("/operation-audits/:event_id", r.GetOperationAudit)
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
