package idbaccess

import (
	"context"

	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/personalspacedbacc/psdbarg"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

//go:generate mockgen -package idbaccessmock -destination ./idbaccessmock/personal_space.go github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/port/driven/idbaccess IPersonalSpaceRepo
type IPersonalSpaceRepo interface {
	IDBAccBaseRepo

	ListPersonalSpaceAgent(ctx context.Context, arg *psdbarg.AgentListArg) (pos []*dapo.DataAgentPo, err error)

	ListPersonalSpaceTpl(ctx context.Context, arg *psdbarg.TplListArg) (pos []*dapo.DataAgentTplPo, err error)
}
