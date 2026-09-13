// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

// Writes to an execution record must never be read-modify-write (#790): every write is a
// partial update decided by OpenSearch on the latest document version.

var paginationMetadataKeys = []string{"results_total", "results_offset", "results_limit"}

func runningExecutionSource(resultCount int) map[string]any {
	results := make([]any, 0, resultCount)
	for i := 0; i < resultCount; i++ {
		results = append(results, map[string]any{"_instance_id": fmt.Sprintf("i_%d", i), "status": "success"})
	}
	return map[string]any{
		"id": "exec_1", "kn_id": "kn_1", "status": interfaces.ExecutionStatusRunning,
		"total_count": float64(2000), "success_count": float64(resultCount), "failed_count": float64(3),
		"start_time": float64(1700000000000), "results": results,
	}
}

func scriptOf(body any) (source string, params map[string]any) {
	script := body.(map[string]any)["script"].(map[string]any)
	return script["source"].(string), script["params"].(map[string]any)
}

// newWriteTestService returns a service over a strict mock: any unexpected call, such as an
// InsertData that would overwrite the whole document, fails the test.
func newWriteTestService(t *testing.T) (*actionLogsService, *omock.MockOpenSearchAccess) {
	ctrl := gomock.NewController(t)
	osa := omock.NewMockOpenSearchAccess(ctrl)
	return &actionLogsService{osAccess: osa}, osa
}

func Test_CancelExecution_LeavesResultsUntouched(t *testing.T) {
	Convey("cancelling an execution with more than 1000 results must not rewrite them", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), "ontology_action_executions_kn_1").Return(true, nil)

		var searched map[string]any
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				searched = q.(map[string]any)
				source := runningExecutionSource(1500)
				delete(source, "results") // excluded by the _source filter
				return []interfaces.Hit{{Source: source}}, nil
			})

		var updateBody any
		osa.EXPECT().UpdateData(gomock.Any(), "ontology_action_executions_kn_1", "exec_1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, body any) (string, error) {
				updateBody = body
				return interfaces.UpdateResultUpdated, nil
			})

		res, err := svc.CancelExecution(context.Background(), "kn_1", "exec_1", "operator request")
		So(err, ShouldBeNil)

		// The summary read skips the per-instance results entirely.
		So(searched["_source"], ShouldResemble, map[string]any{"excludes": []string{"results"}})

		source, params := scriptOf(updateBody)
		So(source, ShouldEqual, cancelScript)
		So(params["cancellable"], ShouldResemble, []string{"pending", "running"})
		So(params["cancelled"], ShouldEqual, "cancelled")
		So(params, ShouldNotContainKey, "results")
		So(strings.Contains(source, "results"), ShouldBeFalse)

		So(res.Status, ShouldEqual, interfaces.ExecutionStatusCancelled)
		// 2000 planned, 1500 succeeded and 3 failed so far: 497 had not run yet.
		So(res.CancelledCount, ShouldEqual, 497)
		So(res.CompletedCount, ShouldEqual, 1500)
	})
}

func Test_CancelExecution_RejectsTerminalStatus(t *testing.T) {
	Convey("an execution that already finished cannot be cancelled and is not written", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).Return([]interfaces.Hit{{Source: map[string]any{
			"id": "exec_1", "status": interfaces.ExecutionStatusCompleted,
		}}}, nil)

		_, err := svc.CancelExecution(context.Background(), "kn_1", "exec_1", "")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "cannot be cancelled, current status: completed")
	})
}

func Test_CancelExecution_ExecutionFinishedMeanwhile(t *testing.T) {
	Convey("a cancel racing with the terminal write reports the status that won", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
		gomock.InOrder(
			osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]interfaces.Hit{{Source: runningExecutionSource(0)}}, nil),
			osa.EXPECT().UpdateData(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(interfaces.UpdateResultNoop, nil),
			osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]interfaces.Hit{{Source: map[string]any{"status": interfaces.ExecutionStatusCompleted}}}, nil),
		)

		_, err := svc.CancelExecution(context.Background(), "kn_1", "exec_1", "")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "cannot be cancelled, current status: completed")
	})
}

func Test_CancelExecution_DocumentVanished(t *testing.T) {
	Convey("a document deleted between the read and the cancel is reported as not found", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]interfaces.Hit{{Source: runningExecutionSource(0)}}, nil)
		osa.EXPECT().UpdateData(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return("", fmt.Errorf("update: %w", interfaces.ErrDocumentNotFound))

		_, err := svc.CancelExecution(context.Background(), "kn_1", "exec_1", "")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "execution not found: exec_1")
	})
}

