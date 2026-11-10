// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func jsonSize(d *KnowledgeNetworkDetail) (int, error) {
	body, err := json.Marshal(d)
	return len(body), err
}

func noticeNamed(reduction KnDetailReduction) string { return "narrowed to " + string(reduction) }

// A network whose object types carry many properties: the shape that fills a summary.
func propertyHeavyNetwork(objectTypes, properties int) *KnowledgeNetworkDetail {
	d := &KnowledgeNetworkDetail{
		ID:            "kn-1",
		Name:          "供应链",
		Comment:       "采购、库存与交付",
		ConceptGroups: []*ConceptGroup{{ID: "cg-1", Name: "采购", ObjectTypeIDs: []string{"ot-0000"}}},
		ActionTypes:   []*ActionType{{ID: "at-1", Name: "下单", Comment: "创建采购订单"}},
	}
	for i := range objectTypes {
		o := &ObjectType{ID: fmt.Sprintf("ot-%04d", i), Name: fmt.Sprintf("对象类-%04d", i), Comment: "长段说明",
			Tags: []string{"core"}, RelatedMetricCount: 2,
			EffectivePermissions: map[string]PropertyAccessLevel{"p": PropertyAccessFull}}
		for j := range properties {
			o.DataProperties = append(o.DataProperties, &DataProperty{Name: fmt.Sprintf("property_%02d", j), Type: "string"})
		}
		o.LogicProperties = []*LogicPropertyDef{{Name: "score", Type: "metric"}}
		d.ObjectTypes = append(d.ObjectTypes, o)
		d.RelationTypes = append(d.RelationTypes, &RelationType{ID: fmt.Sprintf("rt-%04d", i), Name: "属于",
			SourceObjectTypeID: o.ID, TargetObjectTypeID: "ot-0000", Comment: "说明", MappingRules: []string{"rule"}})
	}
	return d
}

func TestFitWithinLeavesAnAnswerThatFits(t *testing.T) {
	d := propertyHeavyNetwork(2, 3)
	n, _ := jsonSize(d)
	reduction, err := d.FitWithin(n, noticeNamed, jsonSize)
	if err != nil || reduction != KnDetailReductionNone {
		t.Fatalf("reduction = %q, err = %v; want none for an answer at the limit", reduction, err)
	}
	if d.Notice != "" || len(d.ObjectTypes[0].DataProperties) != 3 {
		t.Fatalf("an answer that fits must be left as it is, got notice %q", d.Notice)
	}
}

// The outline keeps every concept and what a caller picks it by, and drops what a
// caller reads only for the few it picks.
func TestFitWithinNarrowsToTheOutlineFirst(t *testing.T) {
	d := propertyHeavyNetwork(40, 30)
	reduction, err := d.FitWithin(20000, noticeNamed, jsonSize)
	if err != nil || reduction != KnDetailReductionOutline {
		t.Fatalf("reduction = %q, err = %v; want outline", reduction, err)
	}
	if n, _ := jsonSize(d); n > 20000 {
		t.Fatalf("outline is %d characters, past the limit it was fitted to", n)
	}
	if len(d.ObjectTypes) != 40 || len(d.RelationTypes) != 40 || len(d.ActionTypes) != 1 {
		t.Fatalf("the outline must keep every concept, got %d/%d/%d", len(d.ObjectTypes), len(d.RelationTypes), len(d.ActionTypes))
	}
	o := d.ObjectTypes[0]
	if o.DataProperties != nil || o.LogicProperties != nil || o.EffectivePermissions != nil || o.Comment != "" {
		t.Fatalf("the outline must drop the property lists and prose, got %+v", o)
	}
	if o.DataPropertyCount != 30 || o.LogicPropertyCount != 1 {
		t.Fatalf("property counts = %d/%d, want 30/1", o.DataPropertyCount, o.LogicPropertyCount)
	}
	if o.ID != "ot-0000" || o.Name == "" || len(o.Tags) != 1 || o.RelatedMetricCount != 2 {
		t.Fatalf("the outline must keep what an object type is chosen by, got %+v", o)
	}
	r := d.RelationTypes[1]
	if r.MappingRules != nil || r.Comment != "" || r.SourceObjectTypeID != "ot-0001" || r.TargetObjectTypeID != "ot-0000" {
		t.Fatalf("the outline must keep a relation's endpoints and drop its rules, got %+v", r)
	}
	if d.Notice != noticeNamed(KnDetailReductionOutline) {
		t.Fatalf("notice = %q, want the outline's", d.Notice)
	}
	if d.ObjectTypeCount != 0 {
		t.Fatalf("the arrays are the count on an outline, got object_type_count %d", d.ObjectTypeCount)
	}
}

