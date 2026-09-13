package auth

import (
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/mq"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type authServiceImpl struct {
	logger         interfaces.Logger
	authorization  interfaces.Authorization
	mqClient       mq.MQClient
	userManagement interfaces.UserManagement
}

func NewAuthServiceImpl() interfaces.IAuthorizationService {
	return &authServiceImpl{
		logger:         config.NewConfigLoader().GetLogger(),
		authorization:  drivenadapters.NewAuthorization(),
		mqClient:       mq.NewMQClient(),
		userManagement: drivenadapters.NewUserManagementClient(),
	}
}
