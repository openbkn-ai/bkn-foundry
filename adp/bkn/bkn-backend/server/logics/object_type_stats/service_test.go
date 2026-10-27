// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type_stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

const testKN = "kn1"

func boundObjectType(otID, resourceID string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:       otID,
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: resourceID},
		},
		KNID:       testKN,
		Branch:     interfaces.MAIN_BRANCH,
		UpdateTime: 100,
		Status:     &interfaces.ObjectTypeStatus{},
	}
}

func rowCount(n int64) *int64 { return &n }

// statsFixture wires the service to mocks. Permissions default to query_data on every object type
// and row policies default to TRUE; a test overrides them by setting the maps before calling.
type statsFixture struct {
	svc  *objectTypeStatsService
	ota  *bmock.MockObjectTypeAccess
	ots  *bmock.MockObjectTypeService
	ps   *bmock.MockPermissionService
	cqs  *bmock.MockCypherQueryService
	now  time.Time
	ops  map[string][]string // ot id -> operations; missing means invisible
	pred map[string]string   // ot id -> predicate kind; missing means "true"
}

func newStatsFixture(t *testing.T, objectTypes ...*interfaces.ObjectType) *statsFixture {
	ctrl := gomock.NewController(t)
	f := &statsFixture{
		ota:  bmock.NewMockObjectTypeAccess(ctrl),
		ots:  bmock.NewMockObjectTypeService(ctrl),
		ps:   bmock.NewMockPermissionService(ctrl),
		cqs:  bmock.NewMockCypherQueryService(ctrl),
		now:  time.UnixMilli(1_790_000_000_000),
		ops:  map[string][]string{},
		pred: map[string]string{},
	}
	f.svc = NewObjectTypeStatsServiceWith(f.ota, f.ots, f.ps, f.cqs, 10*time.Minute, time.Second, time.Minute)
	f.svc.now = func() time.Time { return f.now }

	byID := map[string]*interfaces.ObjectType{}
	var ids []string
	for _, objectType := range objectTypes {
		byID[objectType.OTID] = objectType
		ids = append(ids, objectType.OTID)
		f.ops[objectType.OTID] = []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA}
	}
	f.ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), testKN, interfaces.MAIN_BRANCH).Return(ids, nil).AnyTimes()
	f.ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Nil(), testKN, interfaces.MAIN_BRANCH, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ any, _, _ string, want []string) ([]*interfaces.ObjectType, error) {
			var found []*interfaces.ObjectType
			for _, id := range want {
				if objectType, ok := byID[id]; ok {
					found = append(found, objectType)
				}
			}
			return found, nil
		}).AnyTimes()
	f.ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, gomock.Any(), gomock.Nil()).DoAndReturn(
		func(_ context.Context, _ string, refs []string, _ []string) (map[string]interfaces.PermissionResourceOps, error) {
			visible := map[string]interfaces.PermissionResourceOps{}
			for _, ref := range refs {
				if ops, ok := f.ops[ref[len(testKN)+1:]]; ok {
					visible[ref] = interfaces.PermissionResourceOps{ResourceID: ref, Operations: ops}
				}
			}
			return visible, nil
		}).AnyTimes()
	return f
}

// expectRowFilters answers the row-policy decision once per call from f.pred.
func (f *statsFixture) expectRowFilters() *gomock.Call {
	return f.ps.EXPECT().ResolveRowFilters(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, refs []string) ([]interfaces.RowFilterDecisionEntry, error) {
			decisions := make([]interfaces.RowFilterDecisionEntry, 0, len(refs))
			for _, ref := range refs {
				kind := f.pred[ref[len(testKN)+1:]]
				if kind == "" {
					kind = "true"
				}
				decisions = append(decisions, interfaces.RowFilterDecisionEntry{
					ObjectTypeRef: ref, Predicate: interfaces.RowFilterPredicate{Kind: kind},
					EffectiveRowFilterDigest: "digest-" + kind,
				})
			}
			return decisions, nil
		})
}

func countResult(n string) *interfaces.CypherQueryResult {
	return &interfaces.CypherQueryResult{Entries: []map[string]any{{countColumn: json.Number(n)}}}
}

