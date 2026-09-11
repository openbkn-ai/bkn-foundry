// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRevisionInputSQLAtomicityAndImmutability(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_REVISION_STORAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated writable database required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	s := New(db)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, revisionInputTestSchema); err != nil {
		t.Fatal(err)
	}
	v := sealedInputFixture()
	v.RevisionID = fmt.Sprintf("seal-%d", time.Now().UnixNano())
	v.InteractionID = v.RevisionID
	revision := sessionvo.AssemblyRevision{ID: v.RevisionID, InteractionID: v.InteractionID, RevisionNo: 1, CompletionManifestVersion: "test", ArtifactManifestHash: "test", Completeness: sessionvo.EvidencePartial, Trigger: "test", CreatedAt: time.Now()}
	sentinel := errors.New("rollback")
	err = s.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveAssemblyRevision(revision)
		if err := tx.(isessionstore.RevisionInputTransaction).SaveRevisionInput(v, 100); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, found, err := s.ReadRevisionInput(ctx, v.InteractionID, v.RevisionID, 100); err != nil || found {
		t.Fatal("rolled back input visible", err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_assembly_revisions WHERE revision_id=?`, v.RevisionID).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan revision", err)
	}
	if err = s.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveAssemblyRevision(revision)
		return tx.(isessionstore.RevisionInputTransaction).SaveRevisionInput(v, 100)
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		return tx.(isessionstore.RevisionInputTransaction).SaveRevisionInput(v, 100)
	}); err != nil {
		t.Fatal("idempotence", err)
	}
	changedBody := v
	changedBody.Package = []byte("different fixed package")
	sum := sha256.Sum256(changedBody.Package)
	changedBody.Hash = "sha256:" + hex.EncodeToString(sum[:])
	err = s.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		return tx.(isessionstore.RevisionInputTransaction).SaveRevisionInput(changedBody, 100)
	})
	if !errors.Is(err, isessionstore.ErrRevisionInputConflict) {
		t.Fatal("different body accepted", err)
	}
	changed := v
	changed.InteractionID = "other"
	err = s.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		_ = tx.(isessionstore.RevisionInputTransaction).SaveRevisionInput(changed, 100)
		return nil
	})
	if !errors.Is(err, isessionstore.ErrRevisionInputConflict) {
		t.Fatal("ignored conflict committed", err)
	}
	if _, found, err := s.ReadRevisionInput(ctx, v.InteractionID, v.RevisionID, 1); err == nil || found {
		t.Fatal("oversize read")
	}
	got, found, err := s.ReadRevisionInput(ctx, v.InteractionID, v.RevisionID, 100)
	if err != nil || !found || string(got.Package) != string(v.Package) {
		t.Fatal("original changed", err)
	}
	if _, found, err = s.ReadRevisionInput(ctx, "other", v.RevisionID, 100); err != nil || found {
		t.Fatal("cross scope read", err)
	}
	for _, same := range []bool{true, false} {
		name := "different-content"
		if same {
			name = "same-content"
		}
		t.Run(name, func(t *testing.T) {
			concurrentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			rv := revision
			rv.ID = fmt.Sprintf("race-%d", time.Now().UnixNano())
			rv.InteractionID = rv.ID
			if err := s.WithinTransaction(concurrentCtx, func(tx isessionstore.Transaction) error { tx.SaveAssemblyRevision(rv); return nil }); err != nil {
				t.Fatal(err)
			}
			const writers = 8
			start := make(chan struct{})
			results := make(chan error, writers)
			var wg sync.WaitGroup
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					value := v
					value.RevisionID = rv.ID
					value.InteractionID = rv.InteractionID
					if !same {
						value.Package = []byte(fmt.Sprintf("writer-%d", i))
						sum := sha256.Sum256(value.Package)
						value.Hash = "sha256:" + hex.EncodeToString(sum[:])
					}
					results <- s.WithinTransaction(concurrentCtx, func(tx isessionstore.Transaction) error {
						return tx.(isessionstore.RevisionInputTransaction).SaveRevisionInput(value, 100)
					})
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			successes, conflicts := 0, 0
			for err := range results {
				if err == nil {
					successes++
				} else if errors.Is(err, isessionstore.ErrRevisionInputConflict) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if same && successes != writers {
				t.Fatalf("same content: successes=%d conflicts=%d", successes, conflicts)
			}
			if !same && (successes != 1 || conflicts != writers-1) {
				t.Fatalf("different content: successes=%d conflicts=%d", successes, conflicts)
			}
			stored, found, err := s.ReadRevisionInput(concurrentCtx, rv.InteractionID, rv.ID, 100)
			if err != nil || !found {
				t.Fatal("winner missing", err)
			}
			if same && string(stored.Package) != string(v.Package) {
				t.Fatal("same content changed")
			}
			if !same {
				valid := false
				for i := 0; i < writers; i++ {
					if string(stored.Package) == fmt.Sprintf("writer-%d", i) {
						valid = true
					}
				}
				if !valid {
					t.Fatal("unknown winner")
				}
			}
			var rows int
			if err = db.QueryRowContext(concurrentCtx, `SELECT COUNT(*) FROM bkn_trace_revision_inputs WHERE revision_id=?`, rv.ID).Scan(&rows); err != nil || rows != 1 {
				t.Fatal("nonunique seal", err)
			}
		})
	}

}

const revisionInputTestSchema = `CREATE TABLE IF NOT EXISTS bkn_trace_revision_inputs (
 revision_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_hash CHAR(71) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_package LONGBLOB NOT NULL,
 INDEX idx_revision_input_interaction (interaction_id)
) ENGINE=InnoDB;
`
