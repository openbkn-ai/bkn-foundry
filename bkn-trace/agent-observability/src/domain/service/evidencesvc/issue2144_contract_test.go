// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"testing"
)

// Only the supported Tool subset may enter canonical identity paging.
func TestIssue2144ToolIdentityEligibilityContract(t *testing.T) {
	for _, service := range []string{"", "root-service"} {
		options := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Tool: "target-tool", Service: service}
		if !canUseTraceSummaryIdentityPage(options) {
			t.Errorf("Tool with Service=%q must use canonical identity paging", service)
		}
		options.Status = "error"
		if canUseTraceSummaryIdentityPage(options) {
			t.Error("status combination must remain out of this subset")
		}
		options.Status = ""
		options.ErrorKeyword = "failed"
		if canUseTraceSummaryIdentityPage(options) {
			t.Error("error keyword combination must remain out of this subset")
		}
	}
}