func (f *statsFixture) run(query interfaces.ObjectTypeStatsQuery) map[string]*interfaces.ObjectTypeStatsEntry {
	query.KNID = testKN
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "u1", Type: "user"})
	result, err := f.svc.ObjectTypeStats(ctx, query)
	So(err, ShouldBeNil)
	So(result.Total, ShouldEqual, len(result.Entries))
	byID := map[string]*interfaces.ObjectTypeStatsEntry{}
	for _, entry := range result.Entries {
		byID[entry.ID] = entry
	}
	return byID
}

func Test_ObjectTypeStats_SourceSelection(t *testing.T) {
	Convey("Each object type is counted from the cheapest source that can answer it\n", t, func() {
		indexed := boundObjectType("indexed", "res-indexed")
		indexed.Status = &interfaces.ObjectTypeStatus{IndexAvailable: true, DocCount: 7, UpdateTime: 1_789_000_000_000}
		table := boundObjectType("table", "res-table")
		dataset := boundObjectType("dataset", "res-dataset")
		view := boundObjectType("view", "res-view")
		f := newStatsFixture(t, indexed, table, dataset, view)
		f.expectRowFilters()

		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, objectTypes []*interfaces.ObjectType) map[string]*interfaces.VegaResource {
				return map[string]*interfaces.VegaResource{
					// Timestamped exact resource counts are reused without a scan.
					"table":   {ID: "res-table", Category: "table", RowCount: rowCount(52310), RowCountTime: rowCount(f.now.Add(-time.Minute).UnixMilli())},
					"dataset": {ID: "res-dataset", Category: "dataset", RowCount: rowCount(12), RowCountTime: rowCount(1_790_000_000_000)},
					"view":    {ID: "res-view", Category: "logicview", RowCount: rowCount(999)},
				}
			})
		// The untimestamped view falls back to Cypher.
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, q interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
				// Runs on a counting goroutine, where convey assertions are unavailable.
				if q.KNID != testKN || q.Branch != interfaces.MAIN_BRANCH {
					t.Errorf("unexpected scope %s/%s", q.KNID, q.Branch)
				}
				switch q.Query {
				case "MATCH (t:`view`) RETURN count(*) AS n":
					return countResult("41"), nil
				case "MATCH (t:`table`) RETURN count(*) AS n":
					return countResult("52999"), nil
				}
				t.Errorf("unexpected query %q", q.Query)
				return nil, nil
			}).Times(1)

		entries := f.run(interfaces.ObjectTypeStatsQuery{})

		So(len(entries), ShouldEqual, 4)
		So(*entries["indexed"].Count, ShouldEqual, 7)
		So(entries["indexed"].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_INDEX)
		So(entries["indexed"].Exact, ShouldBeFalse)
		So(entries["indexed"].AsOf, ShouldEqual, 1_789_000_000_000)

		So(*entries["table"].Count, ShouldEqual, 52310)
		So(entries["table"].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_CATALOG)
		So(entries["table"].Exact, ShouldBeTrue)
		So(entries["table"].AsOf, ShouldEqual, f.now.Add(-time.Minute).UnixMilli())

		So(*entries["dataset"].Count, ShouldEqual, 12)
		So(entries["dataset"].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_INDEX)
		So(entries["dataset"].Exact, ShouldBeTrue)

		So(*entries["view"].Count, ShouldEqual, 41)
		So(entries["view"].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_QUERY)
		So(entries["view"].Exact, ShouldBeTrue)
	})

	Convey("A dataset resource answers without any COUNT(*)\n", t, func() {
		f := newStatsFixture(t, boundObjectType("notes", "res-notes"))
		f.expectRowFilters()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{
			"notes": {ID: "res-notes", Category: "dataset", RowCount: rowCount(0), RowCountTime: rowCount(1_790_000_000_000)},
		})
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Times(0)

		entries := f.run(interfaces.ObjectTypeStatsQuery{OTIDs: []string{"notes"}})

		So(*entries["notes"].Count, ShouldEqual, 0)
		So(entries["notes"].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_INDEX)
		So(entries["notes"].Exact, ShouldBeTrue)
	})

	Convey("A row policy forces an exact query even when an index exists\n", t, func() {
		indexed := boundObjectType("indexed", "res-indexed")
		indexed.Status = &interfaces.ObjectTypeStatus{IndexAvailable: true, DocCount: 7}
		dataset := boundObjectType("dataset", "res-dataset")
		f := newStatsFixture(t, indexed, dataset)
		f.pred["indexed"] = "in"
		f.pred["dataset"] = "in"
		f.expectRowFilters()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Times(0)
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Return(countResult("2"), nil).Times(2)

		entries := f.run(interfaces.ObjectTypeStatsQuery{})

		for _, id := range []string{"indexed", "dataset"} {
			So(*entries[id].Count, ShouldEqual, 2)
			So(entries[id].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_QUERY)
			So(entries[id].Exact, ShouldBeTrue)
		}
	})

	Convey("An object type without a data resource fails on its own row\n", t, func() {
		unbound := boundObjectType("region", "")
		unbound.DataSource = nil
		f := newStatsFixture(t, unbound, boundObjectType("orders", "res-orders"))
		f.expectRowFilters()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{
			"orders": {ID: "res-orders", Category: "dataset", RowCount: rowCount(5), RowCountTime: rowCount(1_790_000_000_000)},
		})

		entries := f.run(interfaces.ObjectTypeStatsQuery{})

		So(entries["region"].Count, ShouldBeNil)
		So(entries["region"].Error.Code, ShouldEqual, berrors.BknBackend_ObjectType_NoDataSource)
		So(*entries["orders"].Count, ShouldEqual, 5)
	})
}

