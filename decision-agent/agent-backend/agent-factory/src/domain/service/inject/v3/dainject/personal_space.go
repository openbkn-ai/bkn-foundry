package dainject

import (
	"sync"

	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/service"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/service/personalspacesvc"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/daconfdbacc"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/daconftpldbacc"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/personalspacedbacc"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/pubedagentdbacc"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/releaseacc"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/chttpinject"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/port/driver/iv3portdriver"
)

var (
	personalSpaceSvcOnce sync.Once
	personalSpaceSvcImpl iv3portdriver.IPersonalSpaceService
)

// NewPersonalSpaceSvc
func NewPersonalSpaceSvc() iv3portdriver.IPersonalSpaceService {
	personalSpaceSvcOnce.Do(func() {
		dto := &personalspacesvc.NewPersonalSpaceSvcDto{
			SvcBase:           service.NewSvcBase(),
			AgentTplRepo:      daconftpldbacc.NewDataAgentTplRepo(),
			AgentConfigRepo:   daconfdbacc.NewDataAgentRepo(),
			PersonalSpaceRepo: personalspacedbacc.NewPersonalSpaceRepo(),
			ReleaseRepo:       releaseacc.NewReleaseRepo(),
			PubedAgentRepo:    pubedagentdbacc.NewPubedAgentRepo(),
			UmHttp:            chttpinject.NewUmHttpAcc(),
			PmsSvc:            NewPermissionSvc(),
			BizDomainHttp:     chttpinject.NewBizDomainHttpAcc(),
		}

		personalSpaceSvcImpl = personalspacesvc.NewPersonalSpaceService(dto)
	})

	return personalSpaceSvcImpl
}
