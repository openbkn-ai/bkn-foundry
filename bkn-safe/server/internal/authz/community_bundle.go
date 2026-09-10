// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import "fmt"

// communityBundleOperations is the reviewed Community business-operation
// contract. It is deliberately an explicit per-root whitelist: the operation
// directory also contains create, authorization, public-access, system and
// cross-owner operations that must never be acquired by scanning it.
//
// Keep the slice order stable. It is used by permission-read projections and
// therefore becomes the order returned to API consumers.
var communityBundleOperations = map[string][]string{
	"catalog":           {"view_detail", "modify", "delete", "query_data", "resource_manage", "task_manage"},
	"knowledge_network": {"view_detail", "modify", "delete", "query_data", "execute"},
	"connector_type":    {"view_detail", "modify", "delete", "task_manage"},
	"tool_box":          {"view", "modify", "delete", "publish", "unpublish", "execute"},
	"mcp":               {"view", "modify", "delete", "publish", "unpublish", "execute"},
	"operator":          {"view", "modify", "delete", "publish", "unpublish", "execute"},
	"skill":             {"view", "modify", "delete", "publish", "unpublish", "execute"},
	"small_model":       {"display", "modify", "delete", "execute"},
	"large_model":       {"display", "modify", "delete", "execute"},
}

// CommunityBundleOperations returns a copy of the reviewed operation whitelist
// for one top-level resource type. The bool is false for child, administrative
// or otherwise unsupported resource types.
func CommunityBundleOperations(resourceType string) ([]string, bool) {
	ops, ok := communityBundleOperations[resourceType]
	if !ok {
		return nil, false
	}
	return append([]string(nil), ops...), true
}

func isCommunityBundleRow(row []string) bool {
	return len(row) >= 6 && row[2] == ActFullBusinessAccess &&
		policyEffect(row) == EffectAllow && policySourceOf(row) == PolicySourceCommunityBundle
}

func isCommunityBundleSourceRow(row []string) bool {
	return policySourceOf(row) == PolicySourceCommunityBundle
}

func communityBundleAllows(resourceType, operation string) bool {
	for _, allowed := range communityBundleOperations[resourceType] {
		if allowed == operation {
			return true
		}
	}
	return false
}

// projectCommunityBundleRows replaces logical bundle rows with the concrete
// operations decided by grantIndex.decide. When combineSubjects is true, rows
// already represent all policies effective for one accessor (direct and role
// derived) and must be decided together. Direct policy-table reads keep each
// subject separate.
//
// No whitelist operation is granted here directly: this function only supplies
// candidates to the shared decision layer, which remains the unique expansion
// point.
func projectCommunityBundleRows(rows [][]string, combineSubjects bool) [][]string {
	out := make([][]string, 0, len(rows))
	type bundleKey struct{ subject, object string }
	type bundleProjection struct {
		key         bundleKey
		emitSubject string
	}
	bundles := make([]bundleProjection, 0)
	seenBundle := map[bundleKey]bool{}
	var rowsBySubject map[string][][]string
	var indexBySubject map[string]*grantIndex
	if !combineSubjects {
		rowsBySubject = map[string][][]string{}
		indexBySubject = map[string]*grantIndex{}
	}
	for _, row := range rows {
		if !combineSubjects && len(row) > 0 {
			rowsBySubject[row[0]] = append(rowsBySubject[row[0]], row)
		}
		if !isCommunityBundleSourceRow(row) {
			out = append(out, row)
			continue
		}
		// Invalid community_bundle rows are deliberately absent from both the
		// runtime index and permission projections. The trusted writer below
		// cannot create one; a malformed migrated row therefore fails closed.
		if !isCommunityBundleRow(row) {
			continue
		}
		if len(row) < 2 {
			continue
		}
		key := bundleKey{subject: row[0], object: row[1]}
		if combineSubjects {
			key.subject = ""
		}
		if seenBundle[key] {
			continue
		}
		seenBundle[key] = true
		bundles = append(bundles, bundleProjection{key: key, emitSubject: row[0]})
	}
	if len(bundles) == 0 {
		return out
	}
	var combined *grantIndex
	if combineSubjects {
		combined = newGrantIndex(rows, false)
	}
	for _, bundle := range bundles {
		resourceType, resourceID := splitObjectKey(bundle.key.object)
		ops, ok := CommunityBundleOperations(resourceType)
		if !ok || resourceID == "" || hasWildcard(resourceID) {
			continue
		}
		idx := combined
		if !combineSubjects {
			idx = indexBySubject[bundle.key.subject]
			if idx == nil {
				idx = newGrantIndex(rowsBySubject[bundle.key.subject], false)
				indexBySubject[bundle.key.subject] = idx
			}
		}
		decision := idx.decide(ResourceRef{Type: resourceType, ID: resourceID}, ops)
		for _, op := range ops {
			if decision[op] != EffectAllow {
				continue
			}
			out = append(out, []string{bundle.emitSubject, bundle.key.object, op, EffectAllow,
				string(PolicySourceCommunityBundle), string(AuthoritySourceSystem)})
		}
	}
	return out
}

func validateCommunityBundleTarget(resourceType, resourceID string) error {
	if resourceID == "" || hasWildcard(resourceID) {
		return fmt.Errorf("community bundle requires a concrete resource id")
	}
	if _, ok := CommunityBundleOperations(resourceType); !ok {
		return fmt.Errorf("resource type %q is not a Community authorization root", resourceType)
	}
	return nil
}
