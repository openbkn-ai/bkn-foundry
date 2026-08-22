// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencestore

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

func TestMemoryStoreArtifactIsIdempotentAndRejectsConflictingContent(t *testing.T) {
	store := New()
	artifact := normalizedStoreArtifact(t)

	created, err := store.StoreArtifact(context.Background(), artifact)
	if err != nil || !created {
		t.Fatalf("first artifact store must create: created=%v err=%v", created, err)
	}
	created, err = store.StoreArtifact(context.Background(), artifact)
	if err != nil || created {
		t.Fatalf("same artifact must be idempotent: created=%v err=%v", created, err)
	}

	conflict := artifact
	conflict.Content = map[string]any{"text": "different result"}
	conflict.ContentHash = ""
	conflict, validationErrors := evidencevo.NormalizeArtifact(conflict)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize conflicting artifact: %+v", validationErrors)
	}
	created, err = store.StoreArtifact(context.Background(), conflict)
	if created || !errors.Is(err, iartifactstore.ErrArtifactIDConflict) {
		t.Fatalf("different content under same artifact_id must conflict: created=%v err=%v", created, err)
	}
}

func TestMemoryStoreArtifactQueryFailsClosedAcrossOwnership(t *testing.T) {
	store := New()
	artifact := normalizedStoreArtifact(t)
	if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}

	exact := evidencevo.QueryScope{
		TenantID: artifact.TenantID, BusinessDomain: artifact.BusinessDomain,
		AccountID: artifact.AccountID, AccountType: artifact.AccountType,
	}
	foundArtifact, found, err := store.GetArtifact(context.Background(), artifact.ArtifactID, exact)
	if err != nil || !found || foundArtifact.ContentHash != artifact.ContentHash {
		t.Fatalf("exact owner must read artifact: found=%v err=%v artifact=%+v", found, err, foundArtifact)
	}

	mismatched := exact
	mismatched.AccountID = "other-account"
	if leaked, found, err := store.GetArtifact(context.Background(), artifact.ArtifactID, mismatched); err != nil || found || leaked.ArtifactID != "" {
		t.Fatalf("cross-owner artifact query must look absent: found=%v err=%v artifact=%+v", found, err, leaked)
	}
}

func TestMemoryStoreListsOnlyAuthorizedArtifactsForRequest(t *testing.T) {
	store := New()
	owned := normalizedStoreArtifact(t)
	other := owned
	other.ArtifactID = "artifact_other_001"
	other.AccountID = "other-account"
	other, validationErrors := evidencevo.NormalizeArtifact(other)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize second artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), owned); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StoreArtifact(context.Background(), other); err != nil {
		t.Fatal(err)
	}

	result, err := store.ListArtifactsByRequestID(context.Background(), owned.RequestID, iartifactstore.QueryOptions{
		Scope: evidencevo.QueryScope{
			TenantID: owned.TenantID, BusinessDomain: owned.BusinessDomain,
			AccountID: owned.AccountID, AccountType: owned.AccountType,
		},
		Limit: iartifactstore.MaxArtifactQueryLimit,
	})

	if err != nil || len(result.Entries) != 1 || result.Entries[0].ArtifactID != owned.ArtifactID || result.Truncated {
		t.Fatalf("expected only authorized artifact, got %+v err=%v", result, err)
	}
}

func normalizedStoreArtifact(t *testing.T) evidencevo.EvidenceArtifact {
	t.Helper()
	artifact := evidencevo.EvidenceArtifact{
		ArtifactID: "artifact_result_001", ArtifactType: evidencevo.ArtifactTypeResult,
		RequestID: "req_store_001", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		InteractionID: "interaction_001", OperationID: "operation_001", ClaimID: "claim_001",
		SourceRef: "claim:claim_001", BusinessRefs: []string{"object:kn_demo:forecast"},
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:00Z", SourceVersion: "main",
		Content:  map[string]any{"text": "result"},
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	}
	normalized, validationErrors := evidencevo.NormalizeArtifact(artifact)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	return normalized
}
