package daconftpldbacc

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/chelper/dbhelper2"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

func (repo *DAConfigTplRepo) Create(ctx context.Context, tx *sql.Tx, po *dapo.DataAgentTplPo) (err error) {
	sr := dbhelper2.NewSQLRunner(repo.db, repo.logger)
	if tx != nil {
		sr = dbhelper2.TxSr(tx, repo.logger)
	}

	sr.FromPo(po)
	_, err = sr.InsertStruct(po)

	return
}
