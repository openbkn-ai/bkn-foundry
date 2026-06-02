package conf

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/cconf"
)

type AgentFactoryConf struct {
	PublicSvc  cconf.SvcConf `yaml:"public_svc"`
	PrivateSvc cconf.SvcConf `yaml:"private_svc"`
}