func Test_ObjectTypeStats_RowFailures(t *testing.T) {
	Convey("A failed query reports the service error code without the dependency's message\n", t, func() {
		f := newStatsFixture(t, boundObjectType("broken", "res-broken"), boundObjectType("fine", "res-fine"))
		f.expectRowFilters()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{})
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, q interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
				switch q.Query {
				case "MATCH (t:`broken`) RETURN count(*) AS n":
					return nil, errors.New("SELECT COUNT(*) FROM secret_table failed")
				}
				return countResult("1"), nil
			}).Times(2)

		entries := f.run(interfaces.ObjectTypeStatsQuery{})

		So(entries["broken"].Error.Code, ShouldEqual, berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed)
		So(entries["broken"].Error.Message, ShouldNotContainSubstring, "secret_table")
		So(*entries["fine"].Count, ShouldEqual, 1)
	})

	Convey("A refused query keeps the refusal on its row\n", t, func() {
		f := newStatsFixture(t, boundObjectType("guarded", "res-guarded"))
		f.expectRowFilters()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{})
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
				return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
			})

		entries := f.run(interfaces.ObjectTypeStatsQuery{})

		So(entries["guarded"].Count, ShouldBeNil)
		So(entries["guarded"].Error.Code, ShouldEqual, httperrors.PublicError_Forbidden)
	})

	Convey("Row policies that cannot be resolved fail the rows, not the request\n", t, func() {
		f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
		f.ps.EXPECT().ResolveRowFilters(gomock.Any(), gomock.Any()).Return(nil, errors.New("bkn-safe down"))
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Times(0)
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Times(0)

		entries := f.run(interfaces.ObjectTypeStatsQuery{})

		So(entries["orders"].Count, ShouldBeNil)
		So(entries["orders"].Error.Code, ShouldEqual, berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed)
	})
}

func Test_ObjectTypeStats_Permissions(t *testing.T) {
	Convey("Counts are returned only for object types the caller may query\n", t, func() {
		f := newStatsFixture(t, boundObjectType("open", "res-open"), boundObjectType("viewonly", "res-viewonly"),
			boundObjectType("hidden", "res-hidden"))
		f.ops["viewonly"] = []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}
		delete(f.ops, "hidden")
		f.expectRowFilters().Times(2).Do(func(_ context.Context, refs []string) {
			// Only the queryable object type reaches the row-policy decision.
			So(refs, ShouldResemble, []string{testKN + "/open"})
		})
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, objectTypes []*interfaces.ObjectType) map[string]*interfaces.VegaResource {
				So(len(objectTypes), ShouldEqual, 1)
				So(objectTypes[0].OTID, ShouldEqual, "open")
				return map[string]*interfaces.VegaResource{"open": {Category: "dataset", RowCount: rowCount(4), RowCountTime: rowCount(1_790_000_000_000)}}
			})

		Convey("Listed by id, a hidden object type reads exactly like a missing one\n", func() {
			entries := f.run(interfaces.ObjectTypeStatsQuery{OTIDs: []string{"open", "viewonly", "hidden", "missing"}})

			So(*entries["open"].Count, ShouldEqual, 4)
			So(entries["viewonly"].Count, ShouldBeNil)
			So(entries["viewonly"].Error.Code, ShouldEqual, httperrors.PublicError_Forbidden)
			So(entries["hidden"].Count, ShouldBeNil)
			So(entries["hidden"].Error, ShouldResemble, entries["missing"].Error)
			So(entries["missing"].Error.Code, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeNotFound)

			// The whole network leaves the hidden object type out entirely.
			f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Times(0)
			entries = f.run(interfaces.ObjectTypeStatsQuery{})
			So(len(entries), ShouldEqual, 2)
			So(entries, ShouldNotContainKey, "hidden")
			So(entries["open"].Source, ShouldEqual, interfaces.OBJECT_TYPE_COUNT_SOURCE_INDEX)
		})
	})
}

