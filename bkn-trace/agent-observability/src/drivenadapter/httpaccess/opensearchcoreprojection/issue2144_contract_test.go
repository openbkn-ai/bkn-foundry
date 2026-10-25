// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"testing"
)

// Batch search arrives newest-first. Root selection must not depend on input order.
func TestIssue2144CanonicalRootReceiptTieContract(t *testing.T) {
	a := receiptDocument{ReceiptID: "receipt-a", TraceID: "trace", RequestID: "req", ToolName: "root-tool", IssuedAt: "2026-10-23T00:00:00Z"}
	b := a
	b.ReceiptID = "receipt-b"
	b.ToolName = "child-tool"
	for _, receipts := range [][]receiptDocument{{b, a}, {a, b}} {
		_, summaries := evidencevo.BuildExecutionSummaries(tracesFromReceipts(receipts), nil)
		if len(summaries) != 1 || summaries[0].RootOperation != "root-tool" {
			t.Errorf("canonical root must be earliest issued_at then receipt ID, got %+v", summaries)
		}
	}
}
