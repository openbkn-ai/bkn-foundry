package dolphintpleo

import "github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/valueobject/daconfvalobj"

type IDolphinTpl interface {
	LoadFromConfig(config *daconfvalobj.Config)
	ToString() (str string)
}
