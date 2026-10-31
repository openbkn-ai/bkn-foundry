// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// Tested on a shipments ontology: shipments go from stations.

func shipmentOntology() *interfaces.Draft {
	return &interfaces.Draft{Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{ID: "shipments", Name: "Shipments", Adopted: true, PrimaryKeys: []string{"waybill"},
				Properties: []*interfaces.DraftProperty{{Name: "waybill", Type: "string", MappedField: "waybill"},
					{Name: "origin", Type: "integer", MappedField: "origin"}}},
			{ID: "stations", Name: "Stations", Adopted: true, PrimaryKeys: []string{"station_id"},
				Properties: []*interfaces.DraftProperty{{Name: "station_id", Type: "integer", MappedField: "station_id"}}},
			{ID: "carriers", Name: "Carriers", Adopted: true},
			{ID: "depots", Name: "Depots"},
		},
		RelationTypes: []*interfaces.DraftRelationType{{ID: "shipments_from", Source: "shipments", Target: "stations",
			Adopted: true, Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "origin", TargetProperty: "station_id"}}}},
	}}
}

var (
	shipmentRef = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "shipments"}
	stationRef  = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "stations"}
)

// shipmentSpec has q1 on shipments and their stations, in s1, and q2 on shipments alone, in s2
// and s1.
func shipmentSpec(draft *interfaces.Draft) *interfaces.RequirementSpec {
	spec := &interfaces.RequirementSpec{
		Scenarios: []*interfaces.RequirementScenario{{ID: "s1"}, {ID: "s2"}},
		Questions: []*interfaces.RequirementQuestion{
			{ID: "q1", ScenarioIDs: []string{"s1"}, Needs: []interfaces.RequirementItemRef{shipmentRef, stationRef}},
			{ID: "q2", ScenarioIDs: []string{"s2", "s1"}, Needs: []interfaces.RequirementItemRef{shipmentRef}},
		},
	}
	signQuestions(spec.Questions, draft)
	return spec
}

func Test_OntologyChanges(t *testing.T) {
	Convey("Test marking what the ontology changed under the scenarios\n", t, func() {
		Convey("Unchanged, nothing is marked; adopted object types no question rests on are listed\n", func() {
			draft := shipmentOntology()
			spec := shipmentSpec(draft)

			uncovered := markOntologyChanges(spec, draft)

			So(spec.Questions[0].OntologyChanged || spec.Questions[1].OntologyChanged, ShouldBeFalse)
			So(spec.Scenarios[0].OntologyChanged || spec.Scenarios[1].OntologyChanged, ShouldBeFalse)
			So(uncovered, ShouldResemble, []interfaces.ReleaseItem{{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "carriers", Name: "Carriers"}})
		})

		Convey("A changed station marks the question on it and its scenarios only; a rename marks nothing\n", func() {
			draft := shipmentOntology()
			spec := shipmentSpec(draft)
			draft.Items.ObjectTypes[1].Name = "Hubs"
			draft.Items.ObjectTypes[1].Description = "Where parcels change trucks"

			markOntologyChanges(spec, draft)
			So(spec.Questions[0].OntologyChanged, ShouldBeFalse)

			draft.Items.ObjectTypes[1].Properties = append(draft.Items.ObjectTypes[1].Properties,
				&interfaces.DraftProperty{Name: "city", Type: "string", MappedField: "city"})
			markOntologyChanges(spec, draft)

			So(spec.Questions[0].OntologyChanged, ShouldBeTrue)
			// s1 is marked though its other question is unchanged.
			So(spec.Scenarios[0].OntologyChanged, ShouldBeTrue)
			So(spec.Questions[1].OntologyChanged, ShouldBeFalse)
			So(spec.Scenarios[1].OntologyChanged, ShouldBeFalse)
		})

		Convey("An item no longer adopted marks the questions on it\n", func() {
			draft := shipmentOntology()
			spec := shipmentSpec(draft)
			draft.Items.ObjectTypes[0].Adopted = false

			markOntologyChanges(spec, draft)

			So(spec.Questions[0].OntologyChanged && spec.Questions[1].OntologyChanged, ShouldBeTrue)
		})

		Convey("Without a draft, or a signature, nothing is known to change\n", func() {
			spec := shipmentSpec(shipmentOntology())
			spec.Questions[1].OntologySignature = ""

			So(markOntologyChanges(spec, nil), ShouldBeEmpty)
			So(spec.Questions[0].OntologyChanged, ShouldBeFalse)
			other := shipmentOntology()
			other.Items.ObjectTypes[0].PrimaryKeys = []string{"origin"}
			markOntologyChanges(spec, other)
			So(spec.Questions[1].OntologyChanged, ShouldBeFalse)
		})
	})
}

func Test_GetRequirementMarksOntologyChanges(t *testing.T) {
	Convey("GetRequirement marks what the ontology changed\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da}
		draft := shipmentOntology()
		spec := shipmentSpec(draft)
		changed := shipmentOntology()
		changed.Items.RelationTypes[0].Mapping = nil
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{ProjectID: "p1",
			Status: interfaces.REQUIREMENT_STATUS_DRAFTING, Spec: *spec}, true, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(changed, true, nil)

		view, err := svc.GetRequirement(context.Background(), token, "p1")

		So(err, ShouldBeNil)
		// q1 joins shipments to stations by the relation: its path changed.
		So(view.Spec.Questions[0].OntologyChanged, ShouldBeTrue)
		So(view.Spec.Scenarios[0].OntologyChanged, ShouldBeTrue)
		So(view.OntologyUncovered, ShouldHaveLength, 1)
	})
}
