// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"net/http"
	"testing"

	"github.com/kweaver-ai/kweaver-go-lib/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

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
			resource: &interfaces.Resource{ID: "r1", Status: interfaces.ResourceStatusActive},
		},
		{
			name:     "deprecated warns",
			resource: &interfaces.Resource{ID: "r1", Name: "n1", Status: interfaces.ResourceStatusDeprecated},
			wantWarn: true,
		},
		{
			name:        "disabled blocks",
			resource:    &interfaces.Resource{ID: "r1", Status: interfaces.ResourceStatusDisabled},
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
			name:     "unknown status passes (forward compat)",
			resource: &interfaces.Resource{ID: "r1", Status: "unknown_future_status"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := EnsureResourceQueryable(ctx, tc.resource)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				he, ok := err.(*rest.HTTPError)
				if !ok {
					t.Fatalf("expected *rest.HTTPError, got %T", err)
				}
				if he.HTTPCode != http.StatusConflict {
					t.Errorf("expected status 409, got %d", he.HTTPCode)
				}
				if he.BaseError.ErrorCode != tc.wantErrCode {
					t.Errorf("expected error code %q, got %q", tc.wantErrCode, he.BaseError.ErrorCode)
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
			{ID: "a", Status: interfaces.ResourceStatusActive},
			{ID: "b", Status: interfaces.ResourceStatusActive},
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
			{ID: "a", Status: interfaces.ResourceStatusActive},
			{ID: "b", Name: "n", Status: interfaces.ResourceStatusDeprecated},
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
			{ID: "a", Status: interfaces.ResourceStatusActive},
			{ID: "b", Status: interfaces.ResourceStatusDisabled},
			{ID: "c", Status: interfaces.ResourceStatusActive},
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