func Test_ObjectTypeStats_Cache(t *testing.T) {
	Convey("A cached count is served without touching the data again\n", t, func() {
		f := newStatsFixture(t, boundObjectType("view", "res-view"))
		f.expectRowFilters().AnyTimes()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{}).AnyTimes()
		queries := 0
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
			func(context.Context, interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
				queries++
				return countResult("10"), nil
			}).AnyTimes()

		first := f.run(interfaces.ObjectTypeStatsQuery{})
		takenAt := first["view"].AsOf
		So(queries, ShouldEqual, 1)

		f.now = f.now.Add(5 * time.Minute)
		second := f.run(interfaces.ObjectTypeStatsQuery{})
		So(queries, ShouldEqual, 1)
		So(*second["view"].Count, ShouldEqual, 10)
		// A cached count keeps the time it was taken.
		So(second["view"].AsOf, ShouldEqual, takenAt)

		Convey("refresh recomputes it\n", func() {
			refreshed := f.run(interfaces.ObjectTypeStatsQuery{Refresh: true})
			So(queries, ShouldEqual, 2)
			So(refreshed["view"].AsOf, ShouldEqual, f.now.UnixMilli())
		})

		Convey("it expires after the TTL\n", func() {
			f.now = f.now.Add(6 * time.Minute)
			f.run(interfaces.ObjectTypeStatsQuery{})
			So(queries, ShouldEqual, 2)
		})

		Convey("a different row policy does not share it\n", func() {
			f.pred["view"] = "in"
			f.run(interfaces.ObjectTypeStatsQuery{})
			So(queries, ShouldEqual, 2)
		})

		Convey("a changed model does not share it\n", func() {
			objectTypes, _ := f.ota.GetObjectTypesByIDs(context.Background(), nil, testKN, interfaces.MAIN_BRANCH, []string{"view"})
			objectTypes[0].UpdateTime = 200
			f.run(interfaces.ObjectTypeStatsQuery{})
			So(queries, ShouldEqual, 2)
		})
	})

	Convey("Failures are not cached\n", t, func() {
		f := newStatsFixture(t, boundObjectType("view", "res-view"))
		f.expectRowFilters().AnyTimes()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{}).AnyTimes()
		gomock.InOrder(
			f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")),
			f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Return(countResult("6"), nil),
		)

		So(f.run(interfaces.ObjectTypeStatsQuery{})["view"].Error, ShouldNotBeNil)
		So(*f.run(interfaces.ObjectTypeStatsQuery{})["view"].Count, ShouldEqual, 6)
	})
}

func Test_countCache_bounded(t *testing.T) {
	Convey("The cache sweeps expired counts and never grows past its bound\n", t, func() {
		cache := newCountCache(time.Minute)
		now := time.Unix(0, 0)
		for i := 0; i < maxCachedCounts; i++ {
			cache.put(string(rune(i)), cachedCount{count: int64(i)}, now)
		}
		So(len(cache.entries), ShouldEqual, maxCachedCounts)

		later := now.Add(2 * time.Minute)
		cache.put("fresh", cachedCount{count: 1}, later)
		So(len(cache.entries), ShouldEqual, 1)

		disabled := newCountCache(0)
		disabled.put("k", cachedCount{}, now)
		_, ok := disabled.get("k", now)
		So(ok, ShouldBeFalse)
	})
}

