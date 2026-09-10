// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"fmt"

	"github.com/casbin/casbin/v2/util"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
)

// ResourceRef names one concrete resource instance ("type:id").
type ResourceRef struct {
	Type string
	ID   string
}

// FilteredResource is one visible resource plus the subset of the candidate
// operations the accessor holds on it.
type FilteredResource struct {
	Type       string
	ID         string
	Operations []string
	Decisions  []OperationDecision
}

// FilterResourceOps answers, for a batch of resource instances at once: which
// of them the accessor may see, and which of the candidate operations it holds
// on each.
//
// Semantics, deliberately two separate axes (this is the whole point of the
// batch contract — callers previously had to conflate them):
//
//   - visibility: a resource is returned only when the accessor holds EVERY
//     operation in visibility. An empty visibility list imposes no constraint,
//     so every requested resource is returned (the "give me each resource's
//     operation set" query).
//   - projection: the returned Operations are the subset of candidates the
//     accessor holds. Independent of visibility — a resource may be visible via
//     view_detail and still come back with modify and delete attached.
//
// Decisions are identical to Check: same grant sources (direct, role-derived
// including transitive roles, public/root-department, super-admin wildcard),
// same object and act wildcard rules. It does not call Check per (resource, op)
// because that is O(resources x ops x policies) inside Casbin's matcher — the
// list pages this endpoint exists for would time out exactly as they did before
// (#357). Instead the accessor's grants are resolved once and projected onto
// each resource; TestFilterResourceOpsMatchesCheck pins the two paths together.
func (en *Enforcer) FilterResourceOps(accessorID string, resources []ResourceRef, visibility, candidates []string) ([]FilteredResource, error) {
	return en.filterResourceOps(context.Background(), accessorID, resources, visibility, candidates, ScopeEffective, true)
}

// FilterResourceOpsScoped is the structured variant used by the HTTP API.
// Effective keeps the historical filtering/projection response. Local returns
// every requested resource and one decision for every requested operation, so
// a caller can distinguish an explicit deny from absence before doing its own
// trusted parent fallback.
func (en *Enforcer) FilterResourceOpsScoped(ctx context.Context, accessorID string, resources []ResourceRef,
	visibility, candidates []string, scope EvaluationScope) ([]FilteredResource, error) {
	return en.filterResourceOps(ctx, accessorID, resources, visibility, candidates, scope, true)
}

