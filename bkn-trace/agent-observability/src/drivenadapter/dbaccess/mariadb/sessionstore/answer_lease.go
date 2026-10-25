// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"database/sql"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func (t *transaction) CheckProjectionLease(id uint64, token string) error {
	var found uint64
	err := t.tx.QueryRowContext(t.ctx, `SELECT outbox_id FROM bkn_trace_projection_outbox WHERE outbox_id=? AND status='processing' AND lease_token=? FOR UPDATE`, id, token).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return iprojectionoutbox.ErrLeaseLost
	}
	return err
}
