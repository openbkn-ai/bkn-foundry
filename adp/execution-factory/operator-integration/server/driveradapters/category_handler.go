package driveradapters

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/category"
)

// CategoryRestHandler category RESTful API handler. Categories are shared by toolboxes, MCP
// servers and skills.
type CategoryRestHandler interface {
	// RegisterPrivate register internal API.
	RegisterPrivate(engine *gin.RouterGroup)

	// RegisterPublic Register external API.
	RegisterPublic(engine *gin.RouterGroup)
}

type categoryRestHandler struct {
	CategoryHandler category.CategoryHandler
}

var (
	cOnce    sync.Once
	cHandler CategoryRestHandler
)

// NewCategoryRestHandler creates the category route handler.
func NewCategoryRestHandler() CategoryRestHandler {
	cOnce.Do(func() {
		cHandler = &categoryRestHandler{
			CategoryHandler: category.NewCategoryHandler(),
		}
	})
	return cHandler
}

// RegisterPrivate register internal API.
func (r *categoryRestHandler) RegisterPrivate(engine *gin.RouterGroup) {
	engine.GET("/category", r.CategoryHandler.CategoryList)
	engine.POST("/category", r.CategoryHandler.CategoryCreate)
	engine.PUT("/category/:category_type", r.CategoryHandler.CategoryUpdate)
	engine.DELETE("/category/:category_type", r.CategoryHandler.CategoryDelete)
}

// RegisterPublic Register external API.
func (r *categoryRestHandler) RegisterPublic(engine *gin.RouterGroup) {
	engine.GET("/category", r.CategoryHandler.CategoryList)
}
