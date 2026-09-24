// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
)

var stableEvidenceReason = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// RecordKafkaRejection persists only hashes, Kafka coordinates, bounded
// identities, and a stable reason. It intentionally never stores key/value or
// raw header bytes.
func (s *Store) RecordKafkaRejection(ctx context.Context, record ievidenceadmission.Record, details ievidenceadmission.RejectionDetails) error {
	if record.Topic != "openbkn.evidence.v1" || record.Partition < 0 || record.Offset < 0 || record.BrokerTime.IsZero() || !stableEvidenceReason.MatchString(details.ReasonCode) {
		return errors.New("invalid Evidence Kafka rejection decision")
	}
	keyHash := sha256.Sum256([]byte(record.Key))
	valueHash := sha256.Sum256(record.Value)
	bitmap := headerPresenceBitmap(record.Headers)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bkn_trace_evidence_ingest_rejections (
			topic, partition_id, offset_id, broker_log_append_at,
			record_key_sha256, record_value_sha256, record_value_bytes,
			header_presence_bitmap, event_id, payload_hash, producer_id,
			producer_stream_id, migration_id, reason_code, first_detected_at,
			last_detected_at, delivery_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6), 1)
		ON DUPLICATE KEY UPDATE last_detected_at=UTC_TIMESTAMP(6), delivery_count=delivery_count+1`,
		record.Topic, record.Partition, record.Offset, record.BrokerTime.UTC(),
		hex.EncodeToString(keyHash[:]), hex.EncodeToString(valueHash[:]), len(record.Value), bitmap,
		details.EventID, details.PayloadHash, details.ProducerID, record.ProducerStreamID,
		details.MigrationID, details.ReasonCode,
	)
	if err != nil {
		return fmt.Errorf("persist Evidence Kafka rejection decision: %w", err)
	}
	return nil
}

func headerPresenceBitmap(headers []ievidenceadmission.Header) uint64 {
	indices := map[string]uint{
		"content-type":              0,
		"bkn-trace-schema-version":  1,
		"capture_policy_revision":   2,
		"producer_instance_id":      3,
		"bkn-evidence-record-class": 4,
		"bkn-evidence-migration-id": 5,
	}
	var bitmap uint64
	for _, header := range headers {
		if index, found := indices[header.Key]; found {
			bitmap |= uint64(1) << index
		}
	}
	return bitmap
}
