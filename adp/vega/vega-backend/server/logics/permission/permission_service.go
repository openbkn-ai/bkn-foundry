package permission

import (
	"sync"

	"vega-backend/common"
	"vega-backend/interfaces"
)

var (
	pServiceOnce sync.Once
	pService     interfaces.PermissionService
)

func NewPermissionService(appSetting *common.AppSetting) interfaces.PermissionService {
	pServiceOnce.Do(func() {
		pService = NewPermissionServiceImpl(appSetting)
	})
	return pService
}
