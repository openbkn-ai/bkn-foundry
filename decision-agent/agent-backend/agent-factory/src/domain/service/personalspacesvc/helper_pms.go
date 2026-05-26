package personalspacesvc

import (
	"context"

	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/enum/cdaenum"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/enum/cdapmsenum"
)

func (s *PersonalSpaceService) isHasBuiltInAgentMgmtPermission(ctx context.Context) (has bool, err error) {
	has, err = s.pmsSvc.GetSingleMgmtPermission(ctx, cdaenum.ResourceTypeDataAgent, cdapmsenum.AgentBuiltInAgentMgmt)
	return
}
