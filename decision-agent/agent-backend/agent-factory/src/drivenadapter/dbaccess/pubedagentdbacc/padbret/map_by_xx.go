package padbret

import "github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"

type GetPaPoMapByXxRet struct {
	JoinPosID2PoMap  map[string]*dapo.PublishedJoinPo
	JoinPosKey2PoMap map[string]*dapo.PublishedJoinPo
}

func NewGetPaPoMapByXxRet() *GetPaPoMapByXxRet {
	return &GetPaPoMapByXxRet{
		JoinPosID2PoMap:  make(map[string]*dapo.PublishedJoinPo),
		JoinPosKey2PoMap: make(map[string]*dapo.PublishedJoinPo),
	}
}
