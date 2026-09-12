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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// RevisionInputConsumer is assembled explicitly in the internal outbox worker.
// Apply must durably and idempotently persist by revision ID and input hash;
// redelivery is possible after a successful Apply but a failed acknowledgement.
type RevisionInputConsumer struct {
	Reader               isessionstore.RevisionInputReader
	Apply                func(context.Context, sessionvo.EvidenceSnapshot, string) error
	MaxRecords, MaxBytes int
}

func (c *RevisionInputConsumer) HandleRevisionInput(ctx context.Context, item iprojectionoutbox.Item) error {
	if c == nil || c.Apply == nil || c.Reader == nil {
		return errors.New("revision consumer not assembled")
	}
	if item.EventType != sessionvo.RevisionInputSealedEvent || item.AggregateType != "revision_input" || len(item.Payload) > 65536 {
		return iprojectionoutbox.Permanent(errors.New("invalid revision notice envelope"))
	}
	var notice sessionvo.RevisionInputNotice
	if err := json.Unmarshal(item.Payload, &notice); err != nil {
		return iprojectionoutbox.Permanent(err)
	}
	if notice.RevisionID != item.AggregateID || item.EventID != "seal-"+notice.RevisionID {
		return iprojectionoutbox.Permanent(errors.New("revision notice identity mismatch"))
	}
	snapshot, err := ReadSealedRevision(ctx, c.Reader, notice, c.MaxRecords, c.MaxBytes)
	if err != nil {
		return err
	}
	if snapshot.Revisions[0].RevisionNo != item.AggregateVersion {
		return iprojectionoutbox.Permanent(errors.New("revision notice version mismatch"))
	}
	return c.Apply(ctx, snapshot, notice.Hash)
}
