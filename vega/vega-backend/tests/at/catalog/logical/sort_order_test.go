// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logical

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	logicalhelpers "vega-backend-tests/at/catalog/logical/helpers"
	"vega-backend-tests/at/setup"
	"vega-backend-tests/testutil"
)

// TestLogicalCatalogListSortOrder verifies sorting after authorization and summary lookup against a running service and its real database.
func TestLogicalCatalogListSortOrder(t *testing.T) {
	config, err := setup.LoadTestConfig()
	if err != nil {
		t.Fatal(err)
	}
	client := testutil.NewHTTPClient(config.VegaBackend.BaseURL)
	if err := client.CheckHealth(); err != nil {
		t.Fatal(err)
	}

	const catalogCount = 110
	prefix := fmt.Sprintf("sort-order-%d-", time.Now().UnixNano())
	createdIDs := make([]string, 0, catalogCount)
	t.Cleanup(func() {
		for _, id := range createdIDs {
			resp := client.DELETE("/api/vega-backend/v1/catalogs/" + id)
			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				t.Errorf("delete fixture catalog %s: status %d", id, resp.StatusCode)
			}
		}
	})

	// Create in reverse name order so creation time cannot accidentally satisfy name asc.
	for i := catalogCount - 1; i >= 0; i-- {
		name := fmt.Sprintf("%s%03d", prefix, i)
		resp := client.POST("/api/vega-backend/v1/catalogs", logicalhelpers.BuildLogicalCatalogPayloadWithName(name))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create fixture catalog %s: status %d", name, resp.StatusCode)
		}
		id, ok := resp.Body["id"].(string)
		if !ok || id == "" {
			t.Fatalf("create fixture catalog %s: missing id", name)
		}
		createdIDs = append(createdIDs, id)
	}

	readNames := func(t *testing.T, direction string, offset, limit int, nameFilter string) []string {
		t.Helper()
		query := url.Values{
			"type":      {"logical"},
			"name":      {nameFilter},
			"sort":      {"name"},
			"direction": {direction},
			"offset":    {fmt.Sprint(offset)},
			"limit":     {fmt.Sprint(limit)},
		}
		resp := client.GET("/api/vega-backend/v1/catalogs?" + query.Encode())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list fixture catalogs: status %d", resp.StatusCode)
		}
		entries, ok := resp.Body["entries"].([]any)
		if !ok {
			t.Fatalf("list fixture catalogs: invalid entries %T", resp.Body["entries"])
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			catalog, ok := entry.(map[string]any)
			if !ok {
				t.Fatalf("list fixture catalogs: invalid entry %T", entry)
			}
			name, ok := catalog["name"].(string)
			if !ok {
				t.Fatalf("list fixture catalogs: invalid name %T", catalog["name"])
			}
			names = append(names, name)
		}
		return names
	}

	wantAsc := make([]string, catalogCount)
	wantDesc := make([]string, catalogCount)
	for i := range wantAsc {
		wantAsc[i] = fmt.Sprintf("%s%03d", prefix, i)
	}
	for i := range wantDesc {
		wantDesc[i] = wantAsc[catalogCount-1-i]
	}
	for _, tc := range []struct {
		name       string
		direction  string
		offset     int
		limit      int
		nameFilter string
		want       []string
	}{
		{"name asc first page", "asc", 0, 100, prefix, wantAsc[:100]},
		{"name asc second page", "asc", 100, 100, prefix, wantAsc[100:]},
		{"name desc first page", "desc", 0, 100, prefix, wantDesc[:100]},
		{"name desc second page", "desc", 100, 100, prefix, wantDesc[100:]},
		{"name asc search", "asc", 0, 100, prefix + "10", wantAsc[100:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readNames(t, tc.direction, tc.offset, tc.limit, tc.nameFilter)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("catalog order: got %v, want %v", got, tc.want)
			}
		})
	}
}
