// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
)

func resultsOf(n int, status string) []interfaces.ObjectExecutionResult {
	results := make([]interfaces.ObjectExecutionResult, 0, n)
	for i := 0; i < n; i++ {
		results = append(results, interfaces.ObjectExecutionResult{
			ObjectSystemInfo: interfaces.ObjectSystemInfo{InstanceID: fmt.Sprintf("i_%d", i)},
			Status:           status,
			Result:           map[string]any{"n": i},
		})
	}
	return results
}

func Test_AppendResults_StoresOneDocumentPerResult(t *testing.T) {
	Convey("results are bulk-indexed under {execution}_{seq} ids with their execution and position", t, func() {
		svc, osa := newWriteTestService(t)
		gomock.InOrder(
			osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(false, nil),
			osa.EXPECT().CreateIndex(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, body any) error {
					mappings := body.(map[string]any)["mappings"].(map[string]any)
					// Tool output is never mapped: only filter and sort fields are indexed.
					So(mappings["dynamic"], ShouldEqual, false)
					So(mappings["properties"], ShouldContainKey, "execution_id")
					So(mappings["properties"], ShouldContainKey, "seq")
					So(mappings["properties"], ShouldNotContainKey, "result")
					return nil
				}),
		)

		var batches [][]interfaces.BulkDocument
		osa.EXPECT().BulkIndexDocuments(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, docs []interfaces.BulkDocument) error {
				batches = append(batches, docs)
				return nil
			}).Times(3)

		err := svc.AppendResults(context.Background(), "kn_1", "exec_1", 100, resultsOf(1201, interfaces.ObjectStatusSuccess))
		So(err, ShouldBeNil)

		So(len(batches), ShouldEqual, 3)
		So(len(batches[0]), ShouldEqual, 500)
		So(len(batches[1]), ShouldEqual, 500)
		So(len(batches[2]), ShouldEqual, 201)
		So(batches[0][0].ID, ShouldEqual, "exec_1_100")
		So(batches[2][200].ID, ShouldEqual, "exec_1_1300")

		first := batches[0][0].Body.(storedExecutionResult)
		So(first.KNID, ShouldEqual, "kn_1")
		So(first.ExecutionID, ShouldEqual, "exec_1")
		So(first.Seq, ShouldEqual, 100)
		So(first.Status, ShouldEqual, interfaces.ObjectStatusSuccess)
		So(first.InstanceID, ShouldEqual, "i_0")

		// The index is known to exist from now on: no further existence checks.
		osa.EXPECT().BulkIndexDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		So(svc.AppendResults(context.Background(), "kn_1", "exec_1", 1301, resultsOf(1, "failed")), ShouldBeNil)
	})

	Convey("an empty batch writes nothing", t, func() {
		svc, _ := newWriteTestService(t)
		So(svc.AppendResults(context.Background(), "kn_1", "exec_1", 0, nil), ShouldBeNil)
	})
}

func Test_QueryResults_PagesInOpenSearch(t *testing.T) {
	Convey("results of a post-#790 execution are filtered, sorted and paged by OpenSearch", t, func() {
		svc, osa := newWriteTestService(t)
		svc.resultsIndexReady.Store(true)

		osa.EXPECT().IndexExists(gomock.Any(), "ontology_action_executions_kn_1").Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), "ontology_action_executions_kn_1", gomock.Any()).
			Return([]interfaces.Hit{{Source: map[string]any{"id": "exec_1", "status": "completed", "results": []any{}}}}, nil)

		wantQuery := map[string]any{"bool": map[string]any{"filter": []map[string]any{
			{"term": map[string]any{"kn_id": "kn_1"}},
			{"term": map[string]any{"execution_id": "exec_1"}},
			{"term": map[string]any{"status": "failed"}},
		}}}
		osa.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				query := q.(map[string]any)
				So(query["query"], ShouldResemble, wantQuery)
				So(query["sort"], ShouldResemble, []map[string]any{{"seq": map[string]any{"order": "asc"}}})
				So(query["from"], ShouldEqual, 200)
				So(query["size"], ShouldEqual, 50)
				return []interfaces.Hit{{Source: map[string]any{
					"kn_id": "kn_1", "execution_id": "exec_1", "seq": 250,
					"_instance_id": "i_250", "status": "failed", "error_message": "boom",
				}}}, nil
			})
		osa.EXPECT().Count(gomock.Any(), interfaces.ActionExecutionResultsIndex, map[string]any{"query": wantQuery}).
			Return([]byte(`{"count": 251}`), nil)

		page, err := svc.QueryResults(context.Background(), &interfaces.ActionResultsQuery{
			KNID: "kn_1", LogID: "exec_1", Offset: 200, Limit: 50, Status: "failed",
		})
		So(err, ShouldBeNil)
		So(page.TotalCount, ShouldEqual, 251)
		So(len(page.Entries), ShouldEqual, 1)
		So(page.Entries[0].InstanceID, ShouldEqual, "i_250")
		So(page.Entries[0].ErrorMessage, ShouldEqual, "boom")
	})
}