func TestStatsCountContinuesWithoutRestart(t *testing.T) {
	f := newStatsFixture(t, boundObjectType("slow", "res-slow"), boundObjectType("fast", "res-fast"))
	f.svc.queryTimeout = 20 * time.Millisecond
	f.expectRowFilters().AnyTimes()
	f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{}).AnyTimes()
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, q interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
		if q.Query == "MATCH (t:`fast`) RETURN count(*) AS n" {
			return countResult("9"), nil
		}
		calls.Add(1)
		if ai, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); ai.ID != "u1" {
			t.Errorf("caller identity lost: %+v", ai)
		}
		if q.ExecutionTimeout != time.Minute || q.ExpectedRowFilterDigests["slow"] != "digest-true" || q.ExpectedObjectTypeVersions["slow"] != 100 {
			t.Errorf("unpinned counting query: %+v", q)
		}
		select {
		case <-release:
			return countResult("77"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}).AnyTimes()
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u1", Type: "user"}))
	defer cancel()
	result, err := f.svc.ObjectTypeStats(caller, interfaces.ObjectTypeStatsQuery{KNID: testKN})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*interfaces.ObjectTypeStatsEntry{}
	for _, entry := range result.Entries {
		byID[entry.ID] = entry
	}
	if byID["slow"].Error == nil || byID["slow"].Error.Code != berrors.BknBackend_ObjectType_StatsCountInProgress || byID["fast"].Count == nil || *byID["fast"].Count != 9 {
		t.Fatalf("unexpected result: %+v", result)
	}
	cancel() // HTTP cancellation must not end the running scan.
	flight := f.svc.running.get(statsTestKey("slow"))
	if flight == nil {
		t.Fatal("count did not continue")
	}
	_, err = f.svc.ObjectTypeStats(context.Background(), interfaces.ObjectTypeStatsQuery{KNID: testKN, OTIDs: []string{"slow"}, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("started %d scans", calls.Load())
	}
	release <- struct{}{}
	select {
	case <-flight.done:
	case <-time.After(5 * time.Second):
		t.Fatal("count did not finish")
	}
	result, err = f.svc.ObjectTypeStats(context.Background(), interfaces.ObjectTypeStatsQuery{KNID: testKN, OTIDs: []string{"slow"}})
	if err != nil || result.Entries[0].Count == nil || *result.Entries[0].Count != 77 || calls.Load() != 1 {
		t.Fatalf("background result not cached: %+v, %v, calls=%d", result, err, calls.Load())
	}
}

func statsTestKey(id string) string {
	return testKN + "\x00main\x00" + id + "\x00100\x00digest-true"
}

func TestStatsGlobalConcurrencyLimit(t *testing.T) {
	var objectTypes []*interfaces.ObjectType
	for i := 0; i < 12; i++ {
		objectTypes = append(objectTypes, boundObjectType(fmt.Sprint(i), fmt.Sprint("res-", i)))
	}
	f := newStatsFixture(t, objectTypes...)
	f.svc.queryTimeout = 20 * time.Millisecond
	f.expectRowFilters().AnyTimes()
	f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{}).AnyTimes()
	release := make(chan struct{})
	var active, peak atomic.Int32
	f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-release:
			return countResult("1"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}).AnyTimes()
	for range 2 {
		if _, err := f.svc.ObjectTypeStats(context.Background(), interfaces.ObjectTypeStatsQuery{KNID: testKN}); err != nil {
			t.Fatal(err)
		}
	}
	if peak.Load() != queryConcurrency || active.Load() != queryConcurrency {
		t.Errorf("peak=%d active=%d", peak.Load(), active.Load())
	}
	var flights []*countFlight
	f.svc.running.mu.Lock()
	for _, flight := range f.svc.running.keys {
		flights = append(flights, flight)
	}
	f.svc.running.mu.Unlock()
	close(release)
	for _, flight := range flights {
		select {
		case <-flight.done:
		case <-time.After(5 * time.Second):
			t.Fatal("flight leaked")
		}
	}
}

