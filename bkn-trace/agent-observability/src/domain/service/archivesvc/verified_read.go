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
	"io"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var errArchiveObjectRead = errors.New("archive object read failed")

// ReadVerifiedBundle is an internal read primitive, not a download API. Job must
// come from the trusted archive task store. Payload hash proves consistency with
// its stored manifest, not authenticity of an arbitrary uploaded manifest.
// Per-interaction/revision validation remains the evidence reader's responsibility.
func ReadVerifiedBundle(ctx context.Context, store ReadStore, job Job, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > 64<<20 {
		return nil, errors.New("invalid archive read request")
	}
	manifest, err := readArchiveManifest(ctx, store, job)
	if err != nil {
		return nil, err
	}
	body, err := readArchiveObject(ctx, store, manifest.DataKey, maxBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != manifest.Hash {
		return nil, errors.New("archive bundle checksum mismatch")
	}
	return body, nil
}

// archiveManifest matches the existing writer format, including older archives.
type archiveManifest struct {
	JobID   string                      `json:"archive_job_id"`
	Kind    observabilityvo.ArchiveKind `json:"archive_kind"`
	Count   int                         `json:"candidate_count"`
	DataKey string                      `json:"data_key"`
	Hash    string                      `json:"sha256"`
}

func readArchiveManifest(ctx context.Context, store ReadStore, job Job) (archiveManifest, error) {
	var manifest archiveManifest
	if store == nil || job.ID == "" || job.ManifestRef == "" || !job.Kind.Valid() || job.CandidateCount <= 0 || job.CandidateCount != len(job.CandidateIDs) {
		return manifest, errors.New("invalid archive read request")
	}
	if job.Status != observabilityvo.ArchiveStatusCompleted && job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		return manifest, errors.New("archive is not verified for reading")
	}
	raw, err := readArchiveObject(ctx, store, job.ManifestRef, 64<<10)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.JobID != job.ID || manifest.Kind != job.Kind || manifest.Count != job.CandidateCount || manifest.DataKey == "" || len(manifest.Hash) != 64 {
		return manifest, errors.New("archive manifest identity mismatch")
	}
	return manifest, nil
}

func readArchiveObject(ctx context.Context, store ReadStore, key string, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stream, err := store.Read(ctx, key)
	if err != nil {
		return nil, errors.Join(errArchiveObjectRead, err)
	}
	defer func() { _ = stream.Close() }()
	raw, err := io.ReadAll(io.LimitReader(stream, int64(limit)+1))
	if err != nil {
		return nil, errors.Join(errArchiveObjectRead, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("archive read budget exceeded")
	}
	return raw, nil
}
