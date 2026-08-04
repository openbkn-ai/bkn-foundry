package auth

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

var (
	authServiceOnce sync.Once
	authService     interfaces.AuthService
)

func NewAuthService(appSetting *common.AppSetting) interfaces.AuthService {
	authServiceOnce.Do(func() {
		if !common.GetAuthEnabled() {
			authService = NewNoopAuthService(appSetting)
		} else {
			authService = NewHydraAuthService(appSetting)
		}
	})
	return authService
}
