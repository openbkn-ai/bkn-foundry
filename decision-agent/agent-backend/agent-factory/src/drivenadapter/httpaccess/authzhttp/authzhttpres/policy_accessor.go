package authzhttpres

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cenum"
)

// PolicyAccessor 策略访问者
type PolicyAccessor struct {
	ID   string                 `json:"id"`
	Type cenum.PmsTargetObjType `json:"type"`
	Name string                 `json:"name"`
	// ParentDeps [][]*PolicyAccessorDep   `json:"parent_deps"` 目前不需要
}
