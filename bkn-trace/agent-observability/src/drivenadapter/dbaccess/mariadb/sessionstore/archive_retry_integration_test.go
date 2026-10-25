// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestArchivePurgeRetryHistoryMariaDB(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	var db *sql.DB
	var err error
	if dsn == "" {
		// Reuse the guarded, disposable archive fixture when the dedicated
		// batch DSN is supplied by the focused SQL cleanup integration step.
		db = archiveConcurrencyDatabase(t)
	} else {
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := New(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			id := fmt.Sprintf("archive-retry-%d", time.Now().UnixNano())
			now := time.Now().UTC()
			err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
				tx.SaveConversation(sessionvo.Conversation{ID: id})
				tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: id, ExecutionStatus: sessionvo.InteractionCompleted, TerminalAt: &now, IntegritySourceVersion: 3})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_, _ = db.ExecContext(context.Background(), "DELETE FROM bkn_trace_record_integrity_retries WHERE interaction_id=?", id)
				_, _ = db.ExecContext(context.Background(), "DELETE FROM bkn_trace_interactions WHERE interaction_id=?", id)
				_, _ = db.ExecContext(context.Background(), "DELETE FROM bkn_trace_conversations WHERE conversation_id=?", id)
			}()
			for v := 1; v <= 3; v++ {
				_, err = db.ExecContext(ctx, `INSERT INTO bkn_trace_record_integrity_retries (interaction_id,source_version,retry_status,attempts,first_attempt_at,last_attempt_at,next_attempt_at,lease_until,lease_token,reason) VALUES (?,?,'retry_wait',1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),'','inspection_failed')`, id, v)
				if err != nil {
					t.Fatal(err)
				}
			}
			if fail {
				_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER archive_retry_purge_failure BEFORE DELETE ON bkn_trace_interactions FOR EACH ROW BEGIN IF OLD.interaction_id='%s' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected purge failure'; END IF; END`, id))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _, _ = db.ExecContext(context.Background(), "DROP TRIGGER archive_retry_purge_failure") }()
			}
			source := NewTraceArchiveSource(store)
			payload, _, freezeErr := source.packageInteraction(ctx, id)
			if freezeErr != nil {
				t.Fatal(freezeErr)
			}
			err = source.Purge(ctx, observabilityvo.ArchiveKindTrace, []archivesvc.Candidate{{ID: id, Payload: payload}})
			if (err != nil) != fail {
				t.Fatalf("Purge error=%v fail=%v", err, fail)
			}
			want := 0
			if fail {
				want = 3
			}
			var count int
			if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_record_integrity_retries WHERE interaction_id=?", id).Scan(&count); err != nil || count != want {
				t.Fatalf("retry rows=%d want=%d err=%v", count, want, err)
			}
			want = 0
			if fail {
				want = 1
			}
			if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_interactions WHERE interaction_id=?", id).Scan(&count); err != nil || count != want {
				t.Fatalf("interaction rows=%d want=%d err=%v", count, want, err)
			}
		})
	}
}
