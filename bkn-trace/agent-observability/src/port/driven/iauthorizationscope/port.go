package iauthorizationscope

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

// TrustedIdentity is established by the query gateway or the OAuth handler.
// DelegationID is currently an audit and fingerprint dimension; Resolver does not
// independently validate delegation until BKN Safe exposes a verifiable contract.
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
