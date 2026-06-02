package dolphintpleo

import "github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/valueobject/daconfvalobj"

type IDolphinTpl interface {
	LoadFromConfig(config *daconfvalobj.Config)
	ToString() (str string)
}
