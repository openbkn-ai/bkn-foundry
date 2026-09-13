// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
)

var retentionNow = time.Date(2026, 9, 13, 3, 30, 0, 0, time.UTC)

func expiredHit(id string, start int64) interfaces.Hit {
	return interfaces.Hit{Source: map[string]any{"id": id}, Sort: []any{start, id}}
}

func Test_cleanupExpired_Disabled(t *testing.T) {
	Convey("without a retention period nothing is selected or deleted", t, func() {
		svc, _ := newWriteTestService(t) // strict mock: any call fails the test
		res, err := svc.cleanupExpired(context.Background(), RetentionConfig{BatchSize: 500, MaxBatches: 20}, retentionNow)
		So(err, ShouldBeNil)
		So(res.Executions, ShouldEqual, 0)
		So(res.Batches, ShouldEqual, 0)
	})
}

func Test_cleanupExpired_DeletesResultsThenExecutions(t *testing.T) {
	Convey("expired finished executions are deleted page by page, results first", t, func() {
		svc, osa := newWriteTestService(t)
		cutoff := retentionNow.Add(-30 * 24 * time.Hour).UnixMilli()
		expired := expiredExecutionsQuery(cutoff)

		var searches []map[string]any
		search := func(hits ...interfaces.Hit) *gomock.Call {
			return osa.EXPECT().SearchData(gomock.Any(), "ontology_action_executions_*", gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
					searches = append(searches, q.(map[string]any))
					return hits, nil
				})
		}
		deleteResults := func(ids []string, n int64) *gomock.Call {
			return osa.EXPECT().DeleteByQuery(gomock.Any(), interfaces.ActionExecutionResultsIndex,
				map[string]any{"query": map[string]any{"terms": map[string]any{"execution_id": ids}}}).Return(n, nil)
		}
		deleteExecutions := func(ids []string, n int64) *gomock.Call {
			return osa.EXPECT().DeleteByQuery(gomock.Any(), "ontology_action_executions_*",
				map[string]any{"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
					{"terms": map[string]any{"id": ids}}, expired,
				}}}}).Return(n, nil)
		}
		gomock.InOrder(
			search(expiredHit("e1", 100), expiredHit("e2", 200)),
			deleteResults([]string{"e1", "e2"}, 250),
			deleteExecutions([]string{"e1", "e2"}, 2),
			search(expiredHit("e3", 300)),
			deleteResults([]string{"e3"}, 0),
			deleteExecutions([]string{"e3"}, 1),
		)

		res, err := svc.cleanupExpired(context.Background(), RetentionConfig{RetentionDays: 30, BatchSize: 2, MaxBatches: 20}, retentionNow)
		So(err, ShouldBeNil)
		So(res.Cutoff, ShouldEqual, cutoff)
		So(res.Executions, ShouldEqual, 3)
		So(res.Results, ShouldEqual, 250)
		So(res.Batches, ShouldEqual, 2)
		So(res.Truncated, ShouldBeFalse)

		// Only finished executions are eligible; the second page continues after the first.
		So(searches[0]["query"], ShouldResemble, expired)
		So(searches[0], ShouldNotContainKey, "search_after")
		So(searches[1]["search_after"], ShouldResemble, []any{int64(200), "e2"})
		statuses := expired["bool"].(map[string]any)["filter"].([]map[string]any)[0]["terms"].(map[string]any)["status"]
		So(statuses, ShouldResemble, []string{"completed", "failed", "cancelled"})
	})
}

func Test_cleanupExpired_StopsAtMaxBatches(t *testing.T) {
	Convey("a run stops after MaxBatches full pages and reports that more may remain", t, func() {
		svc, osa := newWriteTestService(t)
		osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).Return([]interfaces.Hit{expiredHit("e", 1)}, nil).Times(2)
		osa.EXPECT().DeleteByQuery(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(4)

		res, err := svc.cleanupExpired(context.Background(), RetentionConfig{RetentionDays: 7, BatchSize: 1, MaxBatches: 2}, retentionNow)
		So(err, ShouldBeNil)
		So(res.Batches, ShouldEqual, 2)
		So(res.Truncated, ShouldBeTrue)
	})
}

