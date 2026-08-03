// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeQuerySummary(t *testing.T) {
	t.Run("does not leak raw SQL", func(t *testing.T) {
		rawSQL := "select * from customers where email = 'alice@example.com'"

		summary := SafeQuerySummary(rawSQL)

		require.Contains(t, summary, "query_hash=")
		require.Contains(t, summary, "query_length=")
		require.NotContains(t, summary, rawSQL)
		require.NotContains(t, strings.ToLower(summary), "select * from")
		require.NotContains(t, summary, "alice@example.com")
	})

	t.Run("handles structured query", func(t *testing.T) {
		summary := SafeQuerySummary(map[string]any{
			"query": map[string]any{"match": map[string]any{"email": "alice@example.com"}},
		})

		require.Contains(t, summary, "query_hash=")
		require.Contains(t, summary, "query_type=structured")
		require.NotContains(t, summary, "alice@example.com")
	})
}
