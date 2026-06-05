package releaseacc

import (
	"sync"

	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/global"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/idbaccess"

	"github.com/kweaver-ai/proton-rds-sdk-go/sqlx"
)

var (
	releaseHistoryRepoOnce sync.Once
	releaseHistoryRepoImpl idbaccess.IReleaseHistoryRepo
)

type releaseHistoryRepo struct {
	idbaccess.IDBAccBaseRepo

	db     *sqlx.DB
	logger icmp.Logger
}

var _ idbaccess.IReleaseHistoryRepo = &releaseHistoryRepo{}

func NewReleaseHistoryRepo() idbaccess.IReleaseHistoryRepo {
	releaseHistoryRepoOnce.Do(func() {
		releaseHistoryRepoImpl = &releaseHistoryRepo{
			db:             global.GDB,
			logger:         logger.GetLogger(),
			IDBAccBaseRepo: dbaccess.NewDBAccBase(),
		}
	})

	return releaseHistoryRepoImpl
}
