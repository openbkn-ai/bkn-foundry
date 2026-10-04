// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import "context"

const maxExpectedEvidenceEvents = 128

// EvidenceExpectation is the immutable set of planned events for one attempt.
// It reports enqueue outcomes; only Core can confirm ledger durability.
type EvidenceExpectation struct {
	Version uint32                  `json:"version"`
	Closed  bool                    `json:"closed"`
	Events  []ExpectedEvidenceEvent `json:"events"`
}
type ExpectedEvidenceEvent struct {
	EventID            string `json:"event_id"`
	EventType          string `json:"event_type"`
	PayloadHash        string `json:"payload_hash"`
	ProducerID         string `json:"producer_id"`
	PublishDisposition string `json:"publish_disposition"`
	DropReason         string `json:"drop_reason,omitempty"`
}

func freezeEvidenceExpectation(ctx context.Context) *EvidenceExpectation {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return nil
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	if outcome.frozen == nil {
		outcome.closed = true
		outcome.frozen = &EvidenceExpectation{Version: 1, Closed: !outcome.overflow && !outcome.conflict, Events: append([]ExpectedEvidenceEvent{}, outcome.events...)}
	}
	result := *outcome.frozen
	result.Events = append([]ExpectedEvidenceEvent{}, result.Events...)
	return &result
}
func evidenceExpectationDurability(e *EvidenceExpectation) string {
	if e == nil {
		return "pending"
	}
	if !e.Closed {
		return "failed"
	}
	for _, event := range e.Events {
		if event.PublishDisposition != "accepted" {
			return "failed"
		}
	}
	if len(e.Events) > 0 {
		return "pending"
	}
	return "durable"
}

func evidenceExpectationFailureReason(ctx context.Context) string {
	outcome := evidenceOutcomeFromContext(ctx)
	if outcome == nil {
		return ""
	}
	outcome.mu.Lock()
	defer outcome.mu.Unlock()
	if outcome.overflow {
		return "evidence_expectation_limit_exceeded"
	}
	if outcome.conflict {
		return "evidence_expectation_conflict"
	}
	return ""
}
