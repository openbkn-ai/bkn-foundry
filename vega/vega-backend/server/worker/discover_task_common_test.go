// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/require"
)

func TestDiscoverStatusAfterEnrichPreservesExactCount(t *testing.T) {
	for _, count := range []any{int64(9007199254740993), json.Number("9007199254740993")} {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{
			"properties": map[string]any{"row_count": count, "row_count_time": int64(100)},
		}}
		beforeHash := sourceSnapshotHash(resource)

		status := discoverStatusAfterEnrich(resource, beforeHash)

		require.Equal(t, interfaces.DiscoverStatusUnchanged, status)
		require.Equal(t, beforeHash, sourceSnapshotHash(resource))
		properties := resource.SourceMetadata["properties"].(map[string]any)
		require.Equal(t, json.Number("9007199254740993"), properties["row_count"])
		require.Equal(t, json.Number("100"), properties["row_count_time"])
	}
}

func TestSourceSnapshotHash(t *testing.T) {
	properties := map[string]any{"row_count": int64(42), "row_count_time": int64(100)}
	resource := &interfaces.Resource{SourceMetadata: map[string]any{"properties": properties}}
	before := sourceSnapshotHash(resource)
	properties["row_count_time"] = int64(200)
	require.Equal(t, before, sourceSnapshotHash(resource), "collection time alone is not a source change")
	require.Equal(t, int64(200), properties["row_count_time"], "hashing must not mutate metadata")
	properties["row_count"] = int64(43)
	require.NotEqual(t, before, sourceSnapshotHash(resource))
}

func TestDiscoveredProperties(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fresh     map[string]any
		count     any
		countTime int64
		estimate  int64
	}{
		{"estimate refresh", map[string]any{"estimated_row_count": int64(50)}, int64(42), 100, 50},
		{"empty properties", nil, int64(42), 100, 40},
		{"fresh zero", map[string]any{"row_count": int64(0)}, int64(0), 100, 40},
		{"explicit count and time", map[string]any{"row_count": int64(0), "row_count_time": int64(200)}, int64(0), 200, 40},
		{"explicit nil", map[string]any{"row_count": nil, "row_count_time": int64(999)}, nil, 999, 40},
	} {
		t.Run(tt.name, func(t *testing.T) {
			previous := map[string]any{"row_count": int64(42), "row_count_time": int64(100), "estimated_row_count": int64(40), "source_setting": "retained"}
			got := discoveredProperties(previous, tt.fresh)
			require.Equal(t, tt.count, got["row_count"])
			require.Equal(t, tt.countTime, got["row_count_time"])
			require.Equal(t, tt.estimate, got["estimated_row_count"])
			require.Equal(t, "retained", got["source_setting"])
		})
	}
}

func TestCountOnlyActions(t *testing.T) {
	for _, tt := range []struct {
		name    string
		actions *interfaces.DiscoverActions
		want    bool
	}{
		{"nil", nil, false},
		{"none", &interfaces.DiscoverActions{}, false},
		{"count", &interfaces.DiscoverActions{Count: true}, true},
		{"full sync and count", &interfaces.DiscoverActions{Create: true, Refresh: true, MarkStale: true, Count: true}, false},
		{"create and count", &interfaces.DiscoverActions{Create: true, Count: true}, false},
		{"refresh and count", &interfaces.DiscoverActions{Refresh: true, Count: true}, false},
		{"cleanup and count", &interfaces.DiscoverActions{MarkStale: true, Count: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, countOnlyActions(tt.actions)) })
	}
}
