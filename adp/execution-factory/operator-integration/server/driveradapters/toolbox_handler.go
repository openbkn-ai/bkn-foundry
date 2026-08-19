package driveradapters

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/toolbox"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/business_domain"
)

// ToolBoxRestHandler toolbox rest interface.
type ToolBoxRestHandler interface {
	// RegisterPrivate register internal API.
	RegisterPrivate(engine *gin.RouterGroup)

	// RegisterPublic Register external API.
	RegisterPublic(engine *gin.RouterGroup)
}

type toolboxRestHandler struct {
	ToolBoxHandler        toolbox.ToolBoxHandler
	Logger                interfaces.Logger
	businessDomainService interfaces.IBusinessDomainService
}

var (
	tOnce    sync.Once
	tHandler ToolBoxRestHandler
)

func NewToolBoxRestHandler() ToolBoxRestHandler {
	tOnce.Do(func() {
		confLoader := config.NewConfigLoader()
		tHandler = &toolboxRestHandler{
			ToolBoxHandler:        toolbox.NewToolBoxHandler(),
			Logger:                confLoader.GetLogger(),
			businessDomainService: business_domain.NewBusinessDomainService(),
		}
	})
	return tHandler
}

// RegisterPrivate register internal API.
func (r *toolboxRestHandler) RegisterPrivate(engine *gin.RouterGroup) {
	// Toolbox related interfaces.
	// Query toolbox information.
	engine.GET("/tool-box/list", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.QueryToolBoxPage)
	engine.GET("/tool-box/:box_id", r.ToolBoxHandler.QueryToolBox)
	engine.GET("/tool-box/:box_id/tool/:tool_id", r.ToolBoxHandler.QueryTool)
	engine.GET("/tool-box/:box_id/tools/list", r.ToolBoxHandler.QueryBoxToolPage)
	engine.POST("/tool-box/:box_id/proxy/:tool_id", middlewareProxyRequest(), r.ToolBoxHandler.ExecuteTool)
}

// RegisterPublic Register external API.
func (r *toolboxRestHandler) RegisterPublic(engine *gin.RouterGroup) {
	engine.POST("/tool-box", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.CreateToolBox)
	engine.POST("/tool-box/:box_id", r.ToolBoxHandler.UpdateToolBox)
	engine.GET("/tool-box/:box_id", r.ToolBoxHandler.QueryToolBox)
	engine.DELETE("/tool-box/:box_id", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.DeleteToolBox)
	engine.GET("/tool-box/list", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.QueryToolBoxPage)
	// POST /api/agent-operator-integration/v1/tool-box/names Batch names based on toolbox ID (front-end object-level authorization page echo)
	engine.POST("/tool-box/names", r.ToolBoxHandler.QueryToolBoxNamesByIDs)
	// Tools.
	engine.POST("/tool-box/:box_id/tool", r.ToolBoxHandler.CreateTool)
	engine.POST("/tool-box/:box_id/tool/:tool_id", r.ToolBoxHandler.UpdateTool)
	engine.GET("/tool-box/:box_id/tool/:tool_id", r.ToolBoxHandler.QueryTool)
	engine.POST("/tool-box/:box_id/tools/batch-delete", r.ToolBoxHandler.DeleteBoxTool)
	engine.GET("/tool-box/:box_id/tools/list", r.ToolBoxHandler.QueryBoxToolPage)
	engine.POST("/tool-box/:box_id/tools/status", r.ToolBoxHandler.UpdateToolStatus)
	engine.POST("/tool-box/:box_id/tool/:tool_id/debug", middlewareProxyRequest(), r.ToolBoxHandler.DebugTool)
	engine.POST("/tool-box/:box_id/proxy/:tool_id", middlewareProxyRequest(), r.ToolBoxHandler.ExecuteTool)
	engine.POST("/tool-box/:box_id/status", r.ToolBoxHandler.UpdateToolBoxStatus)

	// Operators converted into tools.
	engine.POST("/operator/convert/tool", r.ToolBoxHandler.OperatorToTool)
	// OpenAPI capability package: operator registration + convert tool (unified bloodline)
	engine.POST("/capabilities/openapi-bundle", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.RegisterOpenApiBundle)
	// Get published toolbox information in batches.
	engine.GET("/tool-box/market/:box_id/:fields", r.ToolBoxHandler.GetReleaseToolBoxInfo)

	// Toolbox Market Interface.
	engine.GET("/tool-box/market", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.QueryMarketToolBoxPage)
	engine.GET("/tool-box/market/:box_id", r.ToolBoxHandler.QueryMarketToolBox)
	engine.GET("/tool-box/market/tools", middlewareBusinessDomain(true, r.businessDomainService), r.ToolBoxHandler.GetMarketToolList)
}
