package idbaccess

import (
	"context"
	"database/sql"

	"github.com/kweaver-ai/proton-rds-sdk-go/sqlx"
)

//go:generate mockgen -package idbaccessmock -destination ./idbaccessmock/base.go github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/idbaccess IDBAccBaseRepo
type IDBAccBaseRepo interface {
	BeginTx(ctx context.Context) (*sql.Tx, error)

	GetDB() *sqlx.DB
}
