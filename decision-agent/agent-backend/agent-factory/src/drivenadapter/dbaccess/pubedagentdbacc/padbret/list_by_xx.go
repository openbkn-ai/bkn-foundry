package padbret

import "github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"

type GetPaPoListByXxRet struct {
	JoinPos []*dapo.PublishedJoinPo
}

func NewGetPaPoListByXxRet() *GetPaPoListByXxRet {
	return &GetPaPoListByXxRet{
		JoinPos: make([]*dapo.PublishedJoinPo, 0),
	}
}
