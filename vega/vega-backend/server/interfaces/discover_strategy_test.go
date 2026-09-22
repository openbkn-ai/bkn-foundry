// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidDiscoverStrategy(t *testing.T) {
	t.Run("valid strategies", func(t *testing.T) {
		for _, strategy := range []string{
			DiscoverStrategyFullSync,
			DiscoverStrategyCreateOnly,
			DiscoverStrategyCleanupOnly,
		} {
			assert.True(t, IsValidDiscoverStrategy(strategy), "strategy %q should be valid", strategy)
		}
	})

	t.Run("invalid strategies", func(t *testing.T) {
		assert.False(t, IsValidDiscoverStrategy("unknown"))
		assert.False(t, IsValidDiscoverStrategy(""))
	})
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
			assert.Equal(t, tt.want, ActionsFromDiscoverStrategy(tt.strategy))
		})
	}
}
