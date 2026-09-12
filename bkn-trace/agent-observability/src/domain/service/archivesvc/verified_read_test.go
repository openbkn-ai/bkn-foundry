// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"io"
	"strings"
	"testing"
)

type verifiedReadFixture map[string]string

func (f verifiedReadFixture) Read(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f[key])), nil
}
func TestReadVerifiedBundle(t *testing.T) {
	body := "{\"interaction\":{\"id\":\"i\"}}\n"
	sum := sha256.Sum256([]byte(body))
	job := Job{ID: "job", Kind: observabilityvo.ArchiveKindTrace, Status: observabilityvo.ArchiveStatusCompleted, ManifestRef: "manifest", CandidateCount: 1, CandidateIDs: []string{"i"}}
	for _, name := range []string{"valid", "tampered", "budget", "wrong_job", "failed", "count"} {
		t.Run(name, func(t *testing.T) {
			j := job
			data := body
			limit := 1000
			id := j.ID
			count := 1
			switch name {
			case "tampered":
				data += " "
			case "budget":
				limit = 2
			case "wrong_job":
				id = "other"
			case "failed":
				j.Status = observabilityvo.ArchiveStatusFailed
			case "count":
				count = 2
			}
			raw, _ := json.Marshal(map[string]any{"archive_job_id": id, "archive_kind": j.Kind, "candidate_count": count, "data_key": "data", "sha256": hex.EncodeToString(sum[:])})
			got, err := ReadVerifiedBundle(context.Background(), verifiedReadFixture{"manifest": string(raw), "data": data}, j, limit)
			if name == "valid" {
				if err != nil || string(got) != body {
					t.Fatalf("%s %v", got, err)
				}
			} else if err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
}
