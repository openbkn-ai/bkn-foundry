package dbaccess

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/db"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

type baseTx struct {
	dbPool *sqlx.DB
}

func NewBaseTx() model.DBTx {
	return &baseTx{
		dbPool: db.NewDBPool(),
	}
}

func (b *baseTx) GetTx(ctx context.Context) (*sql.Tx, error) {
	return b.dbPool.BeginTx(ctx, nil)
}
