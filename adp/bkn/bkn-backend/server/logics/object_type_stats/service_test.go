// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type_stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"
	"sync/atomic"
	"testing"
	"time"
)

func intPtr(n int64) *int64 { return &n }
func object(id string) *interfaces.ObjectType {
	return &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "res-" + id}}}
}
func fixture(t *testing.T, objects ...*interfaces.ObjectType) (*objectTypeStatsService, *bmock.MockPermissionService, *bmock.MockObjectTypeService, *bmock.MockCypherQueryService, map[string][]string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	cqs := bmock.NewMockCypherQueryService(ctrl)
	var ids []string
	byID := map[string]*interfaces.ObjectType{}
	ops := map[string][]string{}
	for _, ot := range objects {
		ids = append(ids, ot.OTID)
		byID[ot.OTID] = ot
		ops["kn/"+ot.OTID] = []string{interfaces.OPERATION_TYPE_QUERY_DATA}
	}
	ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), "kn", "main").Return(ids, nil).AnyTimes()
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Nil(), "kn", "main", gomock.Any()).DoAndReturn(func(_ context.Context, _ any, _, _ string, want []string) ([]*interfaces.ObjectType, error) {
		var found []*interfaces.ObjectType
		for _, id := range want {
			if ot := byID[id]; ot != nil {
				found = append(found, ot)
			}
		}
		return found, nil
	}).AnyTimes()
	ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, gomock.Any(), gomock.Nil()).DoAndReturn(func(_ context.Context, _ string, refs []string, _ []string) (map[string]interfaces.PermissionResourceOps, error) {
		visible := map[string]interfaces.PermissionResourceOps{}
		for _, ref := range refs {
			if op, ok := ops[ref]; ok {
				visible[ref] = interfaces.PermissionResourceOps{ResourceID: ref, Operations: op}
			}
		}
		return visible, nil
	}).AnyTimes()
	return &objectTypeStatsService{ota: ota, ots: ots, ps: ps, cqs: cqs}, ps, ots, cqs, ops
}
func decision(id, kind string) interfaces.RowFilterDecisionEntry {
	return interfaces.RowFilterDecisionEntry{ObjectTypeRef: "kn/" + id, Predicate: interfaces.RowFilterPredicate{Kind: kind}}
}
func run(t *testing.T, s *objectTypeStatsService, ids ...string) *interfaces.ObjectTypeStatsResult {
	t.Helper()
	result, err := s.ObjectTypeStats(context.Background(), interfaces.ObjectTypeStatsQuery{KNID: "kn", OTIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStatsSourceSelection(t *testing.T) {
	for _, tc := range []struct {
		name, kind, category string
		count, stamp         *int64
		queryValue           any
		queryError           error
		wantCount            *int64
		source               string
	}{
		{name: "table", kind: "true", category: "table", count: intPtr(12), stamp: intPtr(100), wantCount: intPtr(12), source: "catalog"},
		{name: "dataset zero", kind: "true", category: "dataset", count: intPtr(0), stamp: intPtr(100), wantCount: intPtr(0), source: "index"},
		{name: "missing measurement", kind: "true", category: "table"},
		{name: "untimestamped legacy count", kind: "true", category: "table", count: intPtr(999)},
		{name: "negative measurement", kind: "true", category: "table", count: intPtr(-1), stamp: intPtr(100)},
		{name: "filtered", kind: "in", count: intPtr(999), stamp: intPtr(100), queryValue: json.Number("2"), wantCount: intPtr(2), source: "query"},
		{name: "filtered zero", kind: "false", queryValue: json.Number("0"), wantCount: intPtr(0), source: "query"},
		{name: "large count", kind: "in", queryValue: json.Number("9007199254740993"), wantCount: intPtr(9007199254740993), source: "query"},
		{name: "invalid query count", kind: "in", queryValue: json.Number("1.5")},
		{name: "query failure", kind: "in", queryError: errors.New("private physical table")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ps, ots, cqs, _ := fixture(t, object("orders"))
			ps.EXPECT().ResolveRowFilters(gomock.Any(), []string{"kn/orders"}).Return([]interfaces.RowFilterDecisionEntry{decision("orders", tc.kind)}, nil)
			if tc.kind == "true" {
				ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"orders": {Category: tc.category, RowCount: tc.count, RowCountTime: tc.stamp}})
			} else {
				cqs.EXPECT().Query(gomock.Any(), interfaces.CypherQuery{KNID: "kn", Branch: "main", Query: "MATCH (t:`orders`) RETURN count(*) AS n"}).Return(&interfaces.CypherQueryResult{Entries: []map[string]any{{"n": tc.queryValue}}}, tc.queryError)
			}
			entry := run(t, s).Entries[0]
			if tc.wantCount == nil {
				if entry.Count != nil {
					t.Fatalf("unexpected count: %+v", entry)
				}
				if tc.kind != "true" && (entry.Error == nil || entry.Error.Message == "") {
					t.Fatalf("missing row error: %+v", entry)
				}
				return
			}
			if entry.Error != nil || entry.Count == nil || *entry.Count != *tc.wantCount || entry.Source != tc.source || !entry.Exact {
				t.Fatalf("unexpected count: %+v", entry)
			}
			if tc.source != "query" && entry.AsOf != *tc.stamp {
				t.Fatal("measurement timestamp changed")
			}
		})
	}
}

