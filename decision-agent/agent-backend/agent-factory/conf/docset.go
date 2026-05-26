package conf

import (
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/cconf"
)

type DocsetConf struct {
	PublicSvc  cconf.SvcConf `yaml:"public_svc"`
	PrivateSvc cconf.SvcConf `yaml:"private_svc"`
}
