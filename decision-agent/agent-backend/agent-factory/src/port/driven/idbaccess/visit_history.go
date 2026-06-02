package idbaccess

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

//go:generate mockgen -package idbaccessmock -destination ./idbaccessmock/visit_history.go github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/idbaccess IVisitHistoryRepo
type IVisitHistoryRepo interface {
	IncVisitCount(ctx context.Context, po *dapo.VisitHistoryPO) (err error)
}
