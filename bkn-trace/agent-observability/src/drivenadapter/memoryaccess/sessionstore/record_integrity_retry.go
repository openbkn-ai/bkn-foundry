// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"crypto/rand"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type integrityRetryKey struct {
	id      string
	version uint64
}

var _ isessionstore.RecordIntegrityRetryStore = (*Store)(nil)

func (s *Store) integrityRetryCandidate(id string, version uint64) bool {
	r, ok := s.integrityRetries[integrityRetryKey{id, version}]
	return !ok || isessionstore.RecordIntegrityRetryDue(r, s.now().UTC())
}
func (s *Store) ClaimRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return isessionstore.RecordIntegrityRetry{}, false, err
	}
	v, ok := s.interactions[id]
	if !ok || !v.IsTerminal() || v.IntegritySourceVersion != version || v.StoredRecordIntegrity != nil {
		return isessionstore.RecordIntegrityRetry{}, false, nil
	}
	if s.integrityRetries == nil {
		s.integrityRetries = map[integrityRetryKey]isessionstore.RecordIntegrityRetry{}
	}
	for key, old := range s.integrityRetries {
		if key.id == id && key.version != version && (old.Status == isessionstore.RecordIntegrityRunning || old.Status == isessionstore.RecordIntegrityRetryWait) {
			old.Status = isessionstore.RecordIntegritySuperseded
			old.Reason = "source_changed"
			old.Token = ""
			s.integrityRetries[key] = old
		}
	}
	key := integrityRetryKey{id, version}
	r, exists := s.integrityRetries[key]
	now := s.now().UTC()
	if exists {
		if !isessionstore.RecordIntegrityRetryDue(r, now) {
			return r, false, nil
		}
		if r.Status == isessionstore.RecordIntegrityRunning {
			r.LastFailureReason = "lease_expired"
		}
		if why := isessionstore.RecordIntegrityExhaustion(r, now); why != "" {
			r.Status = isessionstore.RecordIntegrityExhausted
			r.Reason = why
			r.Token = ""
			s.integrityRetries[key] = r
			return r, false, nil
		}
	} else {
		r = isessionstore.RecordIntegrityRetry{InteractionID: id, SourceVersion: version, FirstAttemptAt: now}
	}
	if r.Status == isessionstore.RecordIntegrityRunning {
		r.Reason = "lease_expired"
		r.LastFailureReason = "lease_expired"
	}
	r.Attempts++
	r.Status = isessionstore.RecordIntegrityRunning
	r.LastAttemptAt = now
	r.NextAttemptAt = now
	r.LeaseUntil = now.Add(isessionstore.RecordIntegrityLeaseDuration)
	r.Token = rand.Text()
	s.integrityRetries[key] = r
	return r, true, nil
}
func (s *Store) GetRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return isessionstore.RecordIntegrityRetry{}, false, e
	}
	r, ok := s.integrityRetries[integrityRetryKey{id, version}]
	return r, ok, nil
}
func (s *Store) FinishRecordIntegrityRetry(ctx context.Context, claim isessionstore.RecordIntegrityRetry, stored *sessionvo.StoredRecordIntegrity, reason string) (bool, error) {
	if e := isessionstore.ValidateRecordIntegrityRetryResult(claim, stored, reason); e != nil {
		return false, e
	}
	if stored != nil {
		if _, e := isessionstore.MarshalStoredRecordIntegrity(*stored); e != nil {
			return false, e
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return false, e
	}
	key := integrityRetryKey{claim.InteractionID, claim.SourceVersion}
	r, ok := s.integrityRetries[key]
	now := s.now().UTC()
	v, exists := s.interactions[claim.InteractionID]
	if !ok || !exists || claim.Token == "" || r.Token != claim.Token || r.Status != isessionstore.RecordIntegrityRunning || !now.Before(r.LeaseUntil) {
		return false, nil
	}
	if v.IntegritySourceVersion != claim.SourceVersion {
		r.Status = isessionstore.RecordIntegritySuperseded
		r.Reason = "source_changed"
		r.Token = ""
		s.integrityRetries[key] = r
		return false, nil
	}
	if !v.IsTerminal() || v.StoredRecordIntegrity != nil {
		return false, nil
	}
	// The retry deadline gates new claims, not a successful result from a
	// still-valid lease. Failed attempts apply exhaustion below.
	if stored != nil {
		v.StoredRecordIntegrity = sessionvo.CopyStoredRecordIntegrity(stored)
		s.interactions[v.ID] = v
		r.Status = isessionstore.RecordIntegritySucceeded
		r.Reason = ""
	} else {
		r.Reason = reason
		r.LastFailureReason = reason
		r.Status = isessionstore.RecordIntegrityRetryWait
		r.NextAttemptAt = now.Add(isessionstore.RecordIntegrityBackoff(r.Attempts))
		if why := isessionstore.RecordIntegrityExhaustion(r, now); why != "" {
			r.Status = isessionstore.RecordIntegrityExhausted
			r.Reason = why
		}
	}
	r.Token = ""
	s.integrityRetries[key] = r
	return true, nil
}
