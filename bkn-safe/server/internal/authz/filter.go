// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"github.com/casbin/casbin/v2/util"
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
	return en.filterResourceOps(accessorID, resources, visibility, candidates, true)
}

// filterResourceOps performs the batched Casbin and hierarchy projection. The
// provenance flag is disabled only while validating the human delegators that
// back a managed proxy source; those checks must never recurse through proxy
// provenance.
func (en *Enforcer) filterResourceOps(accessorID string, resources []ResourceRef, visibility, candidates []string,
	validateProvenance bool) ([]FilteredResource, error) {
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

	decided := map[ResourceRef]map[string]bool{}
	terminal := map[ResourceRef]map[string]bool{}
	for _, r := range resources {
		if _, done := decided[r]; !done {
			decided[r] = map[string]bool{}
			terminal[r] = map[string]bool{}
			for op, effect := range idx.decide(r, union) {
				if effect == EffectAllow {
					decided[r][op] = true
				} else if effect == EffectDeny {
					terminal[r][op] = true
				}
			}
		}
	}

	// Everything not granted on the resource itself gets one batched walk up the
	// hierarchy — the same walk Check uses, so a list page and a detail page
	// cannot disagree about an inherited grant. Skipping this would not merely
	// lose operations: a resource visible only through its catalog would vanish
	// from the page entirely, which reads as data loss rather than as a denial.
	stillMissing := map[ResourceRef][]string{}
	for r, allowed := range decided {
		var missing []string
		for _, op := range union {
			if !allowed[op] && !terminal[r][op] {
				missing = append(missing, op)
			}
		}
		if len(missing) > 0 {
			stillMissing[r] = missing
		}
	}
	inherited, err := en.climb(func(node ResourceRef, ops []string) (map[string]string, error) {
		return idx.decide(node, ops), nil
	}, stillMissing)
	if err != nil {
		return nil, err
	}
	for r, ops := range inherited {
		for op := range ops {
			decided[r][op] = true
		}
	}

	// Managed proxies have a second, provenance-aware condition that is not
	// represented in Casbin: an exact active source must still be valid. Apply
	// it after the batched raw-policy calculation so list/filter decisions stay
	// identical to Check. Human and ordinary app accessors keep the optimized
	// path above without per-decision source lookups.
	if validateProvenance && en.db != nil {
		managed, err := en.isManagedProxy(accessorID)
		if err != nil {
			return nil, err
		}
		if managed {
			current, err := en.currentProxyPermissions(accessorID)
			if err != nil {
				return nil, err
			}
			for resource, allowed := range decided {
				for operation, rawAllowed := range allowed {
					if !rawAllowed {
						continue
					}
					allowed[operation] = current[proxyPermission{
						ResourceType: resource.Type,
						ResourceID:   resource.ID,
						Operation:    operation,
					}]
				}
			}
		}
	}

	out := make([]FilteredResource, 0, len(resources))
	for _, r := range resources {
		allowed := decided[r]
		visible := true
		for _, op := range visibility {
			if !allowed[op] {
				visible = false
				break
			}
		}
		if !visible {
			continue
		}
		ops := make([]string, 0, len(candidates))
		for _, op := range candidates {
			if allowed[op] {
				ops = append(ops, op)
			}
		}
		out = append(out, FilteredResource{Type: r.Type, ID: r.ID, Operations: ops})
	}
	return out, nil
}

// grantRow is one policy line the accessor can invoke: the object pattern and
// the operation it grants (act "*" grants every operation).
type grantRow struct {
	object string
	act    string
	effect string
}

// grantIndex is an accessor's effective grant set, split by whether the object
// pattern needs wildcard matching. Exact patterns resolve by map lookup; only
// the (few) wildcard patterns are walked per resource, which keeps a list page
// linear in resources rather than resources x policies.
type grantIndex struct {
	exact      map[string][]grantRow // object key -> rules
	wildcard   []grantRow
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
	idx := &grantIndex{exact: make(map[string][]grantRow, len(rows)), superAdmin: superAdmin}
	for _, row := range append(rows, public...) {
		if len(row) < 4 {
			continue
		}
		object, act, effect := row[1], row[2], row[3]
		if effect == "" {
			effect = EffectAllow
		}
		rule := grantRow{object: object, act: act, effect: effect}
		if hasWildcard(object) {
			idx.wildcard = append(idx.wildcard, rule)
			continue
		}
		idx.exact[object] = append(idx.exact[object], rule)
	}
	return idx, nil
}

// allowed reports, for one resource, which of ops the grants cover. It mirrors
// the matcher's remaining two clauses: keyMatch(r.obj, p.obj) — via the same
// util.KeyMatch the model uses — and (p.act == "*" || r.act == p.act).
func (idx *grantIndex) decide(r ResourceRef, ops []string) map[string]string {
	out := make(map[string]string, len(ops))
	if idx.superAdmin {
		for _, op := range ops {
			out[op] = EffectAllow
		}
		return out
	}
	object := obj(r.Type, r.ID)
	apply := func(rule grantRow) {
		if rule.act == ActAll {
			for _, op := range ops {
				if rule.effect == EffectDeny || out[op] == "" {
					out[op] = rule.effect
				}
			}
			return
		}
		for _, op := range ops {
			if op == rule.act && (rule.effect == EffectDeny || out[op] == "") {
				out[op] = rule.effect
			}
		}
	}
	for _, rule := range idx.exact[object] {
		apply(rule)
	}
	for _, row := range idx.wildcard {
		if util.KeyMatch(object, row.object) {
			apply(row)
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
