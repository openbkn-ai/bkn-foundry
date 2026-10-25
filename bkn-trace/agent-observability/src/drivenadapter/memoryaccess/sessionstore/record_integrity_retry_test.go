// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func retryFixture() (*Store, *time.Time) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	s := NewWithClock(func() time.Time { return now })
	s.interactions["i"] = sessionvo.Interaction{ID: "i", ExecutionStatus: sessionvo.InteractionCompleted, IntegritySourceVersion: 1}
	return s, &now
}
func retryVerdict() *sessionvo.StoredRecordIntegrity {
	return &sessionvo.StoredRecordIntegrity{SourceVersion: 1, Applicable: false}
}
func TestDurableRetryLeaseExclusionAndAtomicVerdict(t *testing.T) {
	s, now := retryFixture()
	ctx := context.Background()
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, e := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
			if e != nil {
				t.Error(e)
			}
			if ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatal("lease not exclusive")
	}
	claim, _, _ := s.GetRecordIntegrityRetry(ctx, "i", 1)
	raw, _ := json.Marshal(claim)
	if strings.Contains(string(raw), claim.Token) {
		t.Fatal("token exposed")
	}
	bad := claim
	bad.Token = "stale"
	if ok, _ := s.FinishRecordIntegrityRetry(ctx, bad, retryVerdict(), ""); ok {
		t.Fatal("stale token accepted")
	}
	*now = now.Add(61 * time.Second)
	if ok, _ := s.FinishRecordIntegrityRetry(ctx, claim, retryVerdict(), ""); ok {
		t.Fatal("expired lease accepted")
	}
	newClaim, ok, e := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
	if e != nil || !ok || newClaim.Attempts != 2 {
		t.Fatal("expired lease not reclaimable")
	}
	if ok, e = s.FinishRecordIntegrityRetry(ctx, newClaim, retryVerdict(), ""); e != nil || !ok {
		t.Fatal(e)
	}
	state, _, _ := s.GetRecordIntegrityRetry(ctx, "i", 1)
	if state.Status != isessionstore.RecordIntegritySucceeded || s.interactions["i"].StoredRecordIntegrity == nil {
		t.Fatal("success not atomic")
	}
}
func TestDurableRetryExhaustionAndSourceRecovery(t *testing.T) {
	s, now := retryFixture()
	ctx := context.Background()
	for i := 1; i <= 8; i++ {
		c, ok, e := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
		if e != nil || !ok || c.Attempts != i {
			t.Fatalf("claim %d: %v %v", i, ok, e)
		}
		if i == 8 {
			*now = now.Add(61 * time.Second)
			break
		}
		if ok, e = s.FinishRecordIntegrityRetry(ctx, c, nil, "inspection_failed"); !ok || e != nil {
			t.Fatal(e)
		}
		if candidates, _ := s.ListRecordIntegrityCandidates(ctx, "", 10); len(candidates) != 0 {
			t.Fatal("not-due candidate selected")
		}
		*now = now.Add(isessionstore.RecordIntegrityBackoff(i))
	}
	candidates, _ := s.ListRecordIntegrityCandidates(ctx, "", 10)
	if len(candidates) != 1 {
		t.Fatal("expired eighth attempt must finalize exhaustion")
	}
	r, ok, e := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
	if e != nil || ok || r.Status != isessionstore.RecordIntegrityExhausted || r.Reason != "attempts_exhausted" {
		t.Fatalf("exhaustion %+v %v", r, e)
	}
	if s.interactions["i"].StoredRecordIntegrity != nil {
		t.Fatal("failure fabricated verdict")
	}
	v := s.interactions["i"]
	v.IntegritySourceVersion = 2
	s.interactions["i"] = v
	if next, ok, e := s.ClaimRecordIntegrityRetry(ctx, "i", 2); e != nil || !ok || next.Attempts != 1 {
		t.Fatal("new version did not recover")
	}
	old, _, _ := s.GetRecordIntegrityRetry(ctx, "i", 1)
	if old.Status != isessionstore.RecordIntegrityExhausted {
		t.Fatal("historical exhaustion overwritten")
	}
}
func TestDurableRetryDeadlineAndSupersede(t *testing.T) {
	s, now := retryFixture()
	ctx := context.Background()
	_, _, _ = s.ClaimRecordIntegrityRetry(ctx, "i", 1)
	*now = now.Add(31 * time.Minute)
	r, ok, e := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
	if e != nil || ok || r.Reason != "retry_deadline_exceeded" {
		t.Fatal("deadline not terminal")
	}
	s, now = retryFixture()
	c, _, _ := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
	v := s.interactions["i"]
	v.IntegritySourceVersion = 2
	s.interactions["i"] = v
	if ok, e = s.FinishRecordIntegrityRetry(ctx, c, retryVerdict(), ""); e != nil || ok {
		t.Fatal("source change accepted")
	}
	r, _, _ = s.GetRecordIntegrityRetry(ctx, "i", 1)
	if r.Status != isessionstore.RecordIntegritySuperseded {
		t.Fatal("old active history retained")
	}
	_ = now
}

func TestDurableRetryFinishesLeasedAttemptAfterRetryDeadline(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			s, now := retryFixture()
			ctx := context.Background()
			first, ok, err := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
			if err != nil || !ok {
				t.Fatalf("first claim: %v %v", ok, err)
			}
			if ok, err = s.FinishRecordIntegrityRetry(ctx, first, nil, "inspection_failed"); err != nil || !ok {
				t.Fatalf("first failure: %v %v", ok, err)
			}
			*now = now.Add(isessionstore.RecordIntegrityRetryDeadline - 10*time.Second)
			claim, ok, err := s.ClaimRecordIntegrityRetry(ctx, "i", 1)
			if err != nil || !ok {
				t.Fatalf("last permitted claim: %v %v", ok, err)
			}
			*now = now.Add(20 * time.Second) // Retry deadline passed; this lease is still valid.
			var stored *sessionvo.StoredRecordIntegrity
			reason := "attempt_timeout"
			if success {
				stored = retryVerdict()
				reason = ""
			}
			if ok, err = s.FinishRecordIntegrityRetry(ctx, claim, stored, reason); err != nil || !ok {
				t.Fatalf("finish valid lease: %v %v", ok, err)
			}
			state, _, err := s.GetRecordIntegrityRetry(ctx, "i", 1)
			if err != nil {
				t.Fatal(err)
			}
			if success {
				if state.Status != isessionstore.RecordIntegritySucceeded || s.interactions["i"].StoredRecordIntegrity == nil {
					t.Fatalf("successful verdict was discarded: %+v", state)
				}
			} else if state.Status != isessionstore.RecordIntegrityExhausted || state.Reason != "retry_deadline_exceeded" || state.LastFailureReason != "attempt_timeout" || s.interactions["i"].StoredRecordIntegrity != nil {
				t.Fatalf("failed attempt lost terminal cause: %+v", state)
			}
		})
	}
}
