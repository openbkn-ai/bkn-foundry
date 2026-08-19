package category

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/cache"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
)

// categoryManager category manager.
type categoryManager struct {
	logger      interfaces.Logger
	DBTx        model.DBTx
	DBCategory  model.DBCategory
	Validator   interfaces.Validator
	Cache       interfaces.Cache
	AuthService interfaces.IAuthorizationService
}

// NewCategoryManager creates a category manager.
func NewCategoryManager() interfaces.CategoryManager {
	c := &categoryManager{
		logger:      config.NewConfigLoader().GetLogger(),
		DBTx:        dbaccess.NewBaseTx(),
		DBCategory:  dbaccess.NewCategoryDBSingleton(),
		Validator:   validator.NewValidator(),
		Cache:       cache.NewInMemoryCache(),
		AuthService: auth.NewAuthServiceImpl(),
	}
	// Load classification information from the database into the cache.
	categoryDBList, err := c.DBCategory.SelectList(context.Background(), nil)
	if err != nil {
		c.logger.Errorf("load category from db failed, err: %v", err)
		return nil
	}
	for _, categoryDB := range categoryDBList {
		c.Cache.Set(categoryDB.CategoryID, categoryDB.CategoryName)
	}
	return c
}
