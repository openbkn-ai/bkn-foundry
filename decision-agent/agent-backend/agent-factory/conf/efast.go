package conf

import "github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/cconf"

type EfastConf struct {
	PublicSvc  cconf.SvcConf `yaml:"public_svc"`
	PrivateSvc cconf.SvcConf `yaml:"private_svc"`
}
