// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

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
