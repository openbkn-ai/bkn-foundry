package tplsvc

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdaenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdapmsenum"
)

func (s *dataAgentTplSvc) isHasPublishPermission(ctx context.Context) (has bool, err error) {
	has, err = s.pmsSvc.GetSingleMgmtPermission(ctx, cdaenum.ResourceTypeDataAgentTpl, cdapmsenum.AgentTplPublish)
	return
}

func (s *dataAgentTplSvc) isHasUnPublishPermission(ctx context.Context) (has bool, err error) {
	has, err = s.pmsSvc.GetSingleMgmtPermission(ctx, cdaenum.ResourceTypeDataAgentTpl, cdapmsenum.AgentTplUnpublish)
	return
}

func (s *dataAgentTplSvc) isHasUnpublishOtherUserAgentTplPermission(ctx context.Context) (has bool, err error) {
	has, err = s.pmsSvc.GetSingleMgmtPermission(ctx, cdaenum.ResourceTypeDataAgentTpl, cdapmsenum.AgentTplUnpublishOtherUserAgentTpl)
	return
}