func Test_QueryResults_LegacyEmbeddedResults(t *testing.T) {
	Convey("an execution recorded before #790 is paged from its embedded results, not the results index", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), "ontology_action_executions_kn_1").Return(true, nil)
		legacy := make([]any, 0, 30)
		for i := 0; i < 30; i++ {
			status := "success"
			if i%3 == 0 {
				status = "failed"
			}
			legacy = append(legacy, map[string]any{"_instance_id": fmt.Sprintf("i_%d", i), "status": status})
		}
		osa.EXPECT().SearchData(gomock.Any(), "ontology_action_executions_kn_1", gomock.Any()).
			Return([]interfaces.Hit{{Source: map[string]any{"id": "exec_1", "status": "completed", "results": legacy}}}, nil)

		page, err := svc.QueryResults(context.Background(), &interfaces.ActionResultsQuery{
			KNID: "kn_1", LogID: "exec_1", Offset: 2, Limit: 3, Status: "failed",
		})
		So(err, ShouldBeNil)
		So(page.TotalCount, ShouldEqual, 10)
		So(len(page.Entries), ShouldEqual, 3)
		So(page.Entries[0].InstanceID, ShouldEqual, "i_6")
	})
}

func Test_QueryResults_NoResultsIndexYet(t *testing.T) {
	Convey("before any execution stored results, the missing results index reads as no results", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), "ontology_action_executions_kn_1").Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]interfaces.Hit{{Source: map[string]any{"id": "exec_1", "status": "pending"}}}, nil)
		osa.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(false, nil)

		page, err := svc.QueryResults(context.Background(), &interfaces.ActionResultsQuery{KNID: "kn_1", LogID: "exec_1", Limit: 100})
		So(err, ShouldBeNil)
		So(page.TotalCount, ShouldEqual, 0)
		So(page.Entries, ShouldBeEmpty)
	})
}

func Test_QueryResults_RejectsPageBeyondWindow(t *testing.T) {
	Convey("a page ending beyond the OpenSearch result window is rejected before querying", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]interfaces.Hit{{Source: map[string]any{"id": "exec_1"}}}, nil)

		_, err := svc.QueryResults(context.Background(), &interfaces.ActionResultsQuery{
			KNID: "kn_1", LogID: "exec_1", Offset: 9950, Limit: 100,
		})
		So(errors.Is(err, interfaces.ErrActionResultsWindowExceeded), ShouldBeTrue)
	})
}

func Test_GetExecution_ReadsResultsPageFromIndex(t *testing.T) {
	Convey("the detail API reads one page from the results index and reports the real total", t, func() {
		svc, osa := newWriteTestService(t)
		svc.resultsIndexReady.Store(true)
		osa.EXPECT().IndexExists(gomock.Any(), "ontology_action_executions_kn_1").Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), "ontology_action_executions_kn_1", gomock.Any()).
			Return([]interfaces.Hit{{Source: map[string]any{"id": "exec_1", "status": "completed", "total_count": float64(8808)}}}, nil)
		osa.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			Return([]interfaces.Hit{{Source: map[string]any{"execution_id": "exec_1", "seq": 0, "status": "success"}}}, nil)
		osa.EXPECT().Count(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).
			Return([]byte(`{"count": 8808}`), nil)

		exec, err := svc.GetExecution(context.Background(), &interfaces.ActionLogDetailQuery{
			KNID: "kn_1", LogID: "exec_1", ResultsLimit: 10,
		})
		So(err, ShouldBeNil)
		So(exec.ResultsTotal, ShouldEqual, 8808)
		So(exec.ResultsLimit, ShouldEqual, 10)
		So(len(exec.Results), ShouldEqual, 1)
	})
}
