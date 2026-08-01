package iauthorizationscope

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type TrustedIdentity struct {
	TenantID               string
	BusinessDomain         string
	ActorID                string
	EffectiveSubjectID     string
	ApplicationPrincipalID string
	DelegationID           string
}

type Resolver interface {
	Resolve(context.Context, string, TrustedIdentity) (evidencevo.AccessProfile, error)
}
