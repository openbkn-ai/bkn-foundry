// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN

package sessionstore

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
)

func TestVerifyEvidenceOwnershipDistinguishesMismatchFromMissingInteraction(t *testing.T) {
	tests := []struct {
		name         string
		rows         *sqlmock.Rows
		wantMismatch bool
	}{
		{name: "mismatched owner", rows: sqlmock.NewRows([]string{"application_principal_id", "effective_subject_type", "effective_subject_id", "delegation_id"}).AddRow("other-app", "user", "user-1", ""), wantMismatch: true},
		{name: "missing interaction", rows: sqlmock.NewRows([]string{"application_principal_id", "effective_subject_type", "effective_subject_id", "delegation_id"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT c.application_principal_id").WithArgs("conv-1", "int-1").WillReturnRows(tt.rows)
			mock.ExpectRollback()
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			event := ledgervo.Event{ConversationID: "conv-1", InteractionID: "int-1", Owner: sessionvo.Owner{ApplicationPrincipalID: "app-1", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user-1"}}
			err = verifyEvidenceOwnership(context.Background(), tx, event)
			if errors.Is(err, ievidenceledger.ErrOwnerMismatch) != tt.wantMismatch {
				t.Fatalf("error = %v, want owner mismatch = %v", err, tt.wantMismatch)
			}
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
