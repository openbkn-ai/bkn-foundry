// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics"
)

// safeStub 是一个可编程的 bkn-safe 替身，记录同步器实际发了什么。
type safeStub struct {
	previewTotal int
	pushed       [][]string // 每次正式推送的 resource_id
	deleted      []string   // 被清理的孤儿 id
	stored       []string   // 对端已存的 resource_id，供对账读回
	notFound     bool       // 模拟老版本 bkn-safe：端点不存在
}

func (s *safeStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/authz/resource-parents", func(w http.ResponseWriter, r *http.Request) {
		if s.notFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodPut:
			var body struct {
				Items []struct {
					ResourceID string `json:"resource_id"`
				} `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			ids := make([]string, 0, len(body.Items))
			for _, it := range body.Items {
				ids = append(ids, it.ResourceID)
			}
			if r.URL.Query().Get("dry_run") == "true" {
				resp := map[string]any{"total": s.previewTotal, "changes": []any{}}
				if s.previewTotal > 0 {
					resp["changes"] = []map[string]string{{
						"accessor_id": "u-1", "resource_id": ids[0],
						"operation": "modify", "direction": "grant",
					}}
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			s.pushed = append(s.pushed, ids)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			var body struct {
				ResourceIDs []string `json:"resource_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.deleted = append(s.deleted, body.ResourceIDs...)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			items := make([]map[string]string, 0, len(s.stored))
			for _, id := range s.stored {
				items = append(items, map[string]string{"resource_id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": len(items)})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newSyncWorker(baseURL string, approved bool) *OwnershipSyncWorker {
	return &OwnershipSyncWorker{
		baseURL:  baseURL,
		http:     &http.Client{Timeout: 5 * time.Second},
		interval: time.Hour,
		approved: approved,
		stop:     make(chan struct{}),
	}
}

// TestSyncPushesWhenPreviewIsClean 是常态：装机环境没有目录级的对象授权，预演
// 差异为空，推送即零行为变化，不需要任何人点头。
func TestSyncPushesWhenPreviewIsClean(t *testing.T) {
	stub := &safeStub{previewTotal: 0}
	srv := stub.server(t)
	w := newSyncWorker(srv.URL, false)

	links := []ownershipLink{{ResourceID: "r-1", ParentID: "c-1"}, {ResourceID: "r-2", ParentID: "c-1"}}
	if err := w.pushSnapshot(context.Background(), links); err != nil {
		t.Fatal(err)
	}
	if len(stub.pushed) != 1 || len(stub.pushed[0]) != 2 {
		t.Fatalf("pushed = %v, want one chunk of two", stub.pushed)
	}
}

// TestSyncStopsWhenPreviewShowsChanges 是这个设计的关键动作：预演非空说明有人的
// 可见范围会变，同步器停下把决定权交回给人，而不是替人做决定。
func TestSyncStopsWhenPreviewShowsChanges(t *testing.T) {
	stub := &safeStub{previewTotal: 7}
	srv := stub.server(t)
	w := newSyncWorker(srv.URL, false)

	links := []ownershipLink{{ResourceID: "r-1", ParentID: "c-1"}}
	if err := w.pushSnapshot(context.Background(), links); err != nil {
		t.Fatalf("暂停不是错误，不该返回 err: %v", err)
	}
	if len(stub.pushed) != 0 {
		t.Errorf("差异非空却推了: %v", stub.pushed)
	}
}

// TestSyncPushesOnceApproved: 批准之后同一份快照照推。批准是一次人工确认的
// 记录，不是判定开关。
func TestSyncPushesOnceApproved(t *testing.T) {
	stub := &safeStub{previewTotal: 7}
	srv := stub.server(t)
	w := newSyncWorker(srv.URL, true)

	links := []ownershipLink{{ResourceID: "r-1", ParentID: "c-1"}}
	if err := w.pushSnapshot(context.Background(), links); err != nil {
		t.Fatal(err)
	}
	if len(stub.pushed) != 1 {
		t.Errorf("批准后仍未推送: %v", stub.pushed)
	}
}

// TestSyncReconcilesOrphans: 删除路径会漏（进程崩溃、跨服务调用失败），对账才是
// 可靠手段。vega 已经没有的资源，它在对端留下的归属行要清掉。
func TestSyncReconcilesOrphans(t *testing.T) {
	stub := &safeStub{previewTotal: 0, stored: []string{"r-1", "r-gone"}}
	srv := stub.server(t)
	w := newSyncWorker(srv.URL, false)

	links := []ownershipLink{{ResourceID: "r-1", ParentID: "c-1"}}
	if err := w.pushSnapshot(context.Background(), links); err != nil {
		t.Fatal(err)
	}
	if len(stub.deleted) != 1 || stub.deleted[0] != "r-gone" {
		t.Errorf("deleted = %v, want only r-gone", stub.deleted)
	}
}

// TestSyncReconcilesWithAnEmptySnapshot: 本地一个资源都没有时仍要对账，否则
// 清空所有资源之后，对端的归属行永远留着。
func TestSyncReconcilesWithAnEmptySnapshot(t *testing.T) {
	stub := &safeStub{stored: []string{"r-gone"}}
	srv := stub.server(t)
	w := newSyncWorker(srv.URL, false)

	if err := w.pushSnapshot(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.deleted) != 1 || stub.deleted[0] != "r-gone" {
		t.Errorf("deleted = %v, want r-gone", stub.deleted)
	}
}

// TestSyncToleratesAnOlderSafe: 新版 vega 配老版 bkn-safe 是允许的组合，端点
// 404 必须被识别成「对端还没有这个能力」，而不是变成一串报错。
func TestSyncToleratesAnOlderSafe(t *testing.T) {
	stub := &safeStub{notFound: true}
	srv := stub.server(t)
	w := newSyncWorker(srv.URL, false)

	err := w.pushSnapshot(context.Background(), []ownershipLink{{ResourceID: "r-1", ParentID: "c-1"}})
	if !errors.Is(err, errEndpointAbsent) {
		t.Fatalf("err = %v, want errEndpointAbsent", err)
	}
}

// TestSyncDisabledWithoutSafeURL: 授权面可能仍是 ISF，那里没有归属表这个概念。
func TestSyncDisabledWithoutSafeURL(t *testing.T) {
	w := newSyncWorker("", false)
	if err := w.Start(); err != nil {
		t.Fatalf("未配置 BKN_SAFE_URL 不该报错: %v", err)
	}
}

func TestChunkLinks(t *testing.T) {
	links := make([]ownershipLink, 5)
	for i := range links {
		links[i] = ownershipLink{ResourceID: string(rune('a' + i))}
	}
	cases := []struct {
		size  int
		chunk []int // 期望的每片长度
	}{
		{size: 2, chunk: []int{2, 2, 1}},
		{size: 5, chunk: []int{5}},
		{size: 10, chunk: []int{5}},
		{size: 0, chunk: nil},
	}
	for _, tc := range cases {
		got := chunkLinks(links, tc.size)
		if len(got) != len(tc.chunk) {
			t.Fatalf("size=%d 分成 %d 片，want %d", tc.size, len(got), len(tc.chunk))
		}
		for i, want := range tc.chunk {
			if len(got[i]) != want {
				t.Errorf("size=%d 第 %d 片长 %d，want %d", tc.size, i, len(got[i]), want)
			}
		}
	}
	if chunkLinks(nil, 3) != nil {
		t.Error("空快照应返回 nil")
	}
}

// --- 本地快照（唯一读本地库的那一段）--------------------------------------

// TestSnapshotSkipsInternalCatalogs：内部目录下的资源不能推——权限服务按
// internal_catalog / internal_resource 注册它们，种子没声明层级，推过去会 400。
func TestSnapshotSkipsInternalCatalogs(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca, ra := mock_interfaces.NewMockCatalogAccess(ctrl), mock_interfaces.NewMockResourceAccess(ctrl)
	restore := swapAccess(ca, ra)
	defer restore()

	ca.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{"c-internal"}, nil)
	ra.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return([]string{"r-1", "r-2", "r-3"}, nil)
	ra.EXPECT().GetByIDsBasic(gomock.Any(), []string{"r-1", "r-2", "r-3"}).Return([]*interfaces.Resource{
		{ID: "r-1", CatalogID: "c-1"},
		{ID: "r-2", CatalogID: "c-internal"},
		{ID: "r-3", CatalogID: ""}, // 没有目录的资源无处可挂
	}, nil)

	links, err := newSyncWorker("http://x", false).snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].ResourceID != "r-1" || links[0].ParentID != "c-1" {
		t.Fatalf("links = %v, want only r-1 under c-1", links)
	}
}

// TestSnapshotTerminatesBeyondOnePage 是这一段返工的原因：ListIDs 的实现不读
// Offset/Limit，一次返回全量。曾经的翻页循环因此每轮拿到同一批、无限追加，直到
// context 超时——同步永远走不到推送。这里用超过一页的资源量把它钉住。
func TestSnapshotTerminatesBeyondOnePage(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca, ra := mock_interfaces.NewMockCatalogAccess(ctrl), mock_interfaces.NewMockResourceAccess(ctrl)
	restore := swapAccess(ca, ra)
	defer restore()

	const n = ownershipPageSize + 7
	ids := make([]string, n)
	resources := make([]*interfaces.Resource, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("r-%d", i)
		resources[i] = &interfaces.Resource{ID: ids[i], CatalogID: "c-1"}
	}
	ca.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	// 全量一次返回,且只被问一次——多问一次就是又写回了翻页循环。
	ra.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil).Times(1)
	// 详情按批取,所以正好两批。
	ra.EXPECT().GetByIDsBasic(gomock.Any(), ids[:ownershipPageSize]).Return(resources[:ownershipPageSize], nil)
	ra.EXPECT().GetByIDsBasic(gomock.Any(), ids[ownershipPageSize:]).Return(resources[ownershipPageSize:], nil)

	done := make(chan int, 1)
	go func() {
		links, err := newSyncWorker("http://x", false).snapshot(context.Background())
		if err != nil {
			t.Error(err)
		}
		done <- len(links)
	}()
	select {
	case got := <-done:
		if got != n {
			t.Errorf("links = %d, want %d", got, n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot 没有终止——翻页循环又回来了")
	}
}

// swapAccess 临时替换 logics 里的两个访问单例，返回还原函数。
func swapAccess(ca interfaces.CatalogAccess, ra interfaces.ResourceAccess) func() {
	prevCA, prevRA := logics.CA, logics.RA
	logics.CA, logics.RA = ca, ra
	return func() { logics.CA, logics.RA = prevCA, prevRA }
}
