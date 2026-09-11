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
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"io"
)

// ReadVerifiedBundle is an internal read primitive, not a download API. Job must
// come from the trusted archive task store. Payload hash proves consistency with
// its stored manifest, not authenticity of an arbitrary uploaded manifest.
// Per-interaction/revision validation remains the evidence reader's responsibility.
func ReadVerifiedBundle(ctx context.Context, store ReadStore, job Job, maxBytes int) ([]byte, error) {
	if store == nil || maxBytes <= 0 || maxBytes > 64<<20 || job.ID == "" || job.ManifestRef == "" || !job.Kind.Valid() || job.CandidateCount <= 0 || job.CandidateCount != len(job.CandidateIDs) {
		return nil, errors.New("invalid archive read request")
	}
	if job.Status != observabilityvo.ArchiveStatusCompleted && job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		return nil, errors.New("archive is not verified for reading")
	}
	read := func(key string, limit int) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stream, err := store.Read(ctx, key)
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		raw, err := io.ReadAll(io.LimitReader(stream, int64(limit)+1))
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(raw) > limit {
			return nil, errors.New("archive read budget exceeded")
		}
		return raw, nil
	}
	raw, err := read(job.ManifestRef, 64<<10)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		JobID   string                      `json:"archive_job_id"`
		Kind    observabilityvo.ArchiveKind `json:"archive_kind"`
		Count   int                         `json:"candidate_count"`
		DataKey string                      `json:"data_key"`
		Hash    string                      `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.JobID != job.ID || manifest.Kind != job.Kind || manifest.Count != job.CandidateCount || manifest.DataKey == "" || len(manifest.Hash) != 64 {
		return nil, errors.New("archive manifest identity mismatch")
	}
	body, err := read(manifest.DataKey, maxBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != manifest.Hash {
		return nil, errors.New("archive bundle checksum mismatch")
	}
	return body, nil
}
