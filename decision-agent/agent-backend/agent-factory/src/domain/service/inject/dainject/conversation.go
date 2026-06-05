package dainject

import (
	"sync"

	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service/conversationsvc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/conversationdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/conversationmsgdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/httpinject"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/global"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driver/iportdriver"
)

var (
	conversationSvcOnce sync.Once
	conversationSvcImpl iportdriver.IConversationSvc
)

func NewConversationSvc() iportdriver.IConversationSvc {
	conversationSvcOnce.Do(func() {
		dto := &conversationsvc.NewConversationSvcDto{
			SvcBase:             service.NewSvcBase(),
			ConversationRepo:    conversationdbacc.NewConversationRepo(),
			ConversationMsgRepo: conversationmsgdbacc.NewConversationMsgRepo(),
			Logger:              logger.GetLogger(),
			AgentExecutorV1:     httpinject.NewAgentExecutorV1HttpAcc(),
			AgentExecutorV2:     httpinject.NewAgentExecutorV2HttpAcc(),
			SandboxPlatform:     httpinject.NewSandboxPlatformHttpAcc(),
			SandboxPlatformConf: global.GConfig.SandboxPlatformConf,
		}
		conversationSvcImpl = conversationsvc.NewConversationService(dto)
	})

	return conversationSvcImpl
}
