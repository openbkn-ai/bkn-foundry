// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"testing"
	"time"
)

func TestRecordIntegrityRecognizesExplicitUnrecordedCallGap(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.Operations = nil
	snapshot.CallFacts = nil
	snapshot.Receipts = nil
	deadline := now.Add(time.Hour)
	snapshot.Interaction.ClosureManifest = &sessionvo.ClosureManifest{Version: "3.0.0", AssemblerDeadline: &deadline, SystemPartialReasons: []string{"trace_call_unrecorded:execute_tool:request-gap"}}
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report == nil || report.Status != "missing" || len(report.Missing) != 1 || report.Missing[0].Reason != "call_outcome_missing" || report.Missing[0].ToolName != "execute_tool" || report.Missing[0].Field != "request_id:request-gap" || report.Missing[0].OperationID != "" || report.Missing[0].Attempt != 0 {
		t.Fatalf("explicit unregistered call incorrectly complete/hidden: %#v %v", report, err)
	}
}
