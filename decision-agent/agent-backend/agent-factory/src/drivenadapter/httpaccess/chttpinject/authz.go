package chttpinject

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/global"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/ihttpaccess/iauthzacc"
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
		// Authz cutover (revertible): AUTHZ_PROVIDER=shadow wraps the ISF adapter
		// so OperationCheck also queries bkn-safe and logs diffs — ISF stays
		// authoritative. Unset the env to revert. See authzhttp/authz_shadow.go.
		authZImpl = authzhttp.MaybeShadow(authZImpl, logger.GetLogger())
	})

	return authZImpl
}