func Test_cleanupExpired_DryRunDeletesNothing(t *testing.T) {
	Convey("a dry run pages exactly like a real run but only counts", t, func() {
		svc, osa := newWriteTestService(t)
		svc.resultsIndexReady.Store(true)
		gomock.InOrder(
			osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]interfaces.Hit{expiredHit("e1", 1), expiredHit("e2", 2)}, nil),
			osa.EXPECT().Count(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).Return([]byte(`{"count": 40}`), nil),
			osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil),
		)

		res, err := svc.cleanupExpired(context.Background(), RetentionConfig{RetentionDays: 90, BatchSize: 2, MaxBatches: 5, DryRun: true}, retentionNow)
		So(err, ShouldBeNil)
		So(res.Executions, ShouldEqual, 2)
		So(res.Results, ShouldEqual, 40)
	})
}

func Test_LoadRetentionConfig(t *testing.T) {
	Convey("retention configuration comes from the environment and is off by default", t, func() {
		Convey("defaults", func() {
			cfg, err := LoadRetentionConfig()
			So(err, ShouldBeNil)
			So(cfg, ShouldResemble, RetentionConfig{BatchSize: 500, MaxBatches: 20})
		})

		Convey("explicit values", func() {
			t.Setenv(envRetentionDays, "90")
			t.Setenv(envCleanupBatchSize, "200")
			t.Setenv(envCleanupMaxBatches, "5")
			t.Setenv(envCleanupDryRun, "TRUE")
			cfg, err := LoadRetentionConfig()
			So(err, ShouldBeNil)
			So(cfg, ShouldResemble, RetentionConfig{RetentionDays: 90, BatchSize: 200, MaxBatches: 5, DryRun: true})
		})

		Convey("invalid values are rejected rather than guessed", func() {
			t.Setenv(envRetentionDays, "-1")
			_, err := LoadRetentionConfig()
			So(err, ShouldNotBeNil)

			t.Setenv(envRetentionDays, "30")
			t.Setenv(envCleanupBatchSize, "5000")
			_, err = LoadRetentionConfig()
			So(err, ShouldNotBeNil)
		})

		Convey("cleanup-only mode is opt-in", func() {
			So(RetentionCleanupOnly(), ShouldBeFalse)
			t.Setenv(envRetentionCleanupOnly, "true")
			So(RetentionCleanupOnly(), ShouldBeTrue)
		})
	})
}

func Test_cleanupExpired_SearchAfterKeepsDecodedSortValues(t *testing.T) {
	Convey("sort values decoded as json.Number are sent back as JSON numbers in search_after", t, func() {
		svc, osa := newWriteTestService(t)
		// SearchData decodes with UseNumber, so real sort values arrive as json.Number.
		first := interfaces.Hit{Source: map[string]any{"id": "e2"}, Sort: []any{json.Number("1789000000000"), "e2"}}

		var second map[string]any
		gomock.InOrder(
			osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).Return([]interfaces.Hit{first}, nil),
			osa.EXPECT().DeleteByQuery(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), nil).Times(2),
			osa.EXPECT().SearchData(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, q any) ([]interfaces.Hit, error) {
					second = q.(map[string]any)
					return nil, nil
				}),
		)

		_, err := svc.cleanupExpired(context.Background(), RetentionConfig{RetentionDays: 30, BatchSize: 1, MaxBatches: 5}, retentionNow)
		So(err, ShouldBeNil)

		wire, err := sonic.Marshal(second["search_after"])
		So(err, ShouldBeNil)
		So(string(wire), ShouldEqual, `[1789000000000,"e2"]`)
	})
}
