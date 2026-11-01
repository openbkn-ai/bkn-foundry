// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_AskNewRelationType(t *testing.T) {
	Convey("Test asking AI about a relation type not made yet\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		// Asking only reads: any write of the draft fails the test.
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		svc := &draftService{setting: setting, da: da, ps: ps, va: va, ba: ba}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
			return logisticsDraft(), true, nil
		}).AnyTimes()
		va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(logisticsTables(), nil).AnyTimes()
		va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
			DoAndReturn(joinCounts(map[string][3]int64{"r_deliveries": {80, 0, 2}, "r_shipments": {100, 10, 30}})).AnyTimes()
		// read keeps the message the Agent reads and answers with the revisions given.
		var in askInput
		var message string
		read := func(revisions string, pieces ...string) {
			if len(pieces) == 0 {
				pieces = []string{"运单由配送运送。"}
			}
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					So(req.AgentID, ShouldEqual, askAgentID)
					message = req.Message
					in = askInput{}
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					return answered(`{"revisions":[`+revisions+`]}`, pieces...)(ctx, account, req, on)
				})
		}
		ask := func(working *interfaces.DraftRelationTypeSave) (*interfaces.DraftAskAnswer, error) {
			return svc.AskNewRelationType(ctx, token, "p1",
				interfaces.DraftRelationAskRequest{Question: "它怎么连？", Working: working}, caller, nil)
		}
		endsOf := func() []string {
			ids := []string{}
			for _, ot := range in.Ends {
				ids = append(ids, ot.ID)
			}
			return ids
		}

		Convey("With one end, the ends its candidates join are offered with the candidates, and the other end it gives is checked as the save would\n", func() {
			read(`{"field":"name","value":"配送的运单","reason":"按业务含义命名"},
				{"field":"ends","source_object_type_id":"deliveries","target_object_type_id":"shipments","mapping":[{"source_property":"waybill","target_property":"waybill"}],"reason":"配送记录运单号"}`)

			got, err := ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "shipments"})

			So(err, ShouldBeNil)
			So(in.Kind, ShouldEqual, askKindRelationType)
			So(in.RelationType.ID, ShouldBeEmpty)
			So(in.RelationType.Source, ShouldEqual, "shipments")
			// The candidates' ends first, then the other adopted object types.
			So(endsOf(), ShouldResemble, []string{"shipments", "stations", "deliveries", "carriers", "routes"})
			// The ends given and the candidates' ends whole; the others brief.
			So(message, ShouldContainSubstring, `{"id":"routes","name":"Routes","identity":["route_id"],"property_names":["route_id","from_station"]}`)
			So(in.Ends[1].Table, ShouldEqual, "stations")
			So(len(in.Candidates), ShouldEqual, 3)
			So(*in.Candidates[1].HitRate, ShouldEqual, 78.0/80)
			So(got.Answer, ShouldEqual, "运单由配送运送。")
			So(len(got.Revisions), ShouldEqual, 2)
			ends := got.Revisions[1].Value.(interfaces.DraftAskEndsRevision)
			So(ends.SourceObjectTypeID+">"+ends.TargetObjectTypeID, ShouldEqual, "deliveries>shipments")
			So(ends.MappingRules[0].TargetProperty.Name, ShouldEqual, "waybill")
		})

		Convey("Ends the save refuses are dropped with why: a repeat of a drafted relation, an unknown property, no end\n", func() {
			read(`{"field":"ends","source_object_type_id":"shipments","target_object_type_id":"stations","mapping":[{"source_property":"origin_code","target_property":"station_code"}],"reason":"x"}`)
			got, err := ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "shipments", TargetObjectTypeID: "stations"})
			So(err, ShouldBeNil)
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{{Field: interfaces.DRAFT_ASK_REVISION_ENDS, Reason: relationDuplicate}})
			// Both ends given: only those two, and the candidates joining them.
			So(endsOf(), ShouldResemble, []string{"shipments", "stations"})
			So(len(in.Candidates), ShouldEqual, 1)

			read(`{"field":"ends","source_object_type_id":"shipments","target_object_type_id":"routes","mapping":[{"source_property":"ghost","target_property":"route_id"}],"reason":"x"}`)
			got, err = ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "shipments"})
			So(err, ShouldBeNil)
			So(got.Dropped[0].Reason, ShouldEqual, relationUnknownProperty)

			read(`{"field":"ends","target_object_type_id":"routes","mapping":[{"source_property":"route_id","target_property":"route_id"}],"reason":"x"}`)
			got, err = ask(nil)
			So(err, ShouldBeNil)
			So(got.Dropped[0].Reason, ShouldEqual, relationUnknownEnd)
		})

		Convey("With no end, every adopted object type is offered and nothing is counted\n", func() {
			read(`{"field":"ends","source_object_type_id":"deliveries","target_object_type_id":"routes","mapping":[{"source_property":"route_id","target_property":"route_id"}],"reason":"x"}`)

			got, err := ask(&interfaces.DraftRelationTypeSave{Name: " 配送路线 "})

			So(err, ShouldBeNil)
			So(in.RelationType.Name, ShouldEqual, "配送路线")
			So(endsOf(), ShouldResemble, []string{"shipments", "stations", "routes", "deliveries", "carriers"})
			So(in.Candidates, ShouldBeEmpty)
			// The same join as an adopted relation is a repeat too.
			So(got.Dropped[0].Reason, ShouldEqual, relationDuplicate)
		})

		Convey("Ends the form already has are no revision\n", func() {
			read(`{"field":"ends","source_object_type_id":"deliveries","target_object_type_id":"shipments","mapping":[{"source_property":"waybill","target_property":"waybill"}],"reason":"x"}`)

			got, err := ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "deliveries", TargetObjectTypeID: "shipments",
				Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: json.RawMessage(`[{"source_property":{"name":"waybill"},"target_property":{"name":"waybill"}}]`)})

			So(err, ShouldBeNil)
			So(in.RelationType.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "waybill", TargetProperty: "waybill"}})
			So(got.Dropped[0].Reason, ShouldEqual, askDroppedUnchanged)
		})

		Convey("The ends chosen are told apart, and ends that leave one of them out are dropped\n", func() {
			read(`{"field":"ends","source_object_type_id":"routes","target_object_type_id":"stations","mapping":[{"source_property":"from_station","target_property":"station_code"}],"reason":"x"}`)
			got, err := ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "shipments"})
			So(err, ShouldBeNil)
			So(in.ChosenEnds, ShouldResemble, []askChosenEnd{{ID: "shipments", Name: "Shipments", Role: askChosenSource}})
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{{Field: interfaces.DRAFT_ASK_REVISION_ENDS, Reason: askDroppedNotFromGivenEnd}})

			// The chosen end as the target is from it too.
			read(`{"field":"ends","source_object_type_id":"deliveries","mapping":[{"source_property":"waybill","target_property":"waybill"}],"reason":"x"}`)
			got, err = ask(&interfaces.DraftRelationTypeSave{TargetObjectTypeID: "shipments"})
			So(err, ShouldBeNil)
			So(in.ChosenEnds[0].Role, ShouldEqual, askChosenTarget)
			So(len(got.Revisions), ShouldEqual, 1)

			// With both chosen, both stay.
			read(`{"field":"ends","source_object_type_id":"deliveries","target_object_type_id":"shipments","mapping":[{"source_property":"waybill","target_property":"waybill"}],"reason":"x"}`)
			got, err = ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "shipments", TargetObjectTypeID: "stations"})
			So(err, ShouldBeNil)
			So(len(in.ChosenEnds), ShouldEqual, 2)
			So(got.Dropped[0].Reason, ShouldEqual, askDroppedNotFromGivenEnd)

			// With none chosen, there is nothing to keep.
			read(`{"field":"ends","source_object_type_id":"deliveries","target_object_type_id":"shipments","mapping":[{"source_property":"waybill","target_property":"waybill"}],"reason":"x"}`)
			got, err = ask(nil)
			So(err, ShouldBeNil)
			So(in.ChosenEnds, ShouldBeEmpty)
			So(len(got.Revisions), ShouldEqual, 1)
		})

		Convey("Revisions given with no answer written are still given\n", func() {
			read(`{"field":"name","value":"配送的运单","reason":"按业务含义命名"}`, "")

			got, err := ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "shipments"})

			So(err, ShouldBeNil)
			So(got.Answer, ShouldBeEmpty)
			So(got.Revisions[0].Value, ShouldEqual, "配送的运单")
		})

		Convey("An unknown end in the form is left out; a bad question is refused before asking\n", func() {
			read(``)
			_, err := ask(&interfaces.DraftRelationTypeSave{SourceObjectTypeID: "ghost", TargetObjectTypeID: "stations"})
			So(err, ShouldBeNil)
			So(in.RelationType.Source, ShouldBeEmpty)
			So(in.Ends[0].ID, ShouldEqual, "stations")

			_, err = svc.AskNewRelationType(ctx, token, "p1", interfaces.DraftRelationAskRequest{Question: " "}, caller, nil)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidQuestion)
		})
	})
}
