// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestHasAvailableLocalIndex(t *testing.T) {
	assert.False(t, HasAvailableLocalIndex(nil))
	assert.False(t, HasAvailableLocalIndex(&interfaces.Resource{
		LocalIndexName:   "vega-build-abc",
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusStale,
	}))
	assert.False(t, HasAvailableLocalIndex(&interfaces.Resource{
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
	}))
	assert.True(t, HasAvailableLocalIndex(&interfaces.Resource{
		LocalIndexName:   "vega-build-abc",
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
	}))
}

func TestShouldIgnoreLocalIndex(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		params   *interfaces.ResourceDataQueryParams
		want     bool
	}{
		{name: "no query", category: interfaces.ResourceCategoryTable},
		{name: "ordinary table query", category: interfaces.ResourceCategoryTable, params: &interfaces.ResourceDataQueryParams{}},
		{name: "explicit bypass", category: interfaces.ResourceCategoryTable, params: &interfaces.ResourceDataQueryParams{IgnoreLocalIndex: new(true)}, want: true},
		{name: "exact table", category: interfaces.ResourceCategoryTable, params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "count_distinct", AllowApproximate: new(false)}}, want: true},
		{name: "index has no separate source", category: interfaces.ResourceCategoryIndex, params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "count_distinct", AllowApproximate: new(false)}}},
		{name: "dataset has no separate source", category: interfaces.ResourceCategoryDataset, params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "count_distinct", AllowApproximate: new(false)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ShouldIgnoreLocalIndex(&interfaces.Resource{Category: tc.category}, tc.params))
		})
	}
}

func TestEnsureResourceQueryable(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name        string
		resource    *interfaces.Resource
		wantWarn    bool
		wantErr     bool
		wantErrCode string
	}{
		{name: "nil resource passes", resource: nil},
		{
			name:     "active passes silently",
			resource: &interfaces.Resource{ID: "r1", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		},
		{
			name:     "deprecated warns",
			resource: &interfaces.Resource{ID: "r1", Name: "n1", Enabled: true, Status: interfaces.ResourceStatusDeprecated, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
			wantWarn: true,
		},
		{
			name:        "disabled blocks",
			resource:    &interfaces.Resource{ID: "r1", Enabled: false},
			wantErr:     true,
			wantErrCode: verrors.VegaBackend_Resource_NotQueryable,
		},
		{
			name:        "stale blocks",
			resource:    &interfaces.Resource{ID: "r1", Status: interfaces.ResourceStatusStale},
			wantErr:     true,
			wantErrCode: verrors.VegaBackend_Resource_NotQueryable,
		},
		{
			name: "stale takes precedence over missing",
			resource: &interfaces.Resource{
				ID:                 "r1",
				Enabled:            true,
				Status:             interfaces.ResourceStatusStale,
				LastDiscoverStatus: interfaces.DiscoverStatusMissing,
				SchemaDefinition:   []*interfaces.Property{{Name: "id"}},
			},
			wantErr:     true,
			wantErrCode: verrors.VegaBackend_Resource_NotQueryable,
		},
		{
			name: "disabled takes precedence over missing",
			resource: &interfaces.Resource{
				ID:                 "r1",
				Enabled:            false,
				Status:             interfaces.ResourceStatusActive,
				LastDiscoverStatus: interfaces.DiscoverStatusMissing,
				SchemaDefinition:   []*interfaces.Property{{Name: "id"}},
			},
			wantErr:     true,
			wantErrCode: verrors.VegaBackend_Resource_NotQueryable,
		},
		{
			name:     "unknown status passes (forward compat)",
			resource: &interfaces.Resource{ID: "r1", Enabled: true, Status: "unknown_future_status", SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := EnsureResourceQueryable(ctx, tc.resource)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				var he *httperrors.HTTPError
				if !errors.As(err, &he) {
					t.Fatalf("expected *httperrors.HTTPError, got %T", err)
				}
				if he.HTTPCode != http.StatusConflict {
					t.Errorf("expected status 409, got %d", he.HTTPCode)
				}
				if he.ErrorCode != tc.wantErrCode {
					t.Errorf("expected error code %q, got %q", tc.wantErrCode, he.ErrorCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantWarn && w == "" {
				t.Errorf("expected warning, got empty")
			}
			if !tc.wantWarn && w != "" {
				t.Errorf("expected no warning, got %q", w)
			}
		})
	}
}

func TestEnsureResourcesQueryable(t *testing.T) {
	ctx := context.Background()

	t.Run("all active produces no warnings", func(t *testing.T) {
		ws, err := EnsureResourcesQueryable(ctx, []*interfaces.Resource{
			{ID: "a", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
			{ID: "b", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ws) != 0 {
			t.Errorf("expected no warnings, got %v", ws)
		}
	})

	t.Run("mixed active + deprecated returns deprecated warning", func(t *testing.T) {
		ws, err := EnsureResourcesQueryable(ctx, []*interfaces.Resource{
			{ID: "a", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
			{ID: "b", Name: "n", Enabled: true, Status: interfaces.ResourceStatusDeprecated, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ws) != 1 {
			t.Fatalf("expected 1 warning, got %d (%v)", len(ws), ws)
		}
	})

	t.Run("any disabled in slice fails fast", func(t *testing.T) {
		_, err := EnsureResourcesQueryable(ctx, []*interfaces.Resource{
			{ID: "a", Enabled: true, Status: interfaces.ResourceStatusActive},
			{ID: "b", Enabled: false, Status: interfaces.ResourceStatusActive},
			{ID: "c", Enabled: true, Status: interfaces.ResourceStatusActive},
		})
		if err == nil {
			t.Fatal("expected error from disabled resource")
		}
	})

	t.Run("stale also blocks", func(t *testing.T) {
		_, err := EnsureResourcesQueryable(ctx, []*interfaces.Resource{
			{ID: "a", Status: interfaces.ResourceStatusStale},
		})
		if err == nil {
			t.Fatal("expected error from stale resource")
		}
	})
}
