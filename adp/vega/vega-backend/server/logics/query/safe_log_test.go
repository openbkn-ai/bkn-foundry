package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeQuerySummaryDoesNotLeakRawSQL(t *testing.T) {
	rawSQL := "select * from customers where email = 'alice@example.com'"

	summary := SafeQuerySummary(rawSQL)

	require.Contains(t, summary, "query_hash=")
	require.Contains(t, summary, "query_length=")
	require.NotContains(t, summary, rawSQL)
	require.NotContains(t, strings.ToLower(summary), "select * from")
	require.NotContains(t, summary, "alice@example.com")
}

func TestSafeQuerySummaryHandlesStructuredQuery(t *testing.T) {
	summary := SafeQuerySummary(map[string]any{
		"query": map[string]any{"match": map[string]any{"email": "alice@example.com"}},
	})

	require.Contains(t, summary, "query_hash=")
	require.Contains(t, summary, "query_type=structured")
	require.NotContains(t, summary, "alice@example.com")
}
