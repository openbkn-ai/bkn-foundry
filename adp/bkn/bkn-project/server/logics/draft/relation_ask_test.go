// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_AskRelationSuggestions(t *testing.T) {
	Convey("Test asking AI how an unconnected object type connects\n", t, func() {
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
			DoAndReturn(joinCounts(map[string][3]int64{"r_deliveries": {80, 0, 2}, "r_shipments": {100, 10, 30}, "r_routes": {30, 0, 6}})).AnyTimes()
		ask := func() (*interfaces.DraftRelationSuggestions, error) {
			return svc.AskRelationSuggestions(ctx, token, "p1", "shipments", caller)
		}

		Convey("It reads the object type, the other adopted ones and the candidates, by index\n", func() {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					So(req.AgentID, ShouldEqual, askAgentID)
					schema, _ := json.Marshal(req.ResponseFormat)
					So(string(schema), ShouldContainSubstring, `"proposals"`)
					var in relationSuggestionsInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(in.Kind, ShouldEqual, askKindRelationSuggestions)
					So(in.ObjectType.ID, ShouldEqual, "shipments")
					// The adopted others only: depots are not adopted.
					So(len(in.Others), ShouldEqual, 4)
					So(in.Candidates[0].Origin, ShouldEqual, interfaces.DRAFT_RELATION_CANDIDATE_DRAFTED)
					So(*in.Candidates[1].HitRate, ShouldEqual, 78.0/80)
					So(in.Candidates[2].HitRate, ShouldBeNil)
					return answered(`{"candidates":[],"proposals":[]}`, "运单通过运单号与配送关联。")(ctx, account, req, on)
				})

			got, err := ask()

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "运单通过运单号与配送关联。")
			So(len(got.Candidates), ShouldEqual, 3)
		})

		Convey("JSON it writes after the answer is not part of the answer\n", func() {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(answered(
				`{"candidates":[],"proposals":[]}`, "看不出可以建立的关系。\n\n{", `"candidates":[],"proposals":[]}`))

			got, err := ask()

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "看不出可以建立的关系。")
		})

		Convey("Its names and reasons go to the candidates they name; a name the save would refuse is not taken\n", func() {
			long := strings.Repeat("名", interfaces.DRAFT_NAME_MAX_LENGTH+1)
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(answered(`{"candidates":[
				{"index":1,"name":" 配送的运单 ","reason":" 每次配送运送一张运单 "},
				{"index":2,"name":"`+long+`","reason":"承运商在另一个目录"},
				{"index":9,"name":"不存在","reason":"x"}],"proposals":[]}`, "好"))

			got, err := ask()

			So(err, ShouldBeNil)
			So(got.Candidates[1].Name, ShouldEqual, "配送的运单")
			So(got.Candidates[1].Reason, ShouldEqual, "每次配送运送一张运单")
			So(got.Candidates[2].Name, ShouldNotEqual, long)
			So(got.Candidates[2].Reason, ShouldEqual, "承运商在另一个目录")
			So(got.Candidates[0].Reason, ShouldBeEmpty)
		})

		Convey("A relation it proposes is kept when the save would define it, counted; others are left out\n", func() {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(answered(`{"candidates":[],"proposals":[
				{"source_object_type_id":"routes","target_object_type_id":"shipments","mapping":[{"source_property":"route_id","target_property":"waybill"}],"name":"路线的运单","reason":"路线承运运单"},
				{"source_object_type_id":"deliveries","target_object_type_id":"shipments","mapping":[{"source_property":"waybill","target_property":"waybill"}],"name":"重复","reason":"已在候选中"},
				{"source_object_type_id":"deliveries","target_object_type_id":"stations","mapping":[{"source_property":"route_id","target_property":"station_code"}],"name":"无关","reason":"不涉及运单"}]}`, "好"))

			got, err := ask()

			So(err, ShouldBeNil)
			So(len(got.Candidates), ShouldEqual, 4)
			proposal := got.Candidates[3]
			So(proposal.Origin, ShouldEqual, interfaces.DRAFT_RELATION_CANDIDATE_AI)
			So(proposal.SourceObjectTypeID+">"+proposal.TargetObjectTypeID, ShouldEqual, "routes>shipments")
			So(proposal.Name, ShouldEqual, "路线的运单")
			So(proposal.Reason, ShouldEqual, "路线承运运单")
			// Counted as a saved relation is.
			So(proposal.Evidence.Computed, ShouldBeTrue)
			So(proposal.Evidence.HitRate, ShouldEqual, 0.8)
		})

		Convey("A proposal with an unknown property, an end not adopted, or no name is left out\n", func() {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(answered(`{"candidates":[],"proposals":[
				{"source_object_type_id":"shipments","target_object_type_id":"stations","mapping":[{"source_property":"ghost","target_property":"station_code"}],"name":"a","reason":"x"},
				{"source_object_type_id":"shipments","target_object_type_id":"depots","mapping":[{"source_property":"origin_code","target_property":"id"}],"name":"b","reason":"x"},
				{"source_object_type_id":"shipments","target_object_type_id":"routes","mapping":[{"source_property":"origin_code","target_property":"route_id"}],"name":" ","reason":"x"},
				{"source_object_type_id":"shipments","target_object_type_id":"routes","mapping":[],"name":"c","reason":"x"}]}`, "好"))

			got, err := ask()

			So(err, ShouldBeNil)
			So(len(got.Candidates), ShouldEqual, 3)
		})

		Convey("An Agent that cannot answer is unavailable, an unadopted object type refused before it is asked\n", func() {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).Return(
				&interfaces.BknAgentError{HTTPCode: http.StatusNotFound, Code: "BknAgent.NotFound"})
			_, err := ask()
			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Draft_AskUnavailable)

			_, err = svc.AskRelationSuggestions(ctx, token, "p1", "depots", caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidItem)
		})
	})
}
