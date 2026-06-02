package agentinoutsvc

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdaenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdapmsenum"
)

func (s *agentInOutSvc) isHasSystemAgentCreatePermission(ctx context.Context) (has bool, err error) {
	has, err = s.pmsSvc.GetSingleMgmtPermission(ctx, cdaenum.ResourceTypeDataAgent, cdapmsenum.AgentCreateSystemAgent)
	return
}
