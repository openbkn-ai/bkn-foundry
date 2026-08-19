// Package toolbox toolbox operation adapter.
// @file index.go
// @description: toolbox operation adapter.
package toolbox

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	ltoolbox "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/toolbox"
)

// ToolBoxHandler toolbox operation interface.
type ToolBoxHandler interface {
	// Toolbox operation interface.
	CreateToolBox(c *gin.Context)
	UpdateToolBox(c *gin.Context)
	QueryToolBox(c *gin.Context)
	DeleteToolBox(c *gin.Context)
	QueryToolBoxPage(c *gin.Context)
	QueryToolBoxNamesByIDs(c *gin.Context)
	UpdateToolBoxStatus(c *gin.Context)
	// Tool operation interface.
	CreateTool(c *gin.Context)
	UpdateTool(c *gin.Context)
	QueryTool(c *gin.Context)
	DeleteBoxTool(c *gin.Context)
	QueryBoxToolPage(c *gin.Context)
	UpdateToolStatus(c *gin.Context)
	GetMarketToolList(c *gin.Context)
	// Tool debugging.
	DebugTool(c *gin.Context)
	// tool execution.
	ExecuteTool(c *gin.Context)
	// Operators converted into tools.
	OperatorToTool(c *gin.Context)
	// OpenAPI capability package.
	RegisterOpenApiBundle(c *gin.Context)
	// Query toolbox information.
	GetReleaseToolBoxInfo(c *gin.Context)

	// Toolbox Market.
	QueryMarketToolBoxPage(c *gin.Context)
	QueryMarketToolBox(c *gin.Context)
}

var (
	once sync.Once
	h    ToolBoxHandler
)

type toolBoxHandler struct {
	Logger      interfaces.Logger
	ToolService interfaces.IToolService
	Validator   interfaces.Validator
}

// NewToolBoxHandler tool operation interface.
func NewToolBoxHandler() ToolBoxHandler {
	once.Do(func() {
		confLoader := config.NewConfigLoader()
		h = &toolBoxHandler{
			Logger:      confLoader.GetLogger(),
			ToolService: ltoolbox.NewToolServiceImpl(),
			Validator:   validator.NewValidator(),
		}
	})
	return h
}
