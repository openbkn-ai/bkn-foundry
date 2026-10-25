// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"strings"
)

func (tx memoryTransaction) listRootToolsByTraceIDs(traceIDs []string, scope evidencevo.QueryScope) map[string]string {
	requested := map[string]bool{}
	for _, id := range traceIDs {
		if id != "" {
			requested[id] = true
		}
	}
	roots := map[string]sessionvo.Receipt{}
	for _, r := range tx.s.receipts {
		if !requested[r.TraceID] || r.RequestID == "" {
			continue
		}
		readable := string(r.Owner.EffectiveSubjectType) == scope.AccountType && r.Owner.EffectiveSubjectID == scope.AccountID
		if scope.AccessProfile != nil {
			readable = evidencevo.CanReadRecord(*scope.AccessProfile, evidencevo.RecordScope{EffectiveSubjectID: r.Owner.EffectiveSubjectID, ApplicationPrincipalID: r.Owner.ApplicationPrincipalID}, evidencevo.AccessViewBusiness)
		}
		if !readable {
			continue
		}
		old, ok := roots[r.TraceID]
		blank, oldBlank := strings.TrimSpace(r.ToolName) == "", strings.TrimSpace(old.ToolName) == ""
		if !ok || (!blank && oldBlank) || blank == oldBlank && (r.IssuedAt.Before(old.IssuedAt) || r.IssuedAt.Equal(old.IssuedAt) && r.ID < old.ID) {
			roots[r.TraceID] = r
		}
	}
	result := make(map[string]string, len(roots))
	for id, r := range roots {
		result[id] = strings.TrimSpace(r.ToolName)
	}
	return result
}

func (tx memoryTransaction) ListCanonicalTraceRootsByTraceIDs(ids []string, scope evidencevo.QueryScope) map[string]isessionstore.CanonicalTraceRoot {
	modules := tx.ListFirstOperationSourceModulesByTraceIDs(ids)
	tools := tx.listRootToolsByTraceIDs(ids, scope)
	result := map[string]isessionstore.CanonicalTraceRoot{}
	for _, id := range ids {
		if id != "" {
			tool, found := tools[id]
			result[id] = isessionstore.CanonicalTraceRoot{SourceModule: modules[id], ToolName: tool, HasTool: found}
		}
	}
	return result
}