// filterResourceOps performs the batched Casbin and hierarchy projection. The
// provenance flag is disabled only while validating the human delegators that
// back a managed proxy source; those checks must never recurse through proxy
// provenance.
func (en *Enforcer) filterResourceOps(ctx context.Context, accessorID string, resources []ResourceRef,
	visibility, candidates []string, scope EvaluationScope, validateProvenance bool) ([]FilteredResource, error) {
	if scope != ScopeEffective && scope != ScopeLocal {
		return nil, fmt.Errorf("unsupported evaluation scope %q", scope)
	}
	idx, err := en.grantIndex(accessorID)
	if err != nil {
		return nil, err
	}

	// One decision pass per distinct resource, over the union of both op sets.
	union := make([]string, 0, len(visibility)+len(candidates))
	seenOp := map[string]bool{}
	for _, op := range append(append([]string{}, visibility...), candidates...) {
		if op == "" || seenOp[op] {
			continue
		}
		seenOp[op] = true
		union = append(union, op)
	}

	want := make(map[ResourceRef][]string, len(resources))
	for _, r := range resources {
		if _, done := want[r]; !done {
			want[r] = union
		}
	}
	var requirements map[string]map[string][]string
	if scope == ScopeLocal {
		requirements, err = en.requirementsFor(ctx, want)
		if err != nil {
			return nil, err
		}
		want = expandWithRequirements(want, requirements)
	}
	var decided map[ResourceRef]map[string]Evaluation
	if scope == ScopeLocal {
		decided, err = en.localDecisionsWithIndex(ctx, accessorID, idx, want)
	} else {
		decided, err = en.operationDecisionsWithIndex(ctx, accessorID, idx, want, validateProvenance)
	}
	if err != nil {
		return nil, err
	}

	// Managed proxies have a second, provenance-aware condition that is not
	// represented in Casbin: an exact active source must still be valid. Apply
	// it after the batched raw-policy calculation so list/filter decisions stay
	// identical to Check. Human and ordinary app accessors keep the optimized
	// path above without per-decision source lookups.
	if scope == ScopeLocal && validateProvenance && en.db != nil {
		managed, err := en.isManagedProxyContext(ctx, accessorID)
		if err != nil {
			return nil, err
		}
		if managed {
			current, err := en.currentProxyPermissions(ctx, accessorID)
			if err != nil {
				return nil, err
			}
			for resource, decisions := range decided {
				for operation, decision := range decisions {
					if !decision.Allowed() {
						continue
					}
					if current[proxyPermission{
						ResourceType: resource.Type,
						ResourceID:   resource.ID,
						Operation:    operation,
					}] {
						continue
					}
					decisions[operation] = Evaluation{
						Scope: scope, Decision: DecisionDeny, Basis: BasisDirect,
					}
				}
			}
		}
	}

	out := make([]FilteredResource, 0, len(resources))
	for _, r := range resources {
		resourceDecisions := decided[r]
		if scope == ScopeLocal {
			item := FilteredResource{Type: r.Type, ID: r.ID, Operations: make([]string, 0, len(candidates))}
			for _, op := range candidates {
				if resourceDecisions[op].Allowed() {
					item.Operations = append(item.Operations, op)
				}
			}
			for _, op := range want[r] {
				d := resourceDecisions[op]
				d.Requirements = append([]string(nil), requirements[r.Type][op]...)
				item.Decisions = append(item.Decisions, OperationDecision{
					Operation: op, Decision: d.Decision, Basis: d.Basis,
					Requirements:      d.Requirements,
					DeniedRequirement: d.DeniedRequirement, RequirementBasis: d.RequirementBasis,
				})
			}
			out = append(out, item)
			continue
		}
		visible := true
		for _, op := range visibility {
			if !resourceDecisions[op].Allowed() {
				visible = false
				break
			}
		}
		if !visible {
			continue
		}
		ops := make([]string, 0, len(candidates))
		for _, op := range candidates {
			if resourceDecisions[op].Allowed() {
				ops = append(ops, op)
			}
		}
		structured := make([]OperationDecision, 0, len(union))
		for _, op := range union {
			d := resourceDecisions[op]
			structured = append(structured, OperationDecision{
				Operation: op, Decision: d.Decision, Basis: d.Basis,
				Requirements:      d.Requirements,
				DeniedRequirement: d.DeniedRequirement, RequirementBasis: d.RequirementBasis,
			})
		}
		out = append(out, FilteredResource{Type: r.Type, ID: r.ID, Operations: ops, Decisions: structured})
	}
	return out, nil
}

// grantRow is one policy line the accessor can invoke: the object pattern and
// the operation it grants (act "*" grants every operation).
type grantRow struct {
	object string
	act    string
	effect string
	source PolicySource
}

// grantIndex is an accessor's effective grant set, split by whether the object
// pattern needs wildcard matching. Exact patterns resolve by map lookup; only
// the (few) wildcard patterns are walked per resource, which keeps a list page
// linear in resources rather than resources x policies.
type grantIndex struct {
	exact      map[string][]grantRow // object key -> rules
	wildcard   []grantRow
	subjects   []string
	superAdmin bool
}

