// Copyright 2026 kowell.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "testing"

func TestIsValidDiscoverStrategy(t *testing.T) {
	for _, strategy := range []string{
		DiscoverStrategyFullSync,
		DiscoverStrategyCreateOnly,
		DiscoverStrategyCleanupOnly,
	} {
		if !IsValidDiscoverStrategy(strategy) {
			t.Fatalf("expected strategy %q to be valid", strategy)
		}
	}

	if IsValidDiscoverStrategy("unknown") {
		t.Fatal("expected unknown strategy to be invalid")
	}
	if IsValidDiscoverStrategy("") {
		t.Fatal("expected empty strategy to be invalid before driver normalization")
	}
}

func TestActionsFromDiscoverStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		want     DiscoverActions
	}{
		{
			name:     "full sync",
			strategy: DiscoverStrategyFullSync,
			want:     DiscoverActions{Create: true, Refresh: true, MarkStale: true},
		},
		{
			name:     "empty defaults to full sync",
			strategy: "",
			want:     DiscoverActions{Create: true, Refresh: true, MarkStale: true},
		},
		{
			name:     "create only",
			strategy: DiscoverStrategyCreateOnly,
			want:     DiscoverActions{Create: true},
		},
		{
			name:     "cleanup only",
			strategy: DiscoverStrategyCleanupOnly,
			want:     DiscoverActions{MarkStale: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ActionsFromDiscoverStrategy(tt.strategy); got != tt.want {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}
