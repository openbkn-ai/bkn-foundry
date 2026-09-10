// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"fmt"
)

// EvaluationScope selects how far authorization evaluation may look. Local is
// intentionally not a final authorization answer: it is exposed only through
// the authenticated Vega path because Vega owns a parent relation bkn-safe does
// not know. A caller must never authorize a business operation from the local
// result alone.
type EvaluationScope string

const (
	ScopeEffective EvaluationScope = "effective"
	ScopeLocal     EvaluationScope = "local"
)

// ParseEvaluationScope applies the public API default and rejects unknown
// values instead of silently turning a misspelling into a different policy.
func ParseEvaluationScope(value string) (EvaluationScope, error) {
	switch EvaluationScope(value) {
	case "", ScopeEffective:
		return ScopeEffective, nil
	case ScopeLocal:
		return ScopeLocal, nil
	default:
		return "", fmt.Errorf("unsupported evaluation scope %q", value)
	}
}

// Decision is the policy outcome. None is only valid for a local evaluation;
// an effective evaluation closes it to deny/default.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	DecisionNone  Decision = "none"
)

// DecisionBasis explains which layer supplied a decision. It deliberately
// does not expose policy-source or authority-source: neither has runtime
// precedence beyond the rules represented here.
type DecisionBasis string

const (
	BasisDirect    DecisionBasis = "direct"
	BasisInherited DecisionBasis = "inherited"
	BasisBundle    DecisionBasis = "bundle"
	BasisWildcard  DecisionBasis = "wildcard"
	BasisDefault   DecisionBasis = "default"
	BasisRequires  DecisionBasis = "requires"
	BasisNone      DecisionBasis = "none"
)

// Evaluation is one structured authorization result.
type Evaluation struct {
	Scope             EvaluationScope
	Decision          Decision
	Basis             DecisionBasis
	DeniedRequirement string
	RequirementBasis  DecisionBasis
}

func (d Evaluation) Allowed() bool { return d.Decision == DecisionAllow }

// OperationDecision is the resource-filter projection for one operation.
type OperationDecision struct {
	Operation         string
	Decision          Decision
	Basis             DecisionBasis
	DeniedRequirement string
	RequirementBasis  DecisionBasis
}

func noneEvaluation(scope EvaluationScope) Evaluation {
	return Evaluation{Scope: scope, Decision: DecisionNone, Basis: BasisNone}
}

func defaultDeny() Evaluation {
	return Evaluation{Scope: ScopeEffective, Decision: DecisionDeny, Basis: BasisDefault}
}

// resolveEffective applies the one documented local/parent/wildcard order to a
// local result and an optional inherited result. Keeping this final merge in a
// helper lets dry-run hierarchy previews evaluate a proposed parent with the
// same semantics as the persisted hierarchy path.
func resolveEffective(local Evaluation, inherited Evaluation, hasInherited bool) Evaluation {
	if local.Decision != "" && local.Decision != DecisionNone && !(local.Decision == DecisionAllow && local.Basis == BasisWildcard) {
		local.Scope = ScopeEffective
		return local
	}
	if hasInherited {
		inherited.Scope = ScopeEffective
		inherited.Basis = BasisInherited
		return inherited
	}
	if local.Decision == DecisionAllow && local.Basis == BasisWildcard {
		local.Scope = ScopeEffective
		return local
	}
	return defaultDeny()
}

// LocalDecision returns only the rules attached to the requested resource. It
// deliberately skips parent traversal, default deny and operation requires.
// Only the authenticated Vega orchestration endpoint may expose this result.
func (en *Enforcer) LocalDecision(ctx context.Context, accessorID, resourceType, resourceID, op string) (Evaluation, error) {
	idx, err := en.grantIndex(accessorID)
	if err != nil {
		return Evaluation{}, err
	}
	resource := ResourceRef{Type: resourceType, ID: resourceID}
	all, err := en.localDecisionsWithIndex(ctx, accessorID, idx,
		map[ResourceRef][]string{resource: {op}})
	if err != nil {
		return Evaluation{}, err
	}
	return en.applyManagedProxyProvenance(ctx, accessorID, resource, op, all[resource][op])
}

// OperationDecision is the only final authorization entry point. #1429 adds
// direct operation-requires evaluation here; callers must not substitute the
// base-effective layer or they would bypass those prerequisites.
func (en *Enforcer) OperationDecision(ctx context.Context, accessorID, resourceType, resourceID, op string) (Evaluation, error) {
	idx, err := en.grantIndex(accessorID)
	if err != nil {
		return Evaluation{}, err
	}
	resource := ResourceRef{Type: resourceType, ID: resourceID}
	all, err := en.operationDecisionsWithIndex(ctx, accessorID, idx,
		map[ResourceRef][]string{resource: {op}})
	if err != nil {
		return Evaluation{}, err
	}
	return en.applyManagedProxyProvenance(ctx, accessorID, resource, op, all[resource][op])
}

func (en *Enforcer) applyManagedProxyProvenance(ctx context.Context, accessorID string,
	resource ResourceRef, op string, decision Evaluation) (Evaluation, error) {
	if !decision.Allowed() || en.db == nil {
		return decision, nil
	}
	managed, err := en.isManagedProxyContext(ctx, accessorID)
	if err != nil {
		return Evaluation{}, err
	}
	if !managed {
		return decision, nil
	}
	current, err := en.hasCurrentProxySource(ctx, accessorID, resource.Type, resource.ID, op)
	if err != nil {
		return Evaluation{}, err
	}
	if current {
		return decision, nil
	}
	// Managed proxy access is source-backed and exact. An obsolete Casbin allow
	// therefore becomes an explicit local denial rather than a default miss.
	return Evaluation{Scope: decision.Scope, Decision: DecisionDeny, Basis: BasisDirect}, nil
}
