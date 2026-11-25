// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
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
		So(props["governance_request_id"], ShouldResemble, governanceRequestMapping)

		raw, err := jsoncodec.Marshal(stored[0])
		So(err, ShouldBeNil)
		var doc map[string]any
		So(jsoncodec.Unmarshal(raw, &doc), ShouldBeNil)
		So(doc["conversation_id"], ShouldEqual, "conv-1")
		So(doc["interaction_id"], ShouldEqual, "int:1")
		for _, key := range paginationMetadataKeys {
			So(doc, ShouldNotContainKey, key)
		}
		So(doc, ShouldNotContainKey, "end_time")
		So(doc, ShouldNotContainKey, "duration_ms")

		// An execution without ids omits both fields.
		raw, _ = jsoncodec.Marshal(stored[1])
		doc = map[string]any{}
		So(jsoncodec.Unmarshal(raw, &doc), ShouldBeNil)
		So(doc, ShouldNotContainKey, "conversation_id")
		So(doc, ShouldNotContainKey, "interaction_id")
	})

	Convey("a new index already maps both ids", t, func() {
		props := executionsIndexBody["mappings"].(map[string]any)["properties"].(map[string]any)
		So(props["conversation_id"], ShouldResemble, map[string]any{"type": "keyword"})
		So(props["interaction_id"], ShouldResemble, map[string]any{"type": "keyword"})
		So(props["governance_request_id"], ShouldResemble, governanceRequestMapping)
	})

	Convey("a failed mapping update does not fail the execution and is not retried within the backoff window", t, func() {
		ctrl := gomock.NewController(t)
		osa := omock.NewMockOpenSearchAccess(ctrl)
		clock := time.Now()
		svc := &actionLogsService{osAccess: osa, now: func() time.Time { return clock }}
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil).Times(5)
		// One attempt in the first window, one after it expired; a second index has its own window.
		osa.EXPECT().PutMapping(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1"), gomock.Any()).
			Return(errors.New("boom")).Times(2)
		osa.EXPECT().PutMapping(gomock.Any(), interfaces.GetActionExecutionIndex("kn_2"), gomock.Any()).
			Return(nil).Times(1)
		osa.EXPECT().InsertData(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(5)

		create := func(id, kn string) {
			So(svc.CreateExecution(context.Background(), &interfaces.ActionExecution{ID: id, KNID: kn}), ShouldBeNil)
		}
		create("e1", "kn_1")
		clock = clock.Add(mappingRetryBackoff - time.Second)
		create("e2", "kn_1") // inside the window: no PutMapping
		create("e3", "kn_2") // other index: its own attempt
		clock = clock.Add(2 * time.Second)
		create("e4", "kn_1") // window over: retried (and fails again)
		create("e5", "kn_1") // new window: no PutMapping
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
					"governance_request_id": "request-1:foo",
				}}}, nil
			})

		svc := &actionLogsService{osAccess: osa}
		res, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{
			KNID: "kn_1", Status: "completed", ConversationID: "conv-1", InteractionID: "int-1",
			GovernanceRequestID: "request-1:foo",
		})
		So(err, ShouldBeNil)
		So(res.Entries[0].ConversationID, ShouldEqual, "conv-1")
		So(res.Entries[0].InteractionID, ShouldEqual, "int-1")
		So(res.Entries[0].GovernanceRequestID, ShouldEqual, "request-1:foo")

		must := query["query"].(map[string]any)["bool"].(map[string]any)["must"].([]map[string]any)
		So(must, ShouldContain, map[string]any{"term": map[string]any{"conversation_id": "conv-1"}})
		So(must, ShouldContain, map[string]any{"term": map[string]any{"interaction_id": "int-1"}})
		So(must, ShouldContain, map[string]any{"term": map[string]any{"status": "completed"}})
		So(must, ShouldContain, map[string]any{"term": map[string]any{"governance_request_id.keyword": "request-1:foo"}})
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
			So(cond["term"], ShouldNotContainKey, "governance_request_id.keyword")
		}
	})
}
