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

// verifyCleanupArchive binds the object bytes to the durable frozen cleanup
// payloads. Hashing is streamed so retry does not allocate another full bundle.
func verifyCleanupArchive(ctx context.Context, store ReadStore, job Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(job.Candidates) != job.CandidateCount {
		return errors.New("archive cleanup candidates are unavailable")
	}
	ids := make(map[string]bool, len(job.CandidateIDs))
	for _, id := range job.CandidateIDs {
		if id == "" || ids[id] {
			return errors.New("archive cleanup candidate identities mismatch")
		}
		ids[id] = true
	}
	expected := sha256.New()
	var size int64
	for _, candidate := range job.Candidates {
		if !ids[candidate.ID] {
			return errors.New("archive cleanup candidate identities mismatch")
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
	if !strings.HasSuffix(job.ManifestRef, "/manifest.json") || manifest.DataKey != strings.TrimSuffix(job.ManifestRef, "manifest.json")+"data.jsonl" {
		return errors.New("archive manifest data key mismatch")
	}
	if manifest.Hash != hex.EncodeToString(expected.Sum(nil)) {
		return errors.New("archive checksum differs from frozen cleanup candidates")
	}
	stream, err := store.Read(ctx, manifest.DataKey)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	actual := sha256.New()
	// Read at most one byte beyond the exact frozen bundle length.
	n, err := io.Copy(actual, io.LimitReader(stream, size+1))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n != size || hex.EncodeToString(actual.Sum(nil)) != manifest.Hash {
		return errors.New("archive bundle checksum mismatch")
	}
	return nil
}
