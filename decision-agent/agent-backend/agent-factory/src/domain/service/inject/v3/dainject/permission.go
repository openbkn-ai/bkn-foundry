package dainject

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service/permissionsvc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/daconfdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/releaseacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/chttpinject"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driver/iv3portdriver"
)

var (
	permissionSvcOnce sync.Once
	permissionSvcImpl iv3portdriver.IPermissionSvc
)

// NewPermissionSvc
func NewPermissionSvc() iv3portdriver.IPermissionSvc {
	permissionSvcOnce.Do(func() {
		dto := &permissionsvc.NewPermissionSvcDto{
			SvcBase:               service.NewSvcBase(),
			ReleaseRepo:           releaseacc.NewReleaseRepo(),
			ReleasePermissionRepo: releaseacc.NewReleasePermissionRepo(),
			AgentConfigRepo:       daconfdbacc.NewDataAgentRepo(),
			UmHttp:                chttpinject.NewUmHttpAcc(),
			AuthZHttp:             chttpinject.NewAuthZHttpAcc(),
		}

		permissionSvcImpl = permissionsvc.NewPermissionService(dto)
	})

	return permissionSvcImpl
}
