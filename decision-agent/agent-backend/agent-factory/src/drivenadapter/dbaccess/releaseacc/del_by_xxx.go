package releaseacc

import (
	"context"
	"database/sql"

	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/common/chelper/dbhelper2"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

// DeleteByAgentId implements release.ReleaseRepo.
func (repo *releaseRepo) DeleteByAgentID(ctx context.Context, tx *sql.Tx, agentID string) (err error) {
	sr := dbhelper2.NewSQLRunner(repo.db, repo.logger)
	if tx != nil {
		sr = dbhelper2.TxSr(tx, repo.logger)
	}

	po := &dapo.ReleasePO{}
	sr.FromPo(po)
	_, err = sr.WhereEqual("f_agent_id", agentID).Delete()

	return
}
