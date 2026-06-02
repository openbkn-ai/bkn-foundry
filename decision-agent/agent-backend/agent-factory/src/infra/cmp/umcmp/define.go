package umcmp

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/cconf"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	// "github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/config"
)

type Um struct {
	umConf *cconf.UserMgntCfg

	logger icmp.Logger
}

func NewUmCmp(umConf *cconf.UserMgntCfg,
	logger icmp.Logger,
) *Um {
	return &Um{
		umConf: umConf,
		logger: logger,
	}
}
