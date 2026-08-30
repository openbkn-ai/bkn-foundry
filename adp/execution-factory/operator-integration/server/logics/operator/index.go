// Package operator implements the operator operation interface.
// @file index.go initialization.
// @description: Implement operator operation management.
package operator

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/mq"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/category"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/proxy"
)

type operatorManager struct {
	Logger             interfaces.Logger
	DBOperatorManager  model.IOperatorRegisterDB
	DBTx               model.DBTx
	CategoryManager    interfaces.CategoryManager
	UserMgnt           interfaces.UserManagement
	Validator          interfaces.Validator
	Proxy              interfaces.ProxyHandler
	OpReleaseDB        model.IOperatorReleaseDB
	OpReleaseHistoryDB model.IOperatorReleaseHistoryDB
	AuthService        interfaces.IAuthorizationService
	AuditLog           interfaces.LogModelOperator[*metric.AuditLogBuilderParams]
	MQClient           mq.MQClient
	MetadataService    interfaces.IMetadataService
}

var (
	once sync.Once
	om   interfaces.OperatorManager
)

// NewOperatorManager operator operation interface.
func NewOperatorManager() interfaces.OperatorManager {
	once.Do(func() {
		conf := config.NewConfigLoader()
		om = &operatorManager{
			Logger:             conf.GetLogger(),
			DBOperatorManager:  dbaccess.NewOperatorManagerDB(),
			DBTx:               dbaccess.NewBaseTx(),
			CategoryManager:    category.NewCategoryManager(),
			UserMgnt:           drivenadapters.NewUserManagementClient(),
			Validator:          validator.NewValidator(),
			Proxy:              proxy.NewProxyServer(),
			OpReleaseDB:        dbaccess.NewOperatorReleaseDB(),
			OpReleaseHistoryDB: dbaccess.NewOperatorReleaseHistoryDB(),
			AuthService:        auth.NewAuthServiceImpl(),
			AuditLog:           metric.NewAuditLogBuilder(),
			MQClient:           mq.NewMQClient(),
			MetadataService:    metadata.NewMetadataService(),
		}
	})
	return om
}
