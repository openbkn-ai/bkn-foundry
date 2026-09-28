// Copyright openbkn.ai

package interfaces

import "testing"

func networkWith(objectTypes, relationTypes, actionTypes int) *KnowledgeNetworkDetail {
	d := &KnowledgeNetworkDetail{
		ID:            "kn-001",
		ConceptGroups: []*ConceptGroup{{ID: "cg-1", Name: "临床", Comment: "就诊与医嘱"}},
	}
	for range objectTypes {
		d.ObjectTypes = append(d.ObjectTypes, &ObjectType{ID: "ot"})
	}
	for range relationTypes {
		d.RelationTypes = append(d.RelationTypes, &RelationType{ID: "rt"})
	}
	for range actionTypes {
		d.ActionTypes = append(d.ActionTypes, &ActionType{ID: "at"})
	}
	return d
}

func TestNeedsNavigationShellTurnsOnJustPastEitherCap(t *testing.T) {
	if networkWith(MaxSummaryObjectTypes, MaxSummaryRelationTypes, 0).NeedsNavigationShell() {
		t.Fatalf("a network at both caps must still carry its model")
	}
	if !networkWith(MaxSummaryObjectTypes+1, 0, 0).NeedsNavigationShell() {
		t.Fatalf("a network past the object type cap must narrow")
	}
	// Relations cost no downstream call, so they were not what made #1877 time out --
	// but they were a quarter of that network's bytes, and a relation-heavy model can
	// be unreadable on their count alone.
	if !networkWith(1, MaxSummaryRelationTypes+1, 0).NeedsNavigationShell() {
		t.Fatalf("a network past the relation type cap must narrow too")
	}
	var absent *KnowledgeNetworkDetail
	if absent.NeedsNavigationShell() {
		t.Fatalf("a nil detail has nothing to narrow")
	}
}

// The shell has to keep what a caller narrows by, and has to say how much it is
// standing in for -- empty arrays with no counts read as a network with no concepts.
func TestReduceToNavigationShellKeepsTheWayIn(t *testing.T) {
	d := networkWith(1000, 2925, 7)
	d.ReduceToNavigationShell()

	if d.ObjectTypes != nil || d.RelationTypes != nil || d.ActionTypes != nil {
		t.Fatalf("the three concept arrays must be dropped, got %d/%d/%d",
			len(d.ObjectTypes), len(d.RelationTypes), len(d.ActionTypes))
	}
	if d.ObjectTypeCount != 1000 || d.RelationTypeCount != 2925 || d.ActionTypeCount != 7 {
		t.Fatalf("counts = %d/%d/%d, want 1000/2925/7",
			d.ObjectTypeCount, d.RelationTypeCount, d.ActionTypeCount)
	}
	if len(d.ConceptGroups) != 1 || d.ConceptGroups[0].Comment == "" {
		t.Fatalf("concept groups are the way in and must survive with their comment, got %+v", d.ConceptGroups)
	}
	if d.ID != "kn-001" {
		t.Fatalf("the network's own record must survive, got %q", d.ID)
	}
}

// Slim runs after the shell on the oversized path, so it has to tolerate the
// arrays being gone rather than assume it is trimming them.
func TestSlimAfterNavigationShellIsNilSafe(t *testing.T) {
	d := networkWith(1000, 10, 2)
	d.ReduceToNavigationShell()
	d.Slim(DetailLevelSummary)

	if d.ObjectTypeCount != 1000 {
		t.Fatalf("Slim must not erase the counts, got %d", d.ObjectTypeCount)
	}
	if len(d.ConceptGroups) != 1 {
		t.Fatalf("concept groups lost, got %+v", d.ConceptGroups)
	}
}

// The network #1877 reported defines no concept groups, so the notice that tells a
// caller to pick some would be pointing at an empty field.
func TestNavigationShellNoticeFollowsWhetherThereAreConceptGroups(t *testing.T) {
	withGroups := networkWith(1000, 0, 0)
	if got := withGroups.NavigationShellNoticeKey(); got != "KnDetailNavigationShell" {
		t.Fatalf("with concept groups: key = %q", got)
	}
	withGroups.ConceptGroups = nil
	if got := withGroups.NavigationShellNoticeKey(); got != "KnDetailNavigationShellNoGroups" {
		t.Fatalf("without concept groups: key = %q, want the one that points at search_schema", got)
	}
}
