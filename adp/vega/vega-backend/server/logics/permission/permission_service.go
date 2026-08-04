package permission

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

var (
	pServiceOnce sync.Once
	pService     interfaces.PermissionService
)

func NewPermissionService(appSetting *common.AppSetting) interfaces.PermissionService {
	pServiceOnce.Do(func() {
		if !common.GetAuthEnabled() {
			pService = NewNoopPermissionService(appSetting)
		} else {
			pService = NewPermissionServiceImpl(appSetting)
		}
	})
	return pService
}
