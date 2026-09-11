// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"strings"
)

func validateStoredRevisionInput(v sessionvo.SealedRevisionInput, maxBytes int) error {
	if strings.TrimSpace(v.RevisionID) == "" || strings.TrimSpace(v.InteractionID) == "" || maxBytes <= 0 || len(v.Package) == 0 || len(v.Package) > maxBytes {
		return errors.New("invalid revision input identity or budget")
	}
	h := sha256.Sum256(v.Package)
	if v.Hash != "sha256:"+hex.EncodeToString(h[:]) {
		return errors.New("revision input hash mismatch")
	}
	return nil
}

// SaveRevisionInput shares the lifecycle SQL transaction. Locking the parent
// revision serializes writers; no UPSERT may replace an existing sealed body.
// Any failure poisons the transaction even if a caller ignores the return value.
func (t *transaction) SaveRevisionInput(v sessionvo.SealedRevisionInput, maxBytes int) (err error) {
	if t.err != nil {
		return t.err
	}
	defer func() {
		if err != nil {
			t.err = err
		}
	}()
	if err = validateStoredRevisionInput(v, maxBytes); err != nil {
		return err
	}
	var interaction string
	if err = t.tx.QueryRowContext(t.ctx, `SELECT interaction_id FROM bkn_trace_assembly_revisions WHERE revision_id = ? FOR UPDATE`, v.RevisionID).Scan(&interaction); err != nil {
		return err
	}
	if interaction != v.InteractionID {
		return isessionstore.ErrRevisionInputConflict
	}
	var hash string
	var size int64
	err = t.tx.QueryRowContext(t.ctx, `SELECT input_hash, OCTET_LENGTH(input_package) FROM bkn_trace_revision_inputs WHERE revision_id = ?`, v.RevisionID).Scan(&hash, &size)
	if err == nil {
		if hash != v.Hash || size != int64(len(v.Package)) {
			return isessionstore.ErrRevisionInputConflict
		}
		var previous []byte
		if err = t.tx.QueryRowContext(t.ctx, `SELECT input_package FROM bkn_trace_revision_inputs WHERE revision_id = ? AND interaction_id = ?`, v.RevisionID, v.InteractionID).Scan(&previous); err != nil {
			return err
		}
		if !bytes.Equal(previous, v.Package) {
			return isessionstore.ErrRevisionInputConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO bkn_trace_revision_inputs (revision_id, interaction_id, input_hash, input_package) VALUES (?, ?, ?, ?)`, v.RevisionID, v.InteractionID, v.Hash, v.Package)
	return err
}

// ReadRevisionInput is Core-only. Caller resolves existing Trace access first.
// Oversize bodies are excluded by SQL before transfer; no current facts fallback.
func (s *Store) ReadRevisionInput(ctx context.Context, interactionID, revisionID string, maxBytes int) (sessionvo.SealedRevisionInput, bool, error) {
	if strings.TrimSpace(interactionID) == "" || strings.TrimSpace(revisionID) == "" || maxBytes <= 0 {
		return sessionvo.SealedRevisionInput{}, false, errors.New("invalid revision input read")
	}
	v := sessionvo.SealedRevisionInput{InteractionID: interactionID, RevisionID: revisionID}
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT input_hash, OCTET_LENGTH(input_package), CASE WHEN OCTET_LENGTH(input_package) <= ? THEN input_package ELSE NULL END FROM bkn_trace_revision_inputs WHERE revision_id = ? AND interaction_id = ?`, maxBytes, revisionID, interactionID).Scan(&v.Hash, &size, &v.Package)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.SealedRevisionInput{}, false, nil
	}
	if err != nil {
		return sessionvo.SealedRevisionInput{}, false, err
	}
	if size > int64(maxBytes) {
		return sessionvo.SealedRevisionInput{}, false, errors.New("revision input exceeds read budget")
	}
	if err = validateStoredRevisionInput(v, maxBytes); err != nil {
		return sessionvo.SealedRevisionInput{}, false, err
	}
	return v, true, nil
}

var _ isessionstore.RevisionInputTransaction = (*transaction)(nil)
var _ isessionstore.RevisionInputReader = (*Store)(nil)