func TestStatsAuthorizationAndPartialResults(t *testing.T) {
	s, ps, ots, _, ops := fixture(t, object("open"), object("viewonly"), object("hidden"))
	ops["kn/viewonly"] = []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}
	delete(ops, "kn/hidden")
	ps.EXPECT().ResolveRowFilters(gomock.Any(), []string{"kn/open"}).Return([]interfaces.RowFilterDecisionEntry{decision("open", "true")}, nil).Times(2)
	ots.EXPECT().GetObjectTypeResources(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.VegaResource{"open": {RowCount: intPtr(4), RowCountTime: intPtr(100)}}).Times(2)
	entries := run(t, s, "open", "viewonly", "hidden", "missing").Entries
	if entries[0].Count == nil || *entries[0].Count != 4 || entries[1].Error.Code != httperrors.PublicError_Forbidden || entries[2].Error.Code != berrors.BknBackend_ObjectType_ObjectTypeNotFound || entries[3].Error.Code != entries[2].Error.Code {
		t.Fatalf("unexpected authorization results: %+v", entries)
	}
	for _, entry := range entries[1:] {
		if entry.Count != nil || entry.Error.Message == "" {
			t.Fatalf("count leaked or error untranslated: %+v", entry)
		}
	}
	result := run(t, s)
	if result.Total != 2 {
		t.Fatalf("hidden object counted: %+v", result)
	}
}

func TestStatsMissingOrDuplicatePoliciesFailClosed(t *testing.T) {
	for _, entries := range [][]interfaces.RowFilterDecisionEntry{nil, {decision("orders", "true"), decision("orders", "true")}} {
		s, ps, _, _, _ := fixture(t, object("orders"))
		ps.EXPECT().ResolveRowFilters(gomock.Any(), gomock.Any()).Return(entries, nil)
		entry := run(t, s).Entries[0]
		if entry.Count != nil || entry.Error == nil {
			t.Fatalf("policy ambiguity exposed a count: %+v", entry)
		}
	}
	s, ps, _, _, _ := fixture(t, object("orders"))
	ps.EXPECT().ResolveRowFilters(gomock.Any(), gomock.Any()).Return(nil, errors.New("permission service down"))
	if entry := run(t, s).Entries[0]; entry.Count != nil || entry.Error == nil {
		t.Fatal("policy failure exposed a count")
	}
}

func TestStatsUnboundObject(t *testing.T) {
	ot := object("orders")
	ot.DataSource = nil
	s, _, _, _, _ := fixture(t, ot)
	if entry := run(t, s).Entries[0]; entry.Error == nil || entry.Error.Code != berrors.BknBackend_ObjectType_NoDataSource {
		t.Fatalf("unexpected result: %+v", entry)
	}
}

func TestStatsFilteredConcurrency(t *testing.T) {
	var objects []*interfaces.ObjectType
	var decisions []interfaces.RowFilterDecisionEntry
	for i := range 6 {
		id := fmt.Sprint(i)
		objects = append(objects, object(id))
		decisions = append(decisions, decision(id, "in"))
	}
	s, ps, _, cqs, _ := fixture(t, objects...)
	ps.EXPECT().ResolveRowFilters(gomock.Any(), gomock.Any()).Return(decisions, nil)
	started := make(chan struct{}, 6)
	release := make(chan struct{})
	var active, peak atomic.Int32
	cqs.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return &interfaces.CypherQueryResult{Entries: []map[string]any{{"n": 1}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}).Times(6)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, err := s.ObjectTypeStats(ctx, interfaces.ObjectTypeStatsQuery{KNID: "kn"}); done <- err }()
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not start")
		}
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("counts did not complete")
	}
	if peak.Load() > 4 {
		t.Fatalf("concurrency=%d", peak.Load())
	}
}