// grantIndex collects every policy row that can satisfy the matcher's subject
// clause for this accessor:
//
//	(g(r.sub, p.sub) || p.sub == PublicAccessorID)
//
// GetImplicitPermissionsForUser covers the g() half (the accessor itself plus
// every role reachable through g, transitively); the public/root-department
// grants are the other half and are NOT part of implicit permissions, so they
// are read separately. Missing them here would silently deny access the
// single-decision Check grants.
func (en *Enforcer) grantIndex(accessorID string) (*grantIndex, error) {
	rows, err := en.e.GetImplicitPermissionsForUser(accessorID)
	if err != nil {
		return nil, err
	}
	public, err := en.e.GetFilteredPolicy(0, PublicAccessorID)
	if err != nil {
		return nil, err
	}

	superAdmin, err := en.hasSuperAdminRole(accessorID)
	if err != nil {
		return nil, err
	}
	roles, err := en.e.GetImplicitRolesForUser(accessorID)
	if err != nil {
		return nil, err
	}
	edition := entitlement.Current()
	rows = activePolicyRowsForEdition(rows, edition)
	public = activePolicyRowsForEdition(public, edition)
	idx := newGrantIndex(append(rows, public...), superAdmin)
	seenSubject := map[string]bool{}
	for _, subject := range append(append([]string{accessorID}, roles...), PublicAccessorID) {
		if subject == "" || seenSubject[subject] {
			continue
		}
		seenSubject[subject] = true
		idx.subjects = append(idx.subjects, subject)
	}
	return idx, nil
}

func newGrantIndex(rows [][]string, superAdmin bool) *grantIndex {
	idx := &grantIndex{exact: make(map[string][]grantRow, len(rows)), superAdmin: superAdmin}
	for _, row := range rows {
		if len(row) < 4 {
			continue
		}
		object, act, effect := row[1], row[2], row[3]
		if effect == "" {
			effect = EffectAllow
		}
		rule := grantRow{object: object, act: act, effect: effect, source: policySourceOf(row)}
		if hasWildcard(object) {
			idx.wildcard = append(idx.wildcard, rule)
			continue
		}
		idx.exact[object] = append(idx.exact[object], rule)
	}
	return idx
}

type localParts struct {
	direct   map[string]Evaluation
	wildcard map[string]Evaluation
}

// localParts keeps exact-instance and wildcard rules separate. A wildcard deny
// is terminal, but a wildcard allow is only a fallback after parent traversal;
// collapsing them here was the old source of parent-deny bypasses.
func (idx *grantIndex) localParts(r ResourceRef, ops []string) localParts {
	parts := localParts{
		direct:   make(map[string]Evaluation, len(ops)),
		wildcard: make(map[string]Evaluation, len(ops)),
	}
	if idx.superAdmin {
		for _, op := range ops {
			parts.direct[op] = Evaluation{Scope: ScopeLocal, Decision: DecisionAllow, Basis: BasisDirect}
		}
		return parts
	}
	object := obj(r.Type, r.ID)
	apply := func(target map[string]Evaluation, basis DecisionBasis, rule grantRow) {
		if rule.source == PolicySourceCommunityBundle {
			// A bundle is valid only as one exact, allow-only logical policy.
			// Keeping the check here makes malformed wildcard/concrete-op bundle
			// rows fail closed rather than acquiring ordinary Casbin semantics.
			if rule.object != object || rule.act != ActFullBusinessAccess || rule.effect != EffectAllow {
				return
			}
			for _, op := range ops {
				if communityBundleAllows(r.Type, op) && target[op].Decision == "" {
					target[op] = Evaluation{Scope: ScopeLocal, Decision: DecisionAllow, Basis: BasisBundle}
				}
			}
			return
		}
		for _, op := range ops {
			if rule.act != ActAll && op != rule.act {
				continue
			}
			current := target[op]
			if rule.effect == EffectDeny {
				target[op] = Evaluation{Scope: ScopeLocal, Decision: DecisionDeny, Basis: basis}
				continue
			}
			if current.Decision == "" || (current.Basis == BasisBundle && basis == BasisDirect) {
				target[op] = Evaluation{Scope: ScopeLocal, Decision: DecisionAllow, Basis: basis}
			}
		}
	}
	for _, rule := range idx.exact[object] {
		apply(parts.direct, BasisDirect, rule)
	}
	for _, row := range idx.wildcard {
		if util.KeyMatch(object, row.object) {
			apply(parts.wildcard, BasisWildcard, row)
		}
	}
	return parts
}

