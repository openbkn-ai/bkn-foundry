// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// legacySpec is a specification drafted before kinetic needs: capabilities on its scenarios and
// metric, function and skill names in its questions' basis.
func legacySpec() *RequirementSpec {
	return &RequirementSpec{
		Scenarios: []*RequirementScenario{
			{ID: "s1", Capabilities: []RequirementCapability{
				{Kind: LOGIC_NEED_METRIC, Name: "Late rate", Definition: "late over all"},
				{Kind: "dashboard", Name: "Late board"},
				{Kind: LOGIC_NEED_SKILL, Name: " "},
			}},
			{ID: "s2", Capabilities: []RequirementCapability{{Kind: LOGIC_NEED_METRIC, Name: "late RATE", Definition: "other"}}},
		},
		Questions: []*RequirementQuestion{
			{ID: "q1", ScenarioIDs: []string{"s3"}, Basis: RequirementBasis{ConceptIDs: []string{"c1"},
				Metrics: []string{"Late rate", "Late rate"}, Functions: []string{"window check"}, Skills: []string{"Signed change"}}},
			{ID: "q2", Basis: RequirementBasis{Functions: []string{"Window Check"}}},
			{ID: "q3"},
		},
		LastIDs: map[string]int{"q": 3},
	}
}

func TestNormalizeLogicNeeds(t *testing.T) {
	Convey("Test turning a specification drafted before kinetic needs into needs\n", t, func() {
		Convey("Capabilities come first, then the questions' names, merged by kind and name\n", func() {
			spec := legacySpec()

			NormalizeLogicNeeds(spec)

			got := []string{}
			for _, n := range spec.LogicNeeds {
				got = append(got, n.ID+" "+n.Kind+" "+n.Name)
			}
			So(got, ShouldResemble, []string{"n1 metric Late rate", "n2 app Late board", "n3 function window check",
				"n4 skill Signed change"})
			late := spec.LogicNeeds[0]
			// The first definition stands; it serves the scenarios that named it, and those of the
			// questions resting on it.
			So(late.Definition, ShouldEqual, "late over all")
			So(late.ScenarioIDs, ShouldResemble, []string{"s1", "s2", "s3"})
			So(late.Source, ShouldEqual, LOGIC_NEED_FROM_SCENARIOS)
			So(late.ObjectTypeIDs, ShouldResemble, []string{})
			So(spec.LogicNeeds[2].ScenarioIDs, ShouldResemble, []string{"s3"})

			So(spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{"n1", "n3", "n4"})
			So(spec.Questions[1].LogicNeedIDs, ShouldResemble, []string{"n3"})
			So(spec.Questions[2].LogicNeedIDs, ShouldResemble, []string{})
			So(spec.Questions[0].Basis, ShouldResemble, RequirementBasis{ConceptIDs: []string{"c1"}})
			So(spec.Scenarios[0].Capabilities, ShouldBeNil)
			So(spec.LastIDs, ShouldResemble, map[string]int{"q": 3, "n": 4})
		})

		Convey("Read again it gives the same needs, and a specification with needs is left as it is\n", func() {
			spec := legacySpec()
			NormalizeLogicNeeds(spec)
			once, _ := json.Marshal(spec)
			NormalizeLogicNeeds(spec)
			twice, _ := json.Marshal(spec)
			So(string(twice), ShouldEqual, string(once))

			// Written and read back, as the store does, it is the same too.
			read := &RequirementSpec{}
			So(json.Unmarshal(once, read), ShouldBeNil)
			NormalizeLogicNeeds(read)
			again, _ := json.Marshal(read)
			So(string(again), ShouldEqual, string(once))

			withNeeds := &RequirementSpec{LogicNeeds: []*RequirementLogicNeed{},
				Questions: []*RequirementQuestion{{ID: "q1", Basis: RequirementBasis{Metrics: []string{"m"}}}}}
			NormalizeLogicNeeds(withNeeds)
			So(withNeeds.LogicNeeds, ShouldBeEmpty)
			So(withNeeds.Questions[0].Basis.Metrics, ShouldResemble, []string{"m"})
		})

		Convey("A specification with nothing to turn gets an empty list and no id recorded\n", func() {
			spec := &RequirementSpec{Questions: []*RequirementQuestion{{ID: "q1", LogicNeedIDs: []string{"n7"}}}}

			NormalizeLogicNeeds(spec)

			So(spec.LogicNeeds, ShouldNotBeNil)
			So(spec.LogicNeeds, ShouldBeEmpty)
			So(spec.LastIDs, ShouldBeNil)
			// Ids a question already has stay.
			So(spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{"n7"})
		})

		Convey("A recorded id higher than the needs numbered is kept\n", func() {
			spec := legacySpec()
			spec.LastIDs["n"] = 9

			NormalizeLogicNeeds(spec)

			So(spec.LastIDs["n"], ShouldEqual, 9)
		})
	})
}

func TestRunningRun(t *testing.T) {
	Convey("A run goes while it is running and started after staleBefore\n", t, func() {
		spec := &RequirementSpec{Runs: map[string]*RequirementRun{
			REQUIREMENT_RUN_CONCEPTS: {Status: REQUIREMENT_RUN_READY, StartTime: 10},
			REQUIREMENT_RUN_KINETIC:  {Status: REQUIREMENT_RUN_RUNNING, StartTime: 5},
		}}

		So(spec.RunningRun(5), ShouldEqual, REQUIREMENT_RUN_KINETIC)
		So(spec.RunningRun(6), ShouldEqual, "")
		So((&RequirementSpec{}).RunningRun(0), ShouldEqual, "")
		var none *RequirementRun
		So(none.Running(0), ShouldBeFalse)
	})
}
