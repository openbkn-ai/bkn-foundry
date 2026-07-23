package safelog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSQLSummaryDoesNotLeakSQLOrArgs(t *testing.T) {
	sql := "SELECT email, phone FROM customer WHERE email = ? AND tenant_id = ?"
	args := []any{"alice@example.com", "tenant-secret"}

	summary := SQLSummary(sql, args)

	assert.Contains(t, summary, "query_hash=")
	assert.Contains(t, summary, "query_length=")
	assert.Contains(t, summary, "args_count=2")
	assert.NotContains(t, summary, sql)
	assert.NotContains(t, strings.ToLower(summary), "select email")
	assert.NotContains(t, summary, "alice@example.com")
	assert.NotContains(t, summary, "tenant-secret")
}
