// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"errors"
	"testing"

	"github.com/bytedance/sonic"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

// The Conversation and Interaction an execution was declared under are stored on its record
// and filterable (#2658).

func Test_CreateExecution_StoresBKNContextAndMapsItAsKeyword(t *testing.T) {
	Convey("a new execution is stored with its ids; an index of an older version gets the mapping", t, func() {
		ctrl := gomock.NewController(t)
		osa := omock.NewMockOpenSearchAccess(ctrl)
		svc := &actionLogsService{osAccess: osa}

		index := interfaces.GetActionExecutionIndex("kn_1")
		osa.EXPECT().IndexExists(gomock.Any(), index).Return(true, nil).Times(2)
		// The mapping is put once per index, not on every execution.
		var mapping any
		osa.EXPECT().PutMapping(gomock.Any(), index, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, body any) error { mapping = body; return nil }).Times(1)
		var stored []any
		osa.EXPECT().InsertData(gomock.Any(), index, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, data any) error { stored = append(stored, data); return nil }).Times(2)

		So(svc.CreateExecution(context.Background(), &interfaces.ActionExecution{
			ID: "e1", KNID: "kn_1", ConversationID: "conv-1", InteractionID: "int:1",
		}), ShouldBeNil)
		So(svc.CreateExecution(context.Background(), &interfaces.ActionExecution{ID: "e2", KNID: "kn_1"}), ShouldBeNil)

		props := mapping.(map[string]any)["properties"].(map[string]any)
		So(props["conversation_id"], ShouldResemble, map[string]any{"type": "keyword"})
		So(props["interaction_id"], ShouldResemble, map[string]any{"type": "keyword"})

		raw, err := sonic.Marshal(stored[0])
		So(err, ShouldBeNil)
		var doc map[string]any
		So(sonic.Unmarshal(raw, &doc), ShouldBeNil)
		So(doc["conversation_id"], ShouldEqual, "conv-1")
		So(doc["interaction_id"], ShouldEqual, "int:1")

		// An execution without ids omits both fields.
		raw, _ = sonic.Marshal(stored[1])
		doc = map[string]any{}
		So(sonic.Unmarshal(raw, &doc), ShouldBeNil)
		So(doc, ShouldNotContainKey, "conversation_id")
		So(doc, ShouldNotContainKey, "interaction_id")
	})

	Convey("a new index already maps both ids", t, func() {
		props := executionsIndexBody["mappings"].(map[string]any)["properties"].(map[string]any)
		So(props["conversation_id"], ShouldResemble, map[string]any{"type": "keyword"})
		So(props["interaction_id"], ShouldResemble, map[string]any{"type": "keyword"})
	})

	Convey("a failed mapping update does not fail the execution and is retried next time", t, func() {
		ctrl := gomock.NewController(t)
		osa := omock.NewMockOpenSearchAccess(ctrl)
		svc := &actionLogsService{osAccess: osa}
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
		osa.EXPECT().PutMapping(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("boom")).Times(2)
		osa.EXPECT().InsertData(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)

		So(svc.CreateExecution(context.Background(), &interfaces.ActionExecution{ID: "e1", KNID: "kn_1"}), ShouldBeNil)
		So(svc.CreateExecution(context.Background(), &interfaces.ActionExecution{ID: "e2", KNID: "kn_1"}), ShouldBeNil)
	})
}

func Test_QueryExecutions_FiltersByBKNContext(t *testing.T) {
	Convey("conversation_id and interaction_id are exact-match filters that combine with the others", t, func() {
		ctrl := gomock.NewController(t)
		osa := omock.NewMockOpenSearchAccess(ctrl)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)
		var query map[string]any
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				query = q.(map[string]any)
				return []interfaces.Hit{{Source: map[string]any{
					"id": "e1", "status": "completed", "conversation_id": "conv-1", "interaction_id": "int-1",
				}}}, nil
			})

		svc := &actionLogsService{osAccess: osa}
		res, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{
			KNID: "kn_1", Status: "completed", ConversationID: "conv-1", InteractionID: "int-1",
		})
		So(err, ShouldBeNil)
		So(res.Entries[0].ConversationID, ShouldEqual, "conv-1")
		So(res.Entries[0].InteractionID, ShouldEqual, "int-1")

		must := query["query"].(map[string]any)["bool"].(map[string]any)["must"].([]map[string]any)
		So(must, ShouldContain, map[string]any{"term": map[string]any{"conversation_id": "conv-1"}})
		So(must, ShouldContain, map[string]any{"term": map[string]any{"interaction_id": "int-1"}})
		So(must, ShouldContain, map[string]any{"term": map[string]any{"status": "completed"}})
	})

	Convey("without the filters the query is unchanged, and old records read back without ids", t, func() {
		ctrl := gomock.NewController(t)
		osa := omock.NewMockOpenSearchAccess(ctrl)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)
		var query map[string]any
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				query = q.(map[string]any)
				return []interfaces.Hit{{Source: map[string]any{"id": "old", "status": "completed"}}}, nil
			})
		svc := &actionLogsService{osAccess: osa}
		res, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{KNID: "kn_1"})
		So(err, ShouldBeNil)
		So(res.Entries[0].ConversationID, ShouldBeEmpty)
		must, _ := query["query"].(map[string]any)["bool"].(map[string]any)["must"].([]map[string]any)
		for _, cond := range must {
			So(cond["term"], ShouldNotContainKey, "conversation_id")
		}
	})
}