func mergeLocalParts(parts localParts, ops []string) map[string]Evaluation {
	out := make(map[string]Evaluation, len(ops))
	for _, op := range ops {
		direct, wildcard := parts.direct[op], parts.wildcard[op]
		switch {
		case direct.Decision == DecisionDeny:
			out[op] = direct
		case wildcard.Decision == DecisionDeny:
			out[op] = wildcard
		case direct.Decision == DecisionAllow:
			out[op] = direct
		case wildcard.Decision == DecisionAllow:
			out[op] = wildcard
		default:
			out[op] = noneEvaluation(ScopeLocal)
		}
	}
	return out
}

// localDecisions merges all same-resource Core and Enterprise sources before
// hierarchy evaluation. The Enterprise provider returns exact and wildcard
// opinions separately so Core retains the documented wildcard fallback order.
func (en *Enforcer) localDecisions(ctx context.Context, accessorID string, idx *grantIndex,
	r ResourceRef, ops []string) (map[string]Evaluation, error) {
	parts := idx.localParts(r, ops)
	if idx.superAdmin || !permobject.Available() {
		return mergeLocalParts(parts, ops), nil
	}
	coreDecisions := mergeLocalParts(parts, ops)
	for _, op := range ops {
		core := coreDecisions[op]
		opinion, err := permobject.Decide(ctx, permobject.Request{
			AccessorID: accessorID, AccessorIDs: append([]string(nil), idx.subjects...),
			ResourceType: r.Type, ResourceID: r.ID, Op: op,
			CoreDecision: permobject.CoreDecision(core.Decision), CoreBasis: permobject.CoreBasis(core.Basis),
		})
		if err != nil {
			return nil, err
		}
		mergeOpinion := func(target map[string]Evaluation, decision permobject.Decision, basis DecisionBasis) error {
			switch decision {
			case permobject.Abstain:
				return nil
			case permobject.Deny:
				target[op] = Evaluation{Scope: ScopeLocal, Decision: DecisionDeny, Basis: basis}
			case permobject.Allow:
				if target[op].Decision != DecisionDeny {
					target[op] = Evaluation{Scope: ScopeLocal, Decision: DecisionAllow, Basis: basis}
				}
			default:
				return fmt.Errorf("permobject returned invalid decision %d", decision)
			}
			return nil
		}
		if err := mergeOpinion(parts.direct, opinion.Direct, BasisDirect); err != nil {
			return nil, err
		}
		if err := mergeOpinion(parts.wildcard, opinion.Wildcard, BasisWildcard); err != nil {
			return nil, err
		}
	}
	return mergeLocalParts(parts, ops), nil
}

// decide is retained for migration/projection helpers that only need the local
// Core effect. It delegates to the same structured local implementation.
func (idx *grantIndex) decide(r ResourceRef, ops []string) map[string]string {
	structured := mergeLocalParts(idx.localParts(r, ops), ops)
	out := make(map[string]string, len(ops))
	for op, d := range structured {
		switch d.Decision {
		case DecisionAllow:
			out[op] = EffectAllow
		case DecisionDeny:
			out[op] = EffectDeny
		}
	}
	return out
}

// hasWildcard reports whether an object pattern needs keyMatch rather than a
// plain equality lookup. keyMatch treats "*" as the only wildcard, so a pattern
// without one matches exactly itself.
func hasWildcard(object string) bool {
	for i := 0; i < len(object); i++ {
		if object[i] == '*' {
			return true
		}
	}
	return false
}