func Test_UpdateExecutionProgress_NeverCarriesStatus(t *testing.T) {
	Convey("a progress write merges counters and results only, without reading first", t, func() {
		svc, osa := newWriteTestService(t)

		var updateBody map[string]any
		osa.EXPECT().UpdateData(gomock.Any(), "ontology_action_executions_kn_1", "exec_1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, body any) (string, error) {
				updateBody = body.(map[string]any)
				return interfaces.UpdateResultUpdated, nil
			})

		err := svc.UpdateExecutionProgress(context.Background(), "kn_1", "exec_1", &interfaces.ExecutionProgress{
			SuccessCount: 4,
			FailedCount:  1,
			Results:      []interfaces.ObjectExecutionResult{{Status: interfaces.ObjectStatusSuccess}},
		})
		So(err, ShouldBeNil)

		doc, ok := updateBody["doc"].(map[string]any)
		So(ok, ShouldBeTrue)
		So(doc["success_count"], ShouldEqual, 4)
		So(doc["failed_count"], ShouldEqual, 1)
		So(doc["results"], ShouldHaveLength, 1)
		So(doc, ShouldNotContainKey, "status")
		for _, key := range paginationMetadataKeys {
			So(doc, ShouldNotContainKey, key)
		}
	})
}

func Test_MarkExecutionRunning_OnlyFromPending(t *testing.T) {
	Convey("marking running is conditional on the stored status still being pending", t, func() {
		svc, osa := newWriteTestService(t)

		var updateBody any
		osa.EXPECT().UpdateData(gomock.Any(), gomock.Any(), "exec_1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, body any) (string, error) {
				updateBody = body
				// Cancelled before it started: OpenSearch leaves the document untouched.
				return interfaces.UpdateResultNoop, nil
			})

		err := svc.MarkExecutionRunning(context.Background(), "kn_1", "exec_1")
		So(err, ShouldBeNil)

		source, params := scriptOf(updateBody)
		So(source, ShouldEqual, markRunningScript)
		So(params, ShouldResemble, map[string]any{"from": "pending", "to": "running"})
	})
}

func Test_FinishExecution_KeepsCancelled(t *testing.T) {
	Convey("the terminal write never replaces a cancelled status", t, func() {
		svc, osa := newWriteTestService(t)

		var updateBody any
		osa.EXPECT().UpdateData(gomock.Any(), gomock.Any(), "exec_1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, body any) (string, error) {
				updateBody = body
				return interfaces.UpdateResultUpdated, nil
			})

		err := svc.FinishExecution(context.Background(), "kn_1", "exec_1", &interfaces.ExecutionOutcome{
			Status:       interfaces.ExecutionStatusCompleted,
			SuccessCount: 3,
			EndTime:      1700000009000,
			DurationMs:   9000,
		})
		So(err, ShouldBeNil)

		source, params := scriptOf(updateBody)
		So(source, ShouldEqual, finishScript)
		So(source, ShouldStartWith, "if (ctx._source.status != params.cancelled) { ctx._source.status = params.status }")
		So(params["cancelled"], ShouldEqual, "cancelled")
		So(params["status"], ShouldEqual, "completed")
		So(params["success_count"], ShouldEqual, 3)
		// A nil result list is stored as an empty array, not null.
		So(params["results"], ShouldResemble, []interfaces.ObjectExecutionResult{})
		for _, key := range paginationMetadataKeys {
			So(params, ShouldNotContainKey, key)
		}
	})
}

func Test_GetExecutionStatus_ReadsStatusOnly(t *testing.T) {
	Convey("the cancellation check reads the status field and nothing else", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)

		var searched map[string]any
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				searched = q.(map[string]any)
				return []interfaces.Hit{{Source: map[string]any{"status": interfaces.ExecutionStatusCancelled}}}, nil
			})

		status, err := svc.GetExecutionStatus(context.Background(), "kn_1", "exec_1")
		So(err, ShouldBeNil)
		So(status, ShouldEqual, interfaces.ExecutionStatusCancelled)
		So(searched["_source"], ShouldResemble, map[string]any{"includes": []string{"status"}})
	})
}

func Test_QueryExecutions_KeywordMatchesExecutionIDSubstring(t *testing.T) {
	Convey("keyword filters the list and the total by a literal, case-insensitive execution id substring", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)

		var searched, counted map[string]any
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				searched = q.(map[string]any)
				return nil, nil
			})
		osa.EXPECT().Count(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]byte, error) {
				counted = q.(map[string]any)
				return []byte(`{"count": 0}`), nil
			})

		_, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{
			KNID:      "kn_1",
			Keyword:   ` 01A0*b?c\ `,
			NeedTotal: true,
		})
		So(err, ShouldBeNil)

		wantCondition := map[string]any{
			"wildcard": map[string]any{
				"id": map[string]any{
					// Trimmed, and * ? \ escaped so the user's input matches literally.
					"value":            `*01A0\*b\?c\\*`,
					"case_insensitive": true,
				},
			},
		}
		mustOf := func(q map[string]any) []map[string]any {
			return q["query"].(map[string]any)["bool"].(map[string]any)["must"].([]map[string]any)
		}
		So(mustOf(searched), ShouldContain, wantCondition)
		So(mustOf(counted), ShouldContain, wantCondition)
	})

	Convey("a blank keyword adds no condition", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().IndexExists(gomock.Any(), gomock.Any()).Return(true, nil)

		var searched map[string]any
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
				searched = q.(map[string]any)
				return nil, nil
			})

		_, err := svc.QueryExecutions(context.Background(), &interfaces.ActionLogQuery{KNID: "kn_1", Keyword: "   "})
		So(err, ShouldBeNil)
		must := searched["query"].(map[string]any)["bool"].(map[string]any)["must"].([]map[string]any)
		So(must, ShouldBeEmpty)
	})
}
