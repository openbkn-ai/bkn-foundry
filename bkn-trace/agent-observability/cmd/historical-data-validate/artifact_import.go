// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"io"
	"os"
	"time"
)

type artifactTarget interface {
	StoreArtifact(context.Context, evidencevo.EvidenceArtifact) (bool, error)
	GetArtifact(context.Context, string, evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error)
}

func importArtifactRecords(input io.Reader, output io.Writer) error {
	var plan struct {
		historyTransportSettings
		Artifacts []evidencevo.EvidenceArtifact `json:"artifacts"`
	}
	decoder := json.NewDecoder(io.LimitReader(input, 64<<20))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return fmt.Errorf("invalid artifact plan")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing artifact input")
	}
	index := os.Getenv("OPENSEARCH_EVIDENCE_INDEX")
	if index == "" {
		return fmt.Errorf("artifact index missing")
	}
	client, closeClient, err := historyOpenSearchClient(plan.historyTransportSettings)
	if err != nil {
		return err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	created, err := importArtifacts(ctx, opensearchevidencestore.New(client, index), plan.Artifacts)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{"verified": true, "created": created, "already_verified": len(plan.Artifacts) - created, "verified_count": len(plan.Artifacts)})
}
func importArtifacts(ctx context.Context, target artifactTarget, artifacts []evidencevo.EvidenceArtifact) (int, error) {
	normalized := make([]evidencevo.EvidenceArtifact, len(artifacts))
	seen := map[string]bool{}
	// Entire batch is validated and existing targets compared before first write.
	for i, a := range artifacts {
		n, errs := evidencevo.NormalizeArtifact(a)
		if len(errs) != 0 || seen[n.ArtifactID] {
			return 0, fmt.Errorf("invalid or duplicate artifact")
		}
		seen[n.ArtifactID] = true
		normalized[i] = n
	}
	existing := make([]bool, len(normalized))
	for i, a := range normalized {
		actual, found, err := target.GetArtifact(ctx, a.ArtifactID, evidencevo.QueryScope{AccountID: a.AccountID, AccountType: a.AccountType})
		if err != nil {
			return 0, err
		}
		if found && !sameArtifact(a, actual) {
			return 0, fmt.Errorf("artifact target conflict")
		}
		existing[i] = found
	}
	created := 0
	for i, a := range normalized {
		if !existing[i] {
			written, err := target.StoreArtifact(ctx, a)
			if err != nil {
				return created, err
			}
			if written {
				created++
			}
		}
		actual, found, err := target.GetArtifact(ctx, a.ArtifactID, evidencevo.QueryScope{AccountID: a.AccountID, AccountType: a.AccountType})
		if err != nil || !found || !sameArtifact(a, actual) {
			return created, fmt.Errorf("artifact readback mismatch")
		}
	}
	return created, nil
}
func sameArtifact(a, b evidencevo.EvidenceArtifact) bool {
	ah, err := evidencevo.ArtifactFingerprint(a)
	if err != nil {
		return false
	}
	bh, err := evidencevo.ArtifactFingerprint(b)
	return err == nil && ah == bh
}
