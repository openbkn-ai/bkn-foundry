// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"strings"
)

// RecordToolFailure retains a classification supplied by the executing handler.
// It never reads caller arguments or MCP structured content. Unmanaged calls
// have no attempt tracker and keep their original business result.
func RecordToolFailure(ctx context.Context, code, stage string) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	if !outcome.closed {
		outcome.failureCode, outcome.failureStage = code, stage
	}
}

// FreezeToolFailure closes the attempt before its classification is serialized.
// Guard.Finish reuses the same frozen evidence expectation and targets.
func FreezeToolFailure(ctx context.Context) (code, stage string) {
	freezeEvidenceExpectation(ctx)
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return "", ""
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	return outcome.failureCode, outcome.failureStage
}

// retainRunSQLTargets uses the resource IDs already parsed by the SQL service.
// Knowing the target does not imply that its evidence event reached the Ledger.
func retainRunSQLTargets(ctx context.Context, resourceIDs []string) {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return
	}
	refs := runSQLResourceRefs(resourceIDs)
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	if outcome.closed {
		return
	}
	targets := make([]BusinessRef, 0, len(refs))
	for _, ref := range refs {
		targets = append(targets, BusinessRef{RefType: "data_resource", RefID: stringValue(ref["ref_id"]), Version: "unversioned"})
	}
	outcome.businessRefs = mergeBusinessRefs(outcome.businessRefs, targets)
}

func retainedBusinessRefs(ctx context.Context) []BusinessRef {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return nil
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	return append([]BusinessRef(nil), outcome.businessRefs...)
}

// Schema snapshot IDs come from the definitions actually returned by the
// handler, not caller declarations or names. Retain them for the receipt even
// when evidence publication is disabled or fails, as for resolved SQL targets.
func retainSchemaSnapshotTargets(ctx context.Context, kind, knID string, ids []string) {
	outcome := evidenceOutcomeFromContext(ctx)
	knID = strings.TrimSpace(knID)
	if outcome == nil || knID == "" || (kind != "object" && kind != "relation") {
		return
	}
	for _, ref := range requestDerivedBusinessRefsFromContext(ctx) {
		if ref.RefType == "knowledge_network" && ref.RefID != "kn:"+knID {
			return
		}
	}
	refType := trace30RefType(kind)
	targets := make([]BusinessRef, 0, len(ids))
	for _, id := range ids {
		refID := kind + ":" + knID + ":" + strings.TrimSpace(id)
		if trace30CanonicalRefID(refType, refID) {
			targets = append(targets, BusinessRef{RefType: refType, RefID: refID, Version: "unversioned"})
		}
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	if !outcome.closed {
		outcome.businessRefs = mergeBusinessRefs(outcome.businessRefs, targets)
	}
}
