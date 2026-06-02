package publishedtpldbacc

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/global"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/idbaccess"
	"github.com/kweaver-ai/kweaver-go-lib/logger"

	"github.com/kweaver-ai/proton-rds-sdk-go/sqlx"
)

var (
	agentTplRepoOnce sync.Once
	agentTplRepoImpl idbaccess.IPublishedTplRepo
)

type PubedTplRepo struct {
	idbaccess.IDBAccBaseRepo

	db *sqlx.DB

	logger icmp.Logger
}

var _ idbaccess.IPublishedTplRepo = &PubedTplRepo{}

func NewPublishedTplRepo() idbaccess.IPublishedTplRepo {
	agentTplRepoOnce.Do(func() {
		agentTplRepoImpl = &PubedTplRepo{
			db:             global.GDB,
			logger:         logger.GetLogger(),
			IDBAccBaseRepo: dbaccess.NewDBAccBase(),
		}
	})

	return agentTplRepoImpl
}
