package sessionstore

import (
	"strings"
	"testing"
)

func TestListInteractionsOrdersByPersistedOrdinalColumn(t *testing.T) {
	t.Parallel()

	if !strings.Contains(listInteractionsOrderBy, "ordinal_no") {
		t.Fatalf("interaction list order must use persisted ordinal_no column: %q", listInteractionsOrderBy)
	}
	if strings.Contains(listInteractionsOrderBy, "ORDER BY ordinal ") {
		t.Fatalf("interaction list order references nonexistent ordinal column: %q", listInteractionsOrderBy)
	}
}
