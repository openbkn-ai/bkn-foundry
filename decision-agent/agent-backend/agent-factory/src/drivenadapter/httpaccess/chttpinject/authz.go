package chttpinject

import (
	"sync"

	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/common/global"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/port/driven/ihttpaccess/iauthzacc"
	"github.com/kweaver-ai/kweaver-go-lib/logger"
)

var (
	authZOnce sync.Once
	authZImpl iauthzacc.AuthZHttpAcc
)

func NewAuthZHttpAcc() iauthzacc.AuthZHttpAcc {
	authZOnce.Do(func() {
		if global.GConfig.SwitchFields.Mock.MockAuthZ {
			authZImpl = authzhttp.NewMockAuthZHttpAcc(
				logger.GetLogger(),
			)
		} else {
			authZImpl = authzhttp.NewAuthZHttpAcc(
				logger.GetLogger(),
			)
		}
	})

	return authZImpl
}
