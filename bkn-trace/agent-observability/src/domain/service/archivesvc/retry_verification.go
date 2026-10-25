// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

var errCleanupRecoveryState = errors.New("archive cleanup recovery state is unavailable")

// verifyCleanupArchive binds the object bytes to the durable frozen cleanup
// payloads. Hashing is streamed so retry does not allocate another full bundle.
func verifyCleanupArchive(ctx context.Context, store ReadStore, job Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || job.ID == "" || job.ManifestRef == "" || !job.Kind.Valid() || job.CandidateCount <= 0 || len(job.Candidates) != job.CandidateCount || len(job.CandidateIDs) != job.CandidateCount {
		return errCleanupRecoveryState
	}
	ids := make(map[string]bool, len(job.CandidateIDs))
	for _, id := range job.CandidateIDs {
		if id == "" || ids[id] {
			return errCleanupRecoveryState
		}
		ids[id] = true
	}
	expected := sha256.New()
	var size int64
	for _, candidate := range job.Candidates {
		if !ids[candidate.ID] {
			return errCleanupRecoveryState
		}
		delete(ids, candidate.ID)
		_, _ = expected.Write(candidate.Payload)
		_, _ = expected.Write([]byte{'\n'})
		size += int64(len(candidate.Payload)) + 1
	}
	manifest, err := readArchiveManifest(ctx, store, job)
	if err != nil {
		return err
	}
	// Accept only the two layouts produced by the current and v0.1.4 writers.
	currentKey := strings.TrimSuffix(job.ManifestRef, "manifest.json") + "data.jsonl"
	legacyKey := strings.TrimSuffix(job.ManifestRef, "/manifest.json") + ".jsonl"
	if !strings.HasSuffix(job.ManifestRef, "/manifest.json") || (manifest.DataKey != currentKey && manifest.DataKey != legacyKey) {
		return errors.New("archive manifest data key mismatch")
	}
	if manifest.Hash != hex.EncodeToString(expected.Sum(nil)) {
		return errors.New("archive checksum differs from frozen cleanup candidates")
	}
	stream, err := store.Read(ctx, manifest.DataKey)
	if err != nil {
		return errors.Join(errArchiveObjectRead, err)
	}
	defer func() { _ = stream.Close() }()
	actual := sha256.New()
	// Read at most one byte beyond the exact frozen bundle length.
	n, err := io.Copy(actual, io.LimitReader(stream, size+1))
	if err != nil {
		return errors.Join(errArchiveObjectRead, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n != size || hex.EncodeToString(actual.Sum(nil)) != manifest.Hash {
		return errors.New("archive bundle checksum mismatch")
	}
	return nil
}

// Persist fixed categories only: underlying read errors may contain signed URLs.
func cleanupVerificationHint(err error) string {
	switch {
	case errors.Is(err, errCleanupRecoveryState):
		return "archive cleanup recovery records or object read support are unavailable; operator review is required"
	case errors.Is(err, errArchiveObjectRead), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "archive cleanup could not read objects; check storage access and retry"
	default:
		return "archive cleanup manifest or contents do not match frozen candidates; restore the matching archive objects before retrying"
	}
}
