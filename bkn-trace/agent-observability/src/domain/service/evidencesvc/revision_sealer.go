// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// SealRevisionInput must be called after SaveAssemblyRevision in the same
// transaction. No network/artifact fetch occurs while the transaction is held.
func SealRevisionInput(tx isessionstore.Transaction, interaction sessionvo.Interaction, revision sessionvo.AssemblyRevision, maxRecords, maxBytes int) error {
	writer, ok := tx.(isessionstore.RevisionInputTransaction)
	if !ok {
		return errors.New("revision input transaction capability unavailable")
	}
	snapshot, err := sessionvo.CopyEvidenceSnapshot(interaction, tx.ListOperations(interaction.ID), tx.ListReceipts(interaction.ID), tx.ListOperationCallFacts(interaction.ID), []sessionvo.AssemblyRevision{revision})
	if err != nil {
		return err
	}
	raw, hash, err := EncodeRevisionInput(snapshot, maxRecords, maxBytes)
	if err != nil {
		return err
	}
	if err = writer.SaveRevisionInput(sessionvo.SealedRevisionInput{RevisionID: revision.ID, InteractionID: interaction.ID, Hash: hash, Package: raw}, maxBytes); err != nil {
		return err
	}
	notice := sessionvo.RevisionInputNotice{Version: "internal-revision-input-v1", InteractionID: interaction.ID, RevisionID: revision.ID, Hash: hash}
	payload, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	tx.AppendProjection(sessionvo.ProjectionMutation{EventID: "seal-" + revision.ID, AggregateType: "revision_input", AggregateID: revision.ID, AggregateVersion: revision.RevisionNo, EventType: sessionvo.RevisionInputSealedEvent, Payload: payload})
	return nil
}

// ReadSealedRevision consumes a trusted internal notice using only immutable
// storage. Core must authorize the interaction before invoking this helper.
// Retrying the same notice never fetches current lifecycle rows or artifacts.
func ReadSealedRevision(ctx context.Context, reader isessionstore.RevisionInputReader, notice sessionvo.RevisionInputNotice, maxRecords, maxBytes int) (sessionvo.EvidenceSnapshot, error) {
	if reader == nil || notice.Version != "internal-revision-input-v1" || notice.InteractionID == "" || notice.RevisionID == "" || notice.Hash == "" {
		return sessionvo.EvidenceSnapshot{}, errors.New("invalid revision input notice")
	}
	value, found, err := reader.ReadRevisionInput(ctx, notice.InteractionID, notice.RevisionID, maxBytes)
	if err != nil {
		return sessionvo.EvidenceSnapshot{}, err
	}
	if !found {
		return sessionvo.EvidenceSnapshot{}, errors.New("sealed revision input unavailable")
	}
	if value.Hash != notice.Hash || value.InteractionID != notice.InteractionID || value.RevisionID != notice.RevisionID {
		return sessionvo.EvidenceSnapshot{}, errors.New("sealed revision notice mismatch")
	}
	return DecodeRevisionInput(value.Package, notice.Hash, notice.InteractionID, notice.RevisionID, maxRecords, maxBytes)
}
