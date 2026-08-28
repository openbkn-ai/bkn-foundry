package driveradapters

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/category"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/operator"
)

// OperatorRestHandler operator RESTful API Handler interface.
type OperatorRestHandler interface {
	// RegisterPrivate register internal API.
	RegisterPrivate(engine *gin.RouterGroup)

	// RegisterPublic Register external API.
	RegisterPublic(engine *gin.RouterGroup)
}

type operatorRestHandler struct {
	OperatorHandler operator.OperatorHandler
	CategoryHandler category.CategoryHandler
}

var (
	oOnce    sync.Once
	oHandler OperatorRestHandler
)

func NewOperatorRestHandler() OperatorRestHandler {
	oOnce.Do(func() {
		oHandler = &operatorRestHandler{
			OperatorHandler: operator.NewOperatorHandler(),
			CategoryHandler: category.NewCategoryHandler(),
		}
	})
	return oHandler
}

// RegisterPrivate register internal API.
func (o *operatorRestHandler) RegisterPrivate(engine *gin.RouterGroup) {
	// Operator management related interfaces.
	// POST /api/agent-operator-integration/internal-v1/operator/register Register operator.
	engine.POST("/operator/register", o.OperatorHandler.OperatorRegister)
	// GET /api/agent-operator-integration/internal-v1/operator/info/{operator_id} Get operator details.
	engine.GET("/operator/info/:operator_id", o.OperatorHandler.OperatorQueryByOperatorID)
	// GET /api/agent-operator-integration/internal-v1/operator/info/list Get the operator paging list.
	engine.GET("/operator/info/list", o.OperatorHandler.OperatorQueryPage)
	// POST /api/agent-operator-integration/internal-v1/operator/info/update Update operator information (currently only called by Dataflow)
	engine.POST("/operator/info/update", o.OperatorHandler.OperatorUpdateByOpenAPI)
	// POST /api/agent-operator-integration/internal-v1/operator/proxy/:operator_id execute operator.
	engine.POST("/operator/proxy/:operator_id", o.OperatorHandler.ExecuteOperator)

	// Published version details.
	// GET /api/agent-operator-integration/internal-v1/operator/history/:operator_id/:version Get the details of the specified version of the published operator.
	engine.GET("/operator/history/:operator_id/:version", o.OperatorHandler.QueryOperatorHistoryDetail)
	// GET /api/agent-operator-integration/internal-v1/operator/history/:operator_id
	engine.GET("/operator/history/:operator_id", o.OperatorHandler.QueryOperatorHistoryList)

	// Operator market related interfaces.
	// GET /api/agent-operator-integration/internal-v1/operator/market Get the operator market list, support paging, sorting, and query filter conditions.
	engine.GET("/operator/market", o.OperatorHandler.QueryOperatorMarketList)
	// GET /api/agent-operator-integration/internal-v1/operator/market/:operator_id View details in the operator market.
	engine.GET("/operator/market/:operator_id", o.OperatorHandler.QueryOperatorMarketDetail)

	// Operator classification management.
	// GET /api/agent-operator-integration/internal-v1/operator/category //Get the operator classification list.
	engine.GET("/operator/category", o.CategoryHandler.CategoryList)
	// POST /api/agent-operator-integration/internal-v1/operator/category // Create a new operator category.
	engine.POST("/operator/category", o.CategoryHandler.CategoryCreate)
	// PUT /api/agent-operator-integration/internal-v1/operator/category/:category_type // Update operator classification.
	engine.PUT("/operator/category/:category_type", o.CategoryHandler.CategoryUpdate)
	// DELETE /api/agent-operator-integration/internal-v1/operator/category/:category_type // Delete operator category.
	engine.DELETE("/operator/category/:category_type", o.CategoryHandler.CategoryDelete)
}

// RegisterPublic Register external API.
func (o *operatorRestHandler) RegisterPublic(engine *gin.RouterGroup) {
	// POST /api/agent-operator-integration/v1/operator/register
	engine.POST("/operator/register", o.OperatorHandler.OperatorRegister)
	// Query operator related interfaces.
	// GET /api/agent-operator-integration/v1/operator/info/{operator_id}
	engine.GET("/operator/info/:operator_id", o.OperatorHandler.OperatorQueryByOperatorID)
	// GET /api/agent-operator-integration/v1/operator/info/list
	engine.GET("/operator/info/list", o.OperatorHandler.OperatorQueryPage)
	// POST /api/agent-operator-integration/v1/operator/names Batch names based on operator ID (front-end object-level authorization page echo)
	engine.POST("/operator/names", o.OperatorHandler.OperatorQueryNamesByIDs)
	// DELETE /api/agent-operator-integration/v1/operator/delete
	engine.DELETE("/operator/delete", o.OperatorHandler.OperatorDelete)
	// POST /api/agent-operator-integration/v1/operator/status
	engine.POST("/operator/status", o.OperatorHandler.OperatorStatusUpdate)
	// POST /api/agent-operator-integration/v1/operator/info
	engine.POST("/operator/info", o.OperatorHandler.OperatorEdit)
	// POST /api/agent-operator-integration/v1/operator/info/update
	engine.POST("/operator/info/update", o.OperatorHandler.OperatorUpdateByOpenAPI)
	// POST /api/agent-operator-integration/v1/operator/debug
	engine.POST("/operator/debug", o.OperatorHandler.DebugOperator)

	// Published version details.
	// GET /api/agent-operator-integration/v1/operator/history/:operator_id/:version Get the details of the specified version of the published operator.
	engine.GET("/operator/history/:operator_id/:version", o.OperatorHandler.QueryOperatorHistoryDetail)
	// GET /api/agent-operator-integration/v1/operator/history/:operator_id
	engine.GET("/operator/history/:operator_id", o.OperatorHandler.QueryOperatorHistoryList)

	// Operator market related interfaces.
	// GET /api/agent-operator-integration/v1/operator/market Get the operator market list, supports paging, sorting, and query filter conditions.
	engine.GET("/operator/market", o.OperatorHandler.QueryOperatorMarketList)
	// GET /api/agent-operator-integration/v1/operator/market/:operator_id View details in the operator market.
	engine.GET("/operator/market/:operator_id", o.OperatorHandler.QueryOperatorMarketDetail)

	// Operator classification management.
	// GET /api/agent-operator-integration/v1/operator/category //Get the operator classification list.
	engine.GET("/operator/category", o.CategoryHandler.CategoryList)
}
