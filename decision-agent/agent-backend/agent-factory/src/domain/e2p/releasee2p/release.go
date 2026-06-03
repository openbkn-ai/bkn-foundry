package releasee2p

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/entity/releaseeo"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

func ReleaseE2P(entity *releaseeo.ReleaseEO) *dapo.ReleasePO {
	po := &dapo.ReleasePO{
		ID:           entity.ID,
		AgentID:      entity.AgentID,
		AgentConfig:  entity.AgentConfig,
		AgentVersion: entity.AgentVersion,
		AgentDesc:    entity.AgentDesc,
	}

	if len(entity.PublishToBes) > 0 {
		po.SetPublishToBes(entity.PublishToBes)
	}

	return po
}
