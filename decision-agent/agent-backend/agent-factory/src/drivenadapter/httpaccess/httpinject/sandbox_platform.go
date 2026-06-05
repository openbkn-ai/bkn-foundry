package httpinject

import (
	"sync"

	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/sandboxplatformhttp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/global"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/ihttpaccess/isandboxhtpp"
)

var (
	sandboxPlatformOnce sync.Once
	sandboxPlatformImpl isandboxhtpp.ISandboxPlatform
)

func NewSandboxPlatformHttpAcc() isandboxhtpp.ISandboxPlatform {
	sandboxPlatformOnce.Do(func() {
		sandboxPlatformConf := global.GConfig.SandboxPlatformConf

		if global.GConfig.SwitchFields.Mock.MockSandboxPlatform {
			sandboxPlatformImpl = sandboxplatformhttp.NewMockSandboxPlatform(logger.GetLogger())
		} else {
			sandboxPlatformImpl = sandboxplatformhttp.NewSandboxPlatformHttpAcc(
				sandboxPlatformConf,
				rest.NewHTTPClient(),
				logger.GetLogger(),
			)
		}
	})

	return sandboxPlatformImpl
}