func TestStatsRejectsInvalidCounts(t *testing.T) {
	for _, value := range []any{json.Number("1.5"), json.Number("9223372036854775808"), json.Number("-1"), -1, int64(-1), -1.0, 1.5, math.NaN(), math.Inf(1), math.Exp2(63), "-1", "bad", nil} {
		if _, ok := asInt64(value); ok {
			t.Errorf("accepted invalid count %v", value)
		}
	}
	for _, value := range []any{json.Number("9223372036854775807"), json.Number("0"), int64(0), 42, 42.0, "42"} {
		if _, ok := asInt64(value); !ok {
			t.Errorf("rejected count %v", value)
		}
	}
}

func TestStatsVegaFreshness(t *testing.T) {
	for _, tc := range []struct {
		name             string
		count, timestamp *int64
		category         string
		refresh          bool
		source           string
	}{
		{"fresh exact", rowCount(21), rowCount(1_789_999_940_000), "table", false, "catalog"},
		{"zero is exact", rowCount(0), rowCount(1_789_999_940_000), "table", false, "catalog"},
		{"logical view", rowCount(21), rowCount(1_789_999_940_000), "logical_view", false, "catalog"},
		{"legacy timestamp absent", rowCount(21), nil, "table", false, "query"},
		{"timestamp zero", rowCount(21), rowCount(0), "table", false, "query"},
		{"stale", rowCount(21), rowCount(1_789_999_400_000), "table", false, "query"},
		{"future", rowCount(21), rowCount(1_790_000_000_001), "table", false, "query"},
		{"negative", rowCount(-1), rowCount(1_789_999_940_000), "table", false, "query"},
		{"count absent", nil, rowCount(1_789_999_940_000), "table", false, "query"},
		{"forced refresh", rowCount(21), rowCount(1_789_999_940_000), "table", true, "query"},
		{"dataset forced refresh is live", rowCount(21), rowCount(1_790_000_000_000), "dataset", true, "index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Convey("Select only usable resource measurements", t, func() {
				f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
				f.expectRowFilters()
				f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"orders": {Category: tc.category, RowCount: tc.count, RowCountTime: tc.timestamp}})
				if tc.source == "query" {
					f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Return(countResult("42"), nil)
				} else {
					f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Times(0)
				}
				entry := f.run(interfaces.ObjectTypeStatsQuery{Refresh: tc.refresh})["orders"]
				So(entry.Error, ShouldBeNil)
				So(entry.Exact, ShouldBeTrue)
				So(entry.Source, ShouldEqual, tc.source)
				if tc.source != "query" {
					So(*entry.Count, ShouldEqual, *tc.count)
					So(entry.AsOf, ShouldEqual, *tc.timestamp)
				}
			})
		})
	}
	Convey("Caching a resource measurement never extends its freshness", t, func() {
		f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
		f.expectRowFilters().AnyTimes()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"orders": {Category: "table", RowCount: rowCount(21), RowCountTime: rowCount(f.now.Add(-9 * time.Minute).UnixMilli())}}).Times(2)
		So(f.run(interfaces.ObjectTypeStatsQuery{})["orders"].Source, ShouldEqual, "catalog")
		f.now = f.now.Add(time.Minute)
		f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Return(countResult("42"), nil)
		So(f.run(interfaces.ObjectTypeStatsQuery{})["orders"].Source, ShouldEqual, "query")
	})
}

func TestStatsMissingRowDecisionFailsClosed(t *testing.T) {
	Convey("A missing row decision cannot use resource totals", t, func() {
		f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
		f.ps.EXPECT().ResolveRowFilters(gomock.Any(), gomock.Any()).Return(nil, nil)
		entry := f.run(interfaces.ObjectTypeStatsQuery{})["orders"]
		So(entry.Count, ShouldBeNil)
		So(entry.Error.Code, ShouldEqual, berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed)
	})
}

