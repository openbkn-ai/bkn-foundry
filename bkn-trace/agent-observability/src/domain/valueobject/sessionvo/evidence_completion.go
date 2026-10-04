// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// These bounds match the existing evidence snapshot read budget.
const MaxExpectedEvidenceEvents = 128
const MaxEvidenceConfirmationBytes = 32 << 20

type ExpectedEvidenceEvent struct {
	EventID            string `json:"event_id"`
	EventType          string `json:"event_type"`
	PayloadHash        string `json:"payload_hash"`
	ProducerID         string `json:"producer_id"`
	PublishDisposition string `json:"publish_disposition"`
	DropReason         string `json:"drop_reason,omitempty"`
}

type EvidenceExpectation struct {
	Version uint32                  `json:"version"`
	Closed  bool                    `json:"closed"`
	Events  []ExpectedEvidenceEvent `json:"events"`
}

// EvidenceCompletion is immutable Trace metadata. It is stored beside the
// terminal payload envelope, never in the business payload's inline content.
type EvidenceCompletion struct {
	Expectation             *EvidenceExpectation `json:"expectation,omitempty"`
	OriginalDurability      EvidenceDurability   `json:"original_durability"`
	OriginalObservedRefs    []string             `json:"original_observed_refs"`
	OriginalPartialReasons  []string             `json:"original_partial_reasons"`
	RejectionReason         string               `json:"rejection_reason,omitempty"`
	RejectedExpectationHash string               `json:"rejected_expectation_hash,omitempty"`
}

func CopyEvidenceCompletion(source *EvidenceCompletion) *EvidenceCompletion {
	if source == nil {
		return nil
	}
	result := *source
	result.OriginalObservedRefs = append([]string{}, source.OriginalObservedRefs...)
	result.OriginalPartialReasons = append([]string{}, source.OriginalPartialReasons...)
	if source.Expectation != nil {
		expectation := *source.Expectation
		if source.Expectation.Events != nil {
			expectation.Events = append([]ExpectedEvidenceEvent{}, source.Expectation.Events...)
		}
		result.Expectation = &expectation
	}
	return &result
}

// EvidenceExpectationShapeValid is shared by admission and stored-JSON decoding.
// A rejected expectation is kept separately and can never confirm evidence.
func EvidenceExpectationShapeValid(expectation *EvidenceExpectation) bool {
	if expectation == nil || expectation.Version != 1 || !expectation.Closed || len(expectation.Events) > MaxExpectedEvidenceEvents {
		return false
	}
	ids := make(map[string]struct{}, len(expectation.Events))
	for _, event := range expectation.Events {
		if strings.TrimSpace(event.EventID) == "" || len(event.EventID) > 192 || strings.TrimSpace(event.EventType) == "" || len(event.EventType) > 192 || len(event.ProducerID) > 128 || len(event.DropReason) > 128 {
			return false
		}
		if _, duplicate := ids[event.EventID]; duplicate {
			return false
		}
		ids[event.EventID] = struct{}{}
		switch event.PublishDisposition {
		case "accepted":
			hash, err := hex.DecodeString(event.PayloadHash)
			if err != nil || len(hash) != sha256.Size || event.PayloadHash != strings.ToLower(event.PayloadHash) || strings.TrimSpace(event.ProducerID) == "" || event.DropReason != "" {
				return false
			}
		case "dropped":
			if strings.TrimSpace(event.DropReason) == "" || len(event.PayloadHash) > 64 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
