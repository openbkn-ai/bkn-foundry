// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package observabilityvo

import "time"

// AuditCollectionStatus is observed publisher metadata, not proof of complete
// collection or consumer delivery. It describes one configured Safe instance.
// Null counts/history mean unknown; recovery never repairs a historical gap.
type AuditCollectionStatus struct {
	Scope              string     `json:"scope"`
	InstanceID         string     `json:"instance_id,omitempty"`
	Status             string     `json:"status" enums:"unknown,healthy,degraded"`
	Reason             string     `json:"reason"`
	ObservedAt         *time.Time `json:"observed_at"`
	LastDeliveredAt    *time.Time `json:"last_delivered_at"`
	LastFailureAt      *time.Time `json:"last_failure_at"`
	DroppedRecords     *uint64    `json:"dropped_records"`
	HistoricalGap      *bool      `json:"historical_gap"`
	HistoricalGapSince *time.Time `json:"historical_gap_since"`
}

func UnknownAuditCollectionStatus() AuditCollectionStatus {
	return AuditCollectionStatus{Scope: "bkn-safe-publisher-instance", Status: "unknown", Reason: "audit_producer_not_monitored"}
}
