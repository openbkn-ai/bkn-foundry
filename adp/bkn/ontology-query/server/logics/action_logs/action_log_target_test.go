// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func mustOf(query any) []map[string]any {
	return query.(map[string]any)["query"].(map[string]any)["bool"].(map[string]any)["must"].([]map[string]any)
}

func Test_QueryExecutions_FiltersByObjectTypeAndTarget(t *testing.T) {
	Convey("object_type_id is a term on the execution", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1")).Return(true, nil)
		var searched any
		osa.EXPECT().SearchData(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1"), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				searched = q
				return nil, nil
			})
		_, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{KNID: "kn_1", ObjectTypeID: "purchase_order"})
		So(err, ShouldBeNil)
		So(mustOf(searched), ShouldContain, map[string]any{"term": map[string]any{"object_type_id": "purchase_order"}})
	})

	Convey("target_key finds the executions through their results, then filters the executions by id", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1")).Return(true, nil)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(true, nil)
		var resultsQuery map[string]any
		var executionsQuery any
		gomock.InOrder(
			osa.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
					resultsQuery = q.(map[string]any)
					return []interfaces.Hit{
						{Source: map[string]any{"execution_id": "e3"}},
						{Source: map[string]any{"execution_id": "e1"}},
						{Source: map[string]any{"execution_id": "e1"}},
					}, nil
				}),
			osa.EXPECT().SearchData(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1"), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
					executionsQuery = q
					return []interfaces.Hit{{Source: map[string]any{"id": "e3", "status": "completed"}}}, nil
				}),
		)
		list, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{
			KNID: "kn_1", ObjectTypeID: "purchase_order", TargetKey: "9007199254740993",
		})
		So(err, ShouldBeNil)
		So(len(list.Entries), ShouldEqual, 1)

		filter := resultsQuery["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]map[string]any)
		So(filter, ShouldContain, map[string]any{"term": map[string]any{"kn_id": "kn_1"}})
		So(filter, ShouldContain, map[string]any{"term": map[string]any{"target_keys": "9007199254740993"}})
		So(resultsQuery["collapse"], ShouldResemble, map[string]any{"field": "execution_id"})
		So(resultsQuery["size"], ShouldEqual, MaxTargetKeyExecutions)
		So(mustOf(executionsQuery), ShouldContain, map[string]any{"terms": map[string]any{"id": []string{"e3", "e1"}}})
	})

	Convey("a target no result covers has no executions", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1")).Return(true, nil)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).Return(nil, nil)
		list, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{
			KNID: "kn_1", ObjectTypeID: "purchase_order", TargetKey: "PO-404", NeedTotal: true,
		})
		So(err, ShouldBeNil)
		So(list.Entries, ShouldBeEmpty)
		So(*list.TotalCount, ShouldEqual, 0)
	})

	Convey("before any result was stored, no execution has a target", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1")).Return(true, nil)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(false, nil)
		list, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{
			KNID: "kn_1", ObjectTypeID: "purchase_order", TargetKey: "1",
		})
		So(err, ShouldBeNil)
		So(list.Entries, ShouldBeEmpty)
	})
}

func Test_AppendResults_StoresTargetKeys(t *testing.T) {
	Convey("a result lists the keys of its instance and of its targets, each once", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(false, nil)
		osa.EXPECT().CreateIndex(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, body any) error {
				properties := body.(map[string]any)["mappings"].(map[string]any)["properties"].(map[string]any)
				So(properties["target_keys"], ShouldResemble, map[string]any{"type": "keyword"})
				return nil
			})
		// A new index has the whole mapping: no PutMapping.
		var docs []interfaces.BulkDocument
		osa.EXPECT().BulkIndexDocuments(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, batch []interfaces.BulkDocument) error {
				docs = batch
				return nil
			})
		err := svc.AppendResults(context.Background(), "kn_1", "exec_1", 0, []interfaces.ObjectExecutionResult{
			{ObjectSystemInfo: interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{"po_id": json.Number("7")}}},
			{
				ObjectSystemInfo: interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{}},
				Targets: []interfaces.ObjectSystemInfo{
					{InstanceIdentity: map[string]any{"po_id": json.Number("1")}},
					{InstanceIdentity: map[string]any{"po_id": json.Number("2")}},
					{InstanceIdentity: map[string]any{"po_id": json.Number("1")}},
					{InstanceIdentity: map[string]any{"order": "PO-1", "line": json.Number("2")}},
				},
			},
			{},
		})
		So(err, ShouldBeNil)
		So(docs[0].Body.(storedExecutionResult).TargetKeys, ShouldResemble, []string{"7"})
		So(docs[1].Body.(storedExecutionResult).TargetKeys, ShouldResemble, []string{"1", "2", `{"line":2,"order":"PO-1"}`})
		So(docs[2].Body.(storedExecutionResult).TargetKeys, ShouldBeNil)
		encoded, _ := json.Marshal(docs[2].Body)
		So(string(encoded), ShouldNotContainSubstring, "target_keys")
	})

	Convey("an existing results index gets the target key mapping once", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(true, nil)
		osa.EXPECT().PutMapping(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, body any) error {
				So(body.(map[string]any)["properties"], ShouldContainKey, "target_keys")
				return nil
			})
		osa.EXPECT().BulkIndexDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
		So(svc.AppendResults(context.Background(), "kn_1", "exec_1", 0, resultsOf(1, "success")), ShouldBeNil)
		So(svc.AppendResults(context.Background(), "kn_1", "exec_1", 1, resultsOf(1, "success")), ShouldBeNil)
	})

	Convey("a mapping that cannot be added does not fail the write, and waits before it is tried again", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(true, nil)
		osa.EXPECT().PutMapping(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).Return(errors.New("forbidden"))
		osa.EXPECT().BulkIndexDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
		So(svc.AppendResults(context.Background(), "kn_1", "exec_1", 0, resultsOf(1, "success")), ShouldBeNil)
		So(svc.AppendResults(context.Background(), "kn_1", "exec_1", 1, resultsOf(1, "success")), ShouldBeNil)
	})
}

func Test_FinishExecution_WritesTheTargetCountOnlyWhenKnown(t *testing.T) {
	Convey("a record-event check writes how many targets it selected", t, func() {
		svc, osa := newWriteTestService(t)
		var bodies []map[string]any
		osa.EXPECT().UpdateData(gomock.Any(), interfaces.GetActionExecutionIndex("kn_1"), "exec_1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, body any) (any, error) {
				bodies = append(bodies, body.(map[string]any))
				return nil, nil
			}).Times(2)
		targets := 3
		So(svc.FinishExecution(context.Background(), "kn_1", "exec_1", &interfaces.ExecutionOutcome{
			Status: interfaces.ExecutionStatusCompleted, SuccessCount: 1, TargetCount: &targets,
		}), ShouldBeNil)
		So(svc.FinishExecution(context.Background(), "kn_1", "exec_1", &interfaces.ExecutionOutcome{
			Status: interfaces.ExecutionStatusCompleted, SuccessCount: 1,
		}), ShouldBeNil)
		params := func(body map[string]any) map[string]any {
			return body["script"].(map[string]any)["params"].(map[string]any)
		}
		So(params(bodies[0])["target_count"], ShouldEqual, 3)
		So(params(bodies[1]), ShouldNotContainKey, "target_count")
		So(bodies[0]["script"].(map[string]any)["source"], ShouldContainSubstring, "params.target_count != null")
	})
}
