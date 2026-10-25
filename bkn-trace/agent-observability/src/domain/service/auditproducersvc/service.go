// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditproducersvc

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isourcecoveragestore"
)

const observationTTL = 90 * time.Second
const gapSourceID = "audit-ledger"
const gapDeploymentID = "bkn-safe-publisher-gap"

var ErrObservationUnavailable = errors.New("audit producer observation unavailable")
var ErrGapPersistenceUnavailable = errors.New("audit producer gap persistence unavailable")

// Snapshot covers exactly one publisher process, never all replicas or consumers.
type Snapshot struct {
	SchemaVersion   string     `json:"schema_version"`
	InstanceID      string     `json:"instance_id"`
	ObservedAt      time.Time  `json:"observed_at"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	DroppedRecords  uint64     `json:"dropped_records"`
	LastDeliveredAt *time.Time `json:"last_delivered_at"`
	LastFailureAt   *time.Time `json:"last_failure_at"`
}

type Reader interface {
	Read(context.Context) (Snapshot, error)
}
type Options struct{ Now func() time.Time }

type Service struct {
	store           isourcecoveragestore.Store
	reader          Reader
	now             func() time.Time
	observeMu       sync.Mutex
	mu              sync.RWMutex
	current         observabilityvo.AuditCollectionStatus
	writtenInstance string
	writtenDrops    uint64
	hasWritten      bool
}

func New(store isourcecoveragestore.Store, reader Reader, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{store: store, reader: reader, now: options.Now, current: observabilityvo.UnknownAuditCollectionStatus()}
}

// Observe does bounded background work. Status never calls dependencies or
// interprets failed observation transport as publisher failure.
func (s *Service) Observe(ctx context.Context) error {
	s.observeMu.Lock()
	defer s.observeMu.Unlock()
	var gapErr error
	if s.store != nil {
		coverage, found, err := s.store.Get(ctx, gapSourceID, gapDeploymentID)
		if err != nil {
			gapErr = ErrGapPersistenceUnavailable
		} else if found && coverage.DroppedRecords > 0 {
			s.rememberGap(coverage.FirstObservedAt)
		}
	}
	if s.reader == nil {
		s.setUnknown("audit_producer_not_monitored")
		return gapErr
	}
	snapshot, err := s.reader.Read(ctx)
	now := s.now().UTC() // Read may take time; compare producer time after it finishes.
	if err != nil || !validSnapshot(snapshot, now) {
		s.setUnknown("audit_producer_status_unavailable")
		return errors.Join(gapErr, ErrObservationUnavailable)
	}
	s.mu.Lock()
	historicalGap, since := s.current.HistoricalGap, s.current.HistoricalGapSince
	s.current = observabilityvo.AuditCollectionStatus{
		Scope: "bkn-safe-publisher-instance", InstanceID: snapshot.InstanceID, Status: snapshot.Status, Reason: snapshot.Reason,
		ObservedAt: copyTime(&snapshot.ObservedAt), LastDeliveredAt: copyTime(snapshot.LastDeliveredAt), LastFailureAt: copyTime(snapshot.LastFailureAt),
		DroppedRecords: copyUint64(&snapshot.DroppedRecords), HistoricalGap: historicalGap, HistoricalGapSince: since,
	}
	s.mu.Unlock()
	if snapshot.DroppedRecords == 0 {
		return gapErr
	}
	s.rememberGap(snapshot.ObservedAt)
	// A fixed durable row is evidence of historical loss. Its GREATEST count is
	// not a cross-instance total and is never used as the current instance count.
	if s.store == nil {
		return gapErr
	}
	if s.hasWritten && s.writtenInstance == snapshot.InstanceID && s.writtenDrops == snapshot.DroppedRecords {
		return gapErr
	}
	dropped := int64(math.MaxInt64)
	if snapshot.DroppedRecords <= math.MaxInt64 {
		dropped = int64(snapshot.DroppedRecords)
	}
	coverage := observabilityvo.SourceCoverage{SourceID: gapSourceID, DeploymentID: gapDeploymentID, State: observabilityvo.SourceCoverageDegraded, Reason: "telemetry_dropped", DroppedRecords: dropped, FirstObservedAt: snapshot.ObservedAt, LastObservedAt: snapshot.ObservedAt}
	if err := s.store.UpsertDegraded(ctx, coverage); err != nil {
		return errors.Join(gapErr, ErrGapPersistenceUnavailable)
	}
	s.hasWritten, s.writtenInstance, s.writtenDrops = true, snapshot.InstanceID, snapshot.DroppedRecords
	return gapErr
}

func (s *Service) setUnknown(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gap, since := s.current.HistoricalGap, s.current.HistoricalGapSince
	s.current = observabilityvo.UnknownAuditCollectionStatus()
	s.current.Reason = reason
	s.current.HistoricalGap, s.current.HistoricalGapSince = gap, since
}

func (s *Service) rememberGap(since time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gap := true
	s.current.HistoricalGap = &gap
	if !since.IsZero() && (s.current.HistoricalGapSince == nil || since.Before(*s.current.HistoricalGapSince)) {
		since = since.UTC()
		s.current.HistoricalGapSince = &since
	}
}

func (s *Service) Status() observabilityvo.AuditCollectionStatus {
	s.mu.RLock()
	result := copyStatus(s.current)
	s.mu.RUnlock()
	now := s.now().UTC()
	if result.ObservedAt != nil && (!now.Before(result.ObservedAt.Add(observationTTL)) || result.ObservedAt.After(now.Add(5*time.Second))) {
		gap, since := result.HistoricalGap, result.HistoricalGapSince
		result = observabilityvo.UnknownAuditCollectionStatus()
		result.Reason = "audit_producer_status_stale"
		result.HistoricalGap, result.HistoricalGapSince = gap, since
	}
	return result
}

func validSnapshot(v Snapshot, now time.Time) bool {
	if v.SchemaVersion != "1.0" || !validInstanceID(v.InstanceID) || !utcTime(v.ObservedAt) || v.ObservedAt.After(now.Add(5*time.Second)) || !now.Before(v.ObservedAt.Add(observationTTL)) {
		return false
	}
	for _, stamp := range []*time.Time{v.LastDeliveredAt, v.LastFailureAt} {
		if stamp != nil && (!utcTime(*stamp) || stamp.After(v.ObservedAt)) {
			return false
		}
	}
	switch v.Status {
	case "unknown":
		if v.Reason != "delivery_not_observed" || v.LastDeliveredAt != nil {
			return false
		}
	case "healthy":
		if v.Reason != "delivery_observed" || v.LastDeliveredAt == nil || (v.LastFailureAt != nil && v.LastFailureAt.After(*v.LastDeliveredAt)) {
			return false
		}
	case "degraded":
		switch v.Reason {
		case "kafka_not_configured", "publisher_unavailable":
		case "dropped_unavailable", "dropped_invalid", "dropped_queue_full", "dropped_closed", "dropped_retry_exhausted", "dropped_shutdown_timeout":
			if v.DroppedRecords == 0 || v.LastFailureAt == nil || (v.LastDeliveredAt != nil && v.LastDeliveredAt.After(*v.LastFailureAt)) {
				return false
			}
		default:
			return false
		}
	default:
		return false
	}
	// Every confirmed loss needs a timestamp even if a later delivery recovered.
	return v.DroppedRecords == 0 || v.LastFailureAt != nil
}
func utcTime(t time.Time) bool { _, offset := t.Zone(); return !t.IsZero() && offset == 0 }
func copyTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	c := v.UTC()
	return &c
}
func copyUint64(v *uint64) *uint64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
func copyStatus(v observabilityvo.AuditCollectionStatus) observabilityvo.AuditCollectionStatus {
	v.ObservedAt = copyTime(v.ObservedAt)
	v.LastDeliveredAt = copyTime(v.LastDeliveredAt)
	v.LastFailureAt = copyTime(v.LastFailureAt)
	v.HistoricalGapSince = copyTime(v.HistoricalGapSince)
	v.DroppedRecords = copyUint64(v.DroppedRecords)
	if v.HistoricalGap != nil {
		c := *v.HistoricalGap
		v.HistoricalGap = &c
	}
	return v
}

func validInstanceID(id string) bool {
	if id == "" || len(id) > 128 || strings.TrimSpace(id) != id {
		return false
	}
	for _, c := range []byte(id) {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
