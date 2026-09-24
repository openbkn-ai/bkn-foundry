// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func conceptFixture() *KnowledgeNetworkDetail {
	return &KnowledgeNetworkDetail{
		ID: "kn-1", Name: "sales", Comment: "the network itself",
		ObjectTypes: []*ObjectType{{
			ID: "ot_order", Name: "order", ModuleType: "object_type", Score: 0,
			Tags: []string{"reviewed"}, Comment: "long prose about what an order is",
		}},
		RelationTypes: []*RelationType{{
			ID: "rt_places", Name: "places", ModuleType: "relation_type",
			Tags: []string{"reviewed"}, Comment: "long prose about the relation",
		}},
		ActionTypes: []*ActionType{{
			ID: "at_cancel", Name: "cancel", ModuleType: "action_type",
			Tags: []string{"requires_confirmation"}, Comment: "what cancelling does",
		}},
		ConceptGroups: []*ConceptGroup{{
			ID: "cg1", Name: "core", Tags: []string{"sales"},
			Comment: "what this group is about",
		}},
	}
}

// The summary is a map to choose from, not a description of everything on it: the prose
// belongs to the drill-downs, and module_type repeats what the array it sits in already says.
func TestSummaryDropsConceptProseAndModuleType(t *testing.T) {
	d := conceptFixture()

	d.Slim(DetailLevelSummary)

	if d.ObjectTypes[0].Comment != "" || d.RelationTypes[0].Comment != "" || d.ActionTypes[0].Comment != "" {
		t.Errorf("concept prose was kept: %+v", d)
	}
	if d.ObjectTypes[0].ModuleType != "" || d.RelationTypes[0].ModuleType != "" || d.ActionTypes[0].ModuleType != "" {
		t.Errorf("module_type was kept: %+v", d)
	}
}

// A caller picks 1-3 concept groups off the summary, and names alone do not carry enough to
// pick by. Tags stay everywhere: requires_confirmation changes how a caller may use a concept.
func TestSummaryKeepsWhatTheCallerChoosesBy(t *testing.T) {
	d := conceptFixture()

	d.Slim(DetailLevelSummary)

	if d.ConceptGroups[0].Comment == "" || len(d.ConceptGroups[0].Tags) == 0 {
		t.Errorf("the group choice was made harder: %+v", d.ConceptGroups[0])
	}
	if d.Comment == "" {
		t.Errorf("the network's own description went missing: %+v", d)
	}
	if len(d.ObjectTypes[0].Tags) == 0 || len(d.ActionTypes[0].Tags) == 0 {
		t.Errorf("tags were dropped: %+v", d)
	}
}

// get_kn_detail is not a search, and bkn-backend scores every concept 0 here, so the field is
// noise at either level.
func TestScoreNeverReachesTheCaller(t *testing.T) {
	for _, level := range []string{DetailLevelSummary, DetailLevelFull} {
		d := conceptFixture()
		d.ObjectTypes[0].Score = 0.42

		d.Slim(level)

		if d.ObjectTypes[0].Score != 0 {
			t.Errorf("%s published a score: %v", level, d.ObjectTypes[0].Score)
		}
	}
}

// full is the level that returns everything, and the prose is part of everything.
func TestFullKeepsTheConceptProse(t *testing.T) {
	d := conceptFixture()

	d.Slim(DetailLevelFull)

	if d.ObjectTypes[0].Comment == "" || d.RelationTypes[0].Comment == "" || d.ActionTypes[0].Comment == "" {
		t.Errorf("full lost the prose: %+v", d)
	}
	if d.ObjectTypes[0].ModuleType == "" {
		t.Errorf("full lost module_type: %+v", d.ObjectTypes[0])
	}
}

// Clearing a field only saves bytes if the encoder then leaves it out.
func TestTheDroppedFieldsLeaveNoEmptyKeys(t *testing.T) {
	d := conceptFixture()

	d.Slim(DetailLevelSummary)
	encoded, err := sonic.ConfigStd.Marshal(d)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	published := string(encoded)
	for _, key := range []string{`"module_type"`, `"_score"`} {
		if strings.Contains(published, key) {
			t.Errorf("%s is still on the wire: %s", key, published)
		}
	}
	// The network keeps its own comment, so exactly one comment key survives per group entry.
	if strings.Count(published, `"comment"`) != 2 {
		t.Errorf("only the network and the concept group describe themselves: %s", published)
	}
}
