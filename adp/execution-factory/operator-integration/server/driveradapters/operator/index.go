// Package operator operator operation adapter.
// @file operator.go
// @description: operator operation adapter.
package operator

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	lcategory "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/category"
	loperator "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/operator"
)

// OperatorHandler operator registration interface.
type OperatorHandler interface {
	// Operator management interface.
	OperatorRegister(c *gin.Context)
	OperatorQueryByOperatorID(c *gin.Context)
	OperatorQueryPage(c *gin.Context)
	OperatorQueryNamesByIDs(c *gin.Context)
	OperatorUpdateByOpenAPI(c *gin.Context)
	OperatorEdit(c *gin.Context)
	OperatorDelete(c *gin.Context)
	OperatorStatusUpdate(c *gin.Context)
	DebugOperator(c *gin.Context)
	ExecuteOperator(c *gin.Context)

	// History query operation.
	QueryOperatorHistoryDetail(c *gin.Context) // Published version details (query from history records)
	QueryOperatorHistoryList(c *gin.Context)   // Historical version list.

	// Operator market query operation.
	QueryOperatorMarketList(c *gin.Context)
	QueryOperatorMarketDetail(c *gin.Context)
}

var (
	once sync.Once
	h    OperatorHandler
)

type operatorHandle struct {
	Logger          interfaces.Logger
	OperatorManager interfaces.OperatorManager
	CategoryManager interfaces.CategoryManager
	Hydra           interfaces.Hydra
	Validator       interfaces.Validator
}

// NewOperatorHandler operator operation interface.
func NewOperatorHandler() OperatorHandler {
	once.Do(func() {
		confLoader := config.NewConfigLoader()
		h = &operatorHandle{
			Hydra:           drivenadapters.NewHydra(),
			Logger:          confLoader.GetLogger(),
			OperatorManager: loperator.NewOperatorManager(),
			CategoryManager: lcategory.NewCategoryManager(),
			Validator:       validator.NewValidator(),
		}
	})
	return h
}