func TestFitWithinFallsBackToTheShell(t *testing.T) {
	d := propertyHeavyNetwork(400, 1)
	reduction, err := d.FitWithin(3000, noticeNamed, jsonSize)
	if err != nil || reduction != KnDetailReductionShell {
		t.Fatalf("reduction = %q, err = %v; want shell", reduction, err)
	}
	if !d.IsNavigationShell() || d.ObjectTypes != nil || d.ObjectTypeCount != 400 || d.RelationTypeCount != 400 {
		t.Fatalf("want the navigation shell with counts, got %d object types, count %d", len(d.ObjectTypes), d.ObjectTypeCount)
	}
	if d.Notice != noticeNamed(KnDetailReductionShell) {
		t.Fatalf("notice = %q, want the shell's", d.Notice)
	}
	if len(d.ConceptGroups[0].ObjectTypeIDs) != 1 {
		t.Fatalf("a shell that fits keeps its groups' object type ids")
	}
}

// The concept groups are the one part of a shell that grows with the network.
func TestFitWithinDropsGroupMembershipFromAShellStillTooLarge(t *testing.T) {
	d := &KnowledgeNetworkDetail{ID: "kn-1"}
	for i := range 50 {
		g := &ConceptGroup{ID: fmt.Sprintf("cg-%02d", i), Name: "分组"}
		for j := range 40 {
			g.ObjectTypeIDs = append(g.ObjectTypeIDs, fmt.Sprintf("ot-%02d-%02d", i, j))
		}
		d.ConceptGroups = append(d.ConceptGroups, g)
	}
	d.ReduceToNavigationShell()
	reduction, err := d.FitWithin(5000, noticeNamed, jsonSize)
	if err != nil || reduction != KnDetailReductionShell {
		t.Fatalf("reduction = %q, err = %v; want shell", reduction, err)
	}
	if len(d.ConceptGroups) != 50 || d.ConceptGroups[0].ObjectTypeIDs != nil || d.ConceptGroups[0].Name == "" {
		t.Fatalf("want every group kept by name without its member ids, got %+v", d.ConceptGroups[0])
	}
	if d.Notice != "" {
		t.Fatalf("a shell built before fitting keeps the notice it was built with, got %q", d.Notice)
	}
}

func TestFitWithinReportsAMeasuringFailure(t *testing.T) {
	d := propertyHeavyNetwork(1, 1)
	boom := errors.New("render failed")
	if _, err := d.FitWithin(10, noticeNamed, func(*KnowledgeNetworkDetail) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the measuring failure", err)
	}
	var absent *KnowledgeNetworkDetail
	if reduction, err := absent.FitWithin(10, noticeNamed, jsonSize); err != nil || reduction != KnDetailReductionNone {
		t.Fatalf("a nil detail has nothing to fit, got %q %v", reduction, err)
	}
}

func TestKnDetailNoticeNamesTheReason(t *testing.T) {
	grouped := &KnowledgeNetworkDetail{ConceptGroups: []*ConceptGroup{{ID: "cg"}}}
	cases := []struct {
		name      string
		detail    *KnowledgeNetworkDetail
		reduction KnDetailReduction
		level     string
		branch    string
		want      string
	}{
		{"outline", grouped, KnDetailReductionOutline, DetailLevelSummary, "", "KnDetailOutline"},
		{"outline off main", grouped, KnDetailReductionOutline, DetailLevelFull, "release", "KnDetailOutline"},
		{"brief", grouped, KnDetailReductionShell, DetailLevelBrief, "release", "KnDetailBrief"},
		{"shell", grouped, KnDetailReductionShell, DetailLevelSummary, "main", "KnDetailNavigationShell"},
		{"shell without groups", &KnowledgeNetworkDetail{}, KnDetailReductionShell, DetailLevelSummary, "", "KnDetailNavigationShellNoGroups"},
		{"shell off main", grouped, KnDetailReductionShell, DetailLevelFull, " release ", "KnDetailNavigationShellBranch"},
	}
	for _, tc := range cases {
		key, args := tc.detail.KnDetailNotice(tc.reduction, tc.level, tc.branch)
		if key != tc.want {
			t.Errorf("%s: key = %q, want %q", tc.name, key, tc.want)
		}
		if args["Arg5"] != MaxKnDetailChars {
			t.Errorf("%s: Arg5 = %v, want the character limit", tc.name, args["Arg5"])
		}
	}
}

// What the limit is chosen against: an MCP host's 25000-token result limit, at about
// 1.4 characters per token for Chinese text. A limit raised past it brings back the
// answers hosts store to a file or cut off.
func TestMaxKnDetailCharsFitsAHostResultLimit(t *testing.T) {
	const hostTokens, charsPerToken = 25000, 1.4
	if float64(MaxKnDetailChars) > hostTokens*charsPerToken {
		t.Fatalf("MaxKnDetailChars = %d passes a %d-token host limit", MaxKnDetailChars, hostTokens)
	}
}
