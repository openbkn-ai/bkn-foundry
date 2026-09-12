// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type revisionArchiveContents struct {
	Revisions []sessionvo.AssemblyRevision    `json:"revisions"`
	Inputs    []sessionvo.SealedRevisionInput `json:"inputs"`
}

func readRevisionArchive(ctx context.Context, tx *sql.Tx, id string) (revisionArchiveContents, error) {
	result := revisionArchiveContents{}
	adapter := &transaction{ctx: ctx, tx: tx}
	result.Revisions = adapter.ListAssemblyRevisions(id)
	if adapter.err != nil {
		return result, adapter.err
	}
	const maxBytes = 64 << 20
	rows, err := tx.QueryContext(ctx, `SELECT revision_id,input_hash,OCTET_LENGTH(input_package),CASE WHEN OCTET_LENGTH(input_package)<=? THEN input_package ELSE NULL END FROM bkn_trace_revision_inputs WHERE interaction_id=? ORDER BY revision_id`, maxBytes, id)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	remaining := maxBytes
	for rows.Next() {
		v := sessionvo.SealedRevisionInput{InteractionID: id}
		var size int64
		if err = rows.Scan(&v.RevisionID, &v.Hash, &size, &v.Package); err != nil {
			return result, err
		}
		if size > int64(remaining) || len(result.Inputs) >= 1000 {
			return result, errors.New("revision archive budget exceeded")
		}
		if err = validateStoredRevisionInput(v, remaining); err != nil {
			return result, err
		}
		remaining -= len(v.Package)
		found := false
		for _, r := range result.Revisions {
			if r.ID == v.RevisionID {
				found = true
				break
			}
		}
		if !found {
			return result, errors.New("orphan revision input")
		}
		result.Inputs = append(result.Inputs, v)
	}
	return result, rows.Err()
}
func verifyRevisionArchiveForPurge(ctx context.Context, tx *sql.Tx, id string, payload []byte) error {
	var archived struct {
		Interaction sessionvo.EvidenceInteraction `json:"interaction"`
		Evidence    *revisionArchiveContents      `json:"revision_evidence"`
	}
	if err := json.Unmarshal(payload, &archived); err != nil {
		return err
	}
	if archived.Evidence == nil || archived.Interaction.ID != id {
		return errors.New("archive lacks sealed revision inputs")
	}
	var rowVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT row_version FROM bkn_trace_interactions WHERE interaction_id=?`, id).Scan(&rowVersion); err != nil {
		return err
	}
	if rowVersion != archived.Interaction.RowVersion {
		return errors.New("interaction changed since archive freeze")
	}
	// Seal writers lock the parent revision. Hold those same locks across
	// comparison and deletion so an input cannot arrive between the two.
	locks, err := tx.QueryContext(ctx, `SELECT revision_id FROM bkn_trace_assembly_revisions WHERE interaction_id=? ORDER BY revision_id FOR UPDATE`, id)
	if err != nil {
		return err
	}
	for locks.Next() {
		var revisionID string
		if err = locks.Scan(&revisionID); err != nil {
			_ = locks.Close()
			return err
		}
	}
	err = locks.Err()
	closeErr := locks.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	current, err := readRevisionArchive(ctx, tx, id)
	if err != nil {
		return err
	}
	a, err := json.Marshal(archived.Evidence)
	if err != nil {
		return err
	}
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return errors.New("revision inputs changed since archive freeze")
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_projection_outbox o JOIN bkn_trace_revision_inputs i ON o.aggregate_id=i.revision_id WHERE i.interaction_id=? AND o.event_type='revision.input.sealed' AND o.status<>'delivered'`, id).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return errors.New("revision projection delivery pending")
	}
	return nil
}
