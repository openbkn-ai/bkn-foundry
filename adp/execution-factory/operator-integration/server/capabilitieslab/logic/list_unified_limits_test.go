// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/client"
)

func TestPageCapacityIsBounded(t *testing.T) {
	cases := map[int]int{
		-5:              0,
		0:               0,
		20:              20,
		maxListPageSize: maxListPageSize,
		math.MaxInt:     maxListPageSize,
	}
	for in, want := range cases {
		if got := pageCapacity(in); got != want {
			t.Errorf("pageCapacity(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestAllKindWindowDoesNotOverflow(t *testing.T) {
	cases := []struct{ page, pageSize, want int }{
		{1, 20, 20},
		{3, 100, 300},
		{4, 100, maxAllKindWindow},
		{math.MaxInt, 100, maxAllKindWindow}, // page*pageSize would overflow to a negative window.
		{math.MaxInt / 2, 3, maxAllKindWindow},
		{1, 0, maxAllKindWindow},
	}
	for _, c := range cases {
		if got := allKindWindow(c.page, c.pageSize); got != c.want {
			t.Errorf("allKindWindow(%d, %d) = %d, want %d", c.page, c.pageSize, got, c.want)
		}
	}
}

// An abusive page_size must not size an allocation or reach the upstream list call unbounded; a
// valid request keeps working.
func TestListCapabilitiesClampsAbusivePageSize(t *testing.T) {
	var mu sync.Mutex
	var upstreamPageSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tool-box/list"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 1,
				"data":  []client.ToolboxInfo{{BoxID: "box-1", BoxName: "box", MetadataType: "openapi", Tools: []string{"t1"}}},
			})
		case strings.HasSuffix(r.URL.Path, "/tools/list"):
			size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
			mu.Lock()
			upstreamPageSizes = append(upstreamPageSizes, size)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 1,
				"tools": []client.ToolInfo{{ToolID: "t1", Name: "tool", Status: "enabled"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	svc := &Service{Client: client.NewOperatorIntegrationClient(srv.URL)}
	resp, err := svc.ListCapabilities(context.Background(), "http", "", "", "", math.MaxInt, math.MaxInt)
	if err != nil {
		t.Fatalf("ListCapabilities returned error: %v", err)
	}
	if resp.PageSize != maxListPageSize {
		t.Fatalf("PageSize = %d, want clamped to %d", resp.PageSize, maxListPageSize)
	}
	for _, size := range upstreamPageSizes {
		if size > maxListPageSize {
			t.Fatalf("upstream page_size = %d, want <= %d", size, maxListPageSize)
		}
	}

	resp, err = svc.ListCapabilities(context.Background(), "http", "", "", "", 1, 20)
	if err != nil {
		t.Fatalf("valid ListCapabilities returned error: %v", err)
	}
	if len(resp.Data) != 1 || resp.PageSize != 20 {
		t.Fatalf("valid request = %d items, page_size %d; want 1 item, page_size 20", len(resp.Data), resp.PageSize)
	}
}