func TestStatsCachedCountsStillRequirePermission(t *testing.T) {
	Convey("Revoking query permission hides even a cached total", t, func() {
		f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
		f.expectRowFilters()
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"orders": {Category: "table", RowCount: rowCount(21), RowCountTime: rowCount(f.now.UnixMilli())}})
		So(*f.run(interfaces.ObjectTypeStatsQuery{})["orders"].Count, ShouldEqual, 21)
		f.ops["orders"] = []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}
		entry := f.run(interfaces.ObjectTypeStatsQuery{})["orders"]
		So(entry.Count, ShouldBeNil)
		So(entry.Error.Code, ShouldEqual, httperrors.PublicError_Forbidden)
	})
	Convey("Callers with the same effective row policy share the total", t, func() {
		f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
		f.expectRowFilters().Times(2)
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"orders": {Category: "table", RowCount: rowCount(21), RowCountTime: rowCount(f.now.UnixMilli())}})
		f.run(interfaces.ObjectTypeStatsQuery{})
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u2", Type: "user"})
		result, err := f.svc.ObjectTypeStats(ctx, interfaces.ObjectTypeStatsQuery{KNID: testKN})
		So(err, ShouldBeNil)
		So(*result.Entries[0].Count, ShouldEqual, 21)
	})
}

func TestStatsExecutionDeadlineAllowsRetry(t *testing.T) {
	Convey("An exhausted execution budget is not cached or left in flight", t, func() {
		f := newStatsFixture(t, boundObjectType("orders", "res-orders"))
		f.svc.backgroundTimeout = 5 * time.Millisecond
		f.expectRowFilters().Times(2)
		f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{}).Times(2)
		gomock.InOrder(
			f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}),
			f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).Return(countResult("42"), nil),
		)
		first := f.run(interfaces.ObjectTypeStatsQuery{})["orders"]
		So(first.Error.Code, ShouldEqual, berrors.BknBackend_ObjectType_StatsQueryTimeout)
		So(f.svc.running.get(statsTestKey("orders")), ShouldBeNil)
		So(*f.run(interfaces.ObjectTypeStatsQuery{})["orders"].Count, ShouldEqual, 42)
	})
}

func TestRowErrorRendersLazyDescriptions(t *testing.T) {
	err := httperrors.NewHTTPError(context.Background(), http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound).WithErrorDetails("private_table")
	if err.Description != "" {
		t.Fatal("fixture must start with an unrendered error")
	}
	row := rowError(err)
	if row.Message == "" || row.Code != err.ErrorCode {
		t.Fatalf("missing localized row error: %+v", row)
	}
	if row.Message == "private_table" {
		t.Fatal("dependency details leaked")
	}
	if err.Description != "" {
		t.Fatal("rendering must not mutate a shared dependency error")
	}
}

func TestStatsRefreshFlightPrecedesOldCounts(t *testing.T) {
	f := newStatsFixture(t, boundObjectType("product", "res-product"))
	f.svc.queryTimeout = time.Millisecond
	f.expectRowFilters().AnyTimes()
	f.ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"product": {Category: "table", RowCount: rowCount(1), RowCountTime: rowCount(f.now.UnixMilli())}}).AnyTimes()
	query := interfaces.ObjectTypeStatsQuery{KNID: testKN, OTIDs: []string{"product"}}
	first, err := f.svc.ObjectTypeStats(context.Background(), query)
	if err != nil || first.Entries[0].Count == nil {
		t.Fatalf("initial measurement: %v, %+v", err, first)
	}
	release := make(chan struct{})
	defer close(release)
	f.cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
		select {
		case <-release:
			return countResult("42"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}).Times(1)
	query.Refresh = true
	refresh, err := f.svc.ObjectTypeStats(context.Background(), query)
	if err != nil || refresh.Entries[0].Error == nil {
		t.Fatalf("refresh did not continue: %v, %+v", err, refresh)
	}
	flight := f.svc.running.get(statsTestKey("product"))
	if flight == nil {
		t.Fatal("no active refresh")
	}
	query.Refresh = false
	polling, err := f.svc.ObjectTypeStats(context.Background(), query)
	if err != nil || polling.Entries[0].Count != nil || polling.Entries[0].Error == nil || polling.Entries[0].Error.Code != berrors.BknBackend_ObjectType_StatsCountInProgress {
		t.Fatalf("poll returned an old cached or catalog count: %v, %+v", err, polling)
	}
	release <- struct{}{}
	select {
	case <-flight.done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not finish")
	}
	completed, err := f.svc.ObjectTypeStats(context.Background(), query)
	if err != nil || completed.Entries[0].Count == nil || *completed.Entries[0].Count != 42 || completed.Entries[0].Source != "query" {
		t.Fatalf("poll missed refreshed count: %v, %+v", err, completed)
	}
}
