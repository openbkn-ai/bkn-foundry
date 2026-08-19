// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func objectTypesForScope(ids ...string) []*interfaces.ObjectType {
	out := make([]*interfaces.ObjectType, 0, len(ids))
	for _, id := range ids {
		out = append(out, &interfaces.ObjectType{ID: id, Name: id})
	}
	return out
}

func keptIDs(objectTypes []*interfaces.ObjectType) []string {
	out := make([]string, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		out = append(out, objectType.ID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewObjectTypeScopeNilWhenNothingPinned(t *testing.T) {
	if scope := newObjectTypeScope(nil, nil); scope != nil {
		t.Fatalf("expected nil scope, got %+v", scope)
	}
	// Blank entries are not a scope: trimming them away must not turn "no filter" into
	// "filter that matches nothing", which would drop every object type.
	if scope := newObjectTypeScope([]string{"", "  "}, []string{" "}); scope != nil {
		t.Fatalf("expected nil scope for blank ids, got %+v", scope)
	}
}

func TestObjectTypeScopeNilKeepsEverything(t *testing.T) {
	objectTypes := objectTypesForScope("material", "product")
	kept, unmatched := (*objectTypeScope)(nil).apply(objectTypes)
	if !equalStrings(keptIDs(kept), []string{"material", "product"}) {
		t.Fatalf("nil scope changed the pool: %v", keptIDs(kept))
	}
	if len(unmatched) != 0 {
		t.Fatalf("nil scope reported unmatched ids: %v", unmatched)
	}
}

// The allow list is applied to the raw candidate pool, so an object type that would rank below the
// max_object_types cut still survives. This is the regression that filtering after ranking causes.
func TestObjectTypeScopeKeepsLowRankedPin(t *testing.T) {
	objectTypes := objectTypesForScope(
		"ot01", "ot02", "ot03", "ot04", "ot05",
		"ot06", "ot07", "ot08", "ot09", "ot10", "inventory",
	)
	scope := newObjectTypeScope([]string{"inventory"}, nil)
	kept, unmatched := scope.apply(objectTypes)
	if !equalStrings(keptIDs(kept), []string{"inventory"}) {
		t.Fatalf("expected the pinned object type to survive, got %v", keptIDs(kept))
	}
	if len(unmatched) != 0 {
		t.Fatalf("unexpected unmatched ids: %v", unmatched)
	}
}

func TestObjectTypeScopeExcludeWinsOverInclude(t *testing.T) {
	objectTypes := objectTypesForScope("material", "product", "bom")
	scope := newObjectTypeScope([]string{"material", "product"}, []string{"product"})
	kept, unmatched := scope.apply(objectTypes)
	if !equalStrings(keptIDs(kept), []string{"material"}) {
		t.Fatalf("exclusion did not win over inclusion: %v", keptIDs(kept))
	}
	// An id excluded on purpose was still found; it is not a caller mistake to report.
	if len(unmatched) != 0 {
		t.Fatalf("excluded id reported as unmatched: %v", unmatched)
	}
}

func TestObjectTypeScopeExcludeOnly(t *testing.T) {
	objectTypes := objectTypesForScope("material", "audit_log", "product")
	scope := newObjectTypeScope(nil, []string{"audit_log", "does_not_exist"})
	kept, unmatched := scope.apply(objectTypes)
	if !equalStrings(keptIDs(kept), []string{"material", "product"}) {
		t.Fatalf("exclude-only scope kept the wrong pool: %v", keptIDs(kept))
	}
	// An unknown id in the deny list is a no-op, not an error: "drop it if present" is the
	// whole contract, and reporting it would train callers to ignore the report.
	if len(unmatched) != 0 {
		t.Fatalf("deny list reported unmatched ids: %v", unmatched)
	}
}

func TestObjectTypeScopeReportsUnmatchedInCallerSpelling(t *testing.T) {
	objectTypes := objectTypesForScope("material", "product")
	// "物料" is material's *name*: the shape of the mistake this parameter is meant to surface.
	scope := newObjectTypeScope([]string{"物料", "Material", "no_such_type"}, nil)
	kept, unmatched := scope.apply(objectTypes)
	if !equalStrings(keptIDs(kept), []string{"material"}) {
		t.Fatalf("case-insensitive id match failed: %v", keptIDs(kept))
	}
	if !equalStrings(unmatched, []string{"物料", "no_such_type"}) {
		t.Fatalf("unmatched ids wrong or reordered: %v", unmatched)
	}
}

func TestObjectTypeScopeSkipsNilEntries(t *testing.T) {
	objectTypes := []*interfaces.ObjectType{nil, {ID: "material"}, nil}
	scope := newObjectTypeScope([]string{"material"}, nil)
	kept, _ := scope.apply(objectTypes)
	if !equalStrings(keptIDs(kept), []string{"material"}) {
		t.Fatalf("nil entries leaked into the kept pool: %v", keptIDs(kept))
	}
}

func TestNormalizeObjectTypeIDs(t *testing.T) {
	got := normalizeObjectTypeIDs([]string{" Material ", "material", "", "product", "MATERIAL"})
	if !equalStrings(got, []string{"Material", "product"}) {
		t.Fatalf("normalization wrong: %v", got)
	}
	if got := normalizeObjectTypeIDs(nil); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}
