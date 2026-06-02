package dainject

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service/agentinoutsvc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/bddbacc/bdagentdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/daconfdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/chttpinject"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driver/iv3portdriver"
	"github.com/kweaver-ai/kweaver-go-lib/logger"
)

var (
	agentInOutSvcOnce sync.Once
	agentInOutSvcImpl iv3portdriver.IAgentInOutSvc
)

// NewAgentInOutSvc 创建agent导入导出服务
func NewAgentInOutSvc() iv3portdriver.IAgentInOutSvc {
	agentInOutSvcOnce.Do(func() {
		dto := &agentinoutsvc.NewAgentInOutSvcDto{
			SvcBase:        service.NewSvcBase(),
			Logger:         logger.GetLogger(),
			AgentConfRepo:  daconfdbacc.NewDataAgentRepo(),
			PmsSvc:         NewPermissionSvc(),
			BizDomainHttp:  chttpinject.NewBizDomainHttpAcc(),
			BdAgentRelRepo: bdagentdbacc.NewBizDomainAgentRelRepo(),
		}

		agentInOutSvcImpl = agentinoutsvc.NewAgentInOutService(dto)
	})

	return agentInOutSvcImpl
}
