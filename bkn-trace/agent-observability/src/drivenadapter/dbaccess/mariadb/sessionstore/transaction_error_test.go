// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestGetConversationRetriesStorageErrorsInsteadOfDomainNotFound(t *testing.T) {
	for _, code := range []uint16{1205, 1213} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			storageErr := &mysql.MySQLError{Number: code, Message: "conversation query failed"}
			for attempt := 0; attempt < 4; attempt++ {
				expectConversationQuery(mock).WillReturnError(storageErr)
				mock.ExpectRollback()
			}

			_, err := sessionsvc.New(store, sessionsvc.Options{}).GetConversation(context.Background(), transactionErrorOwner(), "conv-1")
			assertStorageError(t, err, storageErr)
			if err == storageErr {
				t.Error("exhausted retries must wrap the final storage error")
			}
		})
	}
}

func TestGetConversationRecoversAfterRetryableStorageErrors(t *testing.T) {
	for _, code := range []uint16{1205, 1213} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			for attempt := 0; attempt < 3; attempt++ {
				expectConversationQuery(mock).WillReturnError(&mysql.MySQLError{Number: code, Message: "conversation query failed"})
				mock.ExpectRollback()
			}
			owner := transactionErrorOwner()
			expectConversationQuery(mock).WillReturnRows(transactionErrorConversationRows(&owner))
			mock.ExpectCommit()

			conversation, err := sessionsvc.New(store, sessionsvc.Options{}).GetConversation(context.Background(), owner, "conv-1")
			if err != nil || conversation.ID != "conv-1" || !conversation.Owner.Equal(owner) {
				t.Fatalf("retry did not recover the owned conversation: %#v, %v", conversation, err)
			}
		})
	}
}

func TestGetConversationPreservesNonRetryableStorageError(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	storageErr := &mysql.MySQLError{Number: 1146, Message: "conversation table missing"}
	expectConversationQuery(mock).WillReturnError(storageErr)
	mock.ExpectRollback()

	_, err := sessionsvc.New(store, sessionsvc.Options{}).GetConversation(context.Background(), transactionErrorOwner(), "conv-1")
	assertStorageError(t, err, storageErr)
	if err != storageErr {
		t.Fatalf("non-retryable storage error changed: got %v, want original %v", err, storageErr)
	}
}

func TestGetConversationPreservesOwnershipChecksWithoutStorageError(t *testing.T) {
	owner := transactionErrorOwner()
	principalMismatch, typeMismatch, subjectMismatch, delegationMismatch := owner, owner, owner, owner
	principalMismatch.ApplicationPrincipalID = "other-app"
	typeMismatch.EffectiveSubjectType = sessionvo.SubjectType("other-type")
	subjectMismatch.EffectiveSubjectID = "other-user"
	delegationMismatch.DelegationID = "other-delegation"
	for _, tc := range []struct {
		name      string
		rowOwner  *sessionvo.Owner
		wantCause string
	}{
		{name: "missing", wantCause: sessionsvc.CauseConversationNotFound},
		{name: "principal mismatch", rowOwner: &principalMismatch, wantCause: sessionsvc.CauseConversationOwnerMismatch},
		{name: "subject type mismatch", rowOwner: &typeMismatch, wantCause: sessionsvc.CauseConversationOwnerMismatch},
		{name: "subject mismatch", rowOwner: &subjectMismatch, wantCause: sessionsvc.CauseConversationOwnerMismatch},
		{name: "delegation mismatch", rowOwner: &delegationMismatch, wantCause: sessionsvc.CauseConversationOwnerMismatch},
		{name: "matching owner", rowOwner: &owner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			expectConversationQuery(mock).WillReturnRows(transactionErrorConversationRows(tc.rowOwner))
			if tc.wantCause == "" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}

			conversation, err := sessionsvc.New(store, sessionsvc.Options{}).GetConversation(context.Background(), owner, "conv-1")
			if tc.wantCause == "" {
				if err != nil || conversation.ID != "conv-1" || !conversation.Owner.Equal(owner) {
					t.Fatalf("matching owner rejected: %#v, %v", conversation, err)
				}
				return
			}
			var domainErr *sessionsvc.DomainError
			if !errors.As(err, &domainErr) || domainErr.Code != sessionsvc.CodeResourceNotDisclosed || domainErr.Cause != tc.wantCause {
				t.Fatalf("ownership rejection changed: got %v, want resource_not_disclosed/%s", err, tc.wantCause)
			}
			if conversation.ID != "" {
				t.Fatalf("rejected conversation disclosed: %#v", conversation)
			}
		})
	}
}

func TestWithinTransactionPrefersStorageErrorOverCallbackDomainError(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	storageErr := &mysql.MySQLError{Number: 1146, Message: "conversation table missing"}
	callbackErr := &sessionsvc.DomainError{Code: sessionsvc.CodeResourceNotDisclosed, Message: "not found"}
	expectConversationQuery(mock).WillReturnError(storageErr)
	mock.ExpectRollback()

	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.FindConversation("conv-1")
		return callbackErr
	})
	assertStorageError(t, err, storageErr)
	if err != storageErr {
		t.Fatalf("must select the original storage error: got %v", err)
	}
}

func TestWithinTransactionPreservesCallbackDomainErrorWithoutStorageError(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	callbackErr := &sessionsvc.DomainError{Code: sessionsvc.CodeResourceNotDisclosed, Message: "not found"}
	expectConversationQuery(mock).WillReturnRows(transactionErrorConversationRows(nil))
	mock.ExpectRollback()

	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.FindConversation("conv-1")
		return callbackErr
	})
	if err != callbackErr {
		t.Fatalf("domain error changed without storage failure: got %v, want original %v", err, callbackErr)
	}
}

func newTransactionErrorStore(t *testing.T) (*sessionstore.Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return sessionstore.New(db), mock
}

func expectConversationQuery(mock sqlmock.Sqlmock) *sqlmock.ExpectedQuery {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)))
	return mock.ExpectQuery("SELECT conversation_id").WithArgs("conv-1")
}

func transactionErrorOwner() sessionvo.Owner {
	return sessionvo.Owner{ApplicationPrincipalID: "app-1", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user-1", DelegationID: "delegation-1"}
}

func transactionErrorConversationRows(owner *sessionvo.Owner) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"conversation_id", "application_principal_id", "agent_name", "actor_name_snapshot", "creation_auth_method", "effective_subject_type", "effective_subject_id", "delegation_id", "external_conversation_key", "generation", "status", "one_shot", "row_version", "created_at", "updated_at", "closed_at"})
	if owner != nil {
		now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
		rows.AddRow("conv-1", owner.ApplicationPrincipalID, "agent", "actor", "oauth", owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, "external-1", 1, "active", false, 1, now, now, nil)
	}
	return rows
}

func assertStorageError(t *testing.T, err, storageErr error) {
	t.Helper()
	if !errors.Is(err, storageErr) {
		t.Errorf("storage failure was replaced: got %v, want %v", err, storageErr)
	}
	var domainErr *sessionsvc.DomainError
	if errors.As(err, &domainErr) {
		t.Errorf("storage failure must not carry a misleading domain error: %#v", domainErr)
	}
}
