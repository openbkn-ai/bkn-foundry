package iauthorizationscope

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

var (
	// ErrDenied means the authenticated identity is not allowed to resolve the scope.
	ErrDenied = errors.New("authorization scope denied")
	// ErrUnavailable means the authorization service could not make a decision.
	ErrUnavailable = errors.New("authorization scope unavailable")
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
