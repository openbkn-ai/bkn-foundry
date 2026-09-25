// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

type closureHeader struct {
	ActivatedAt, ClosedAt, ContractSHA, EntriesDigest, EntryCount, ManifestID, SourceSnapshotAt string
	TerminalCounts                                                                              closureCounts
}

type closureCounts struct {
	Conflict, CoverageGap, LedgerCommitted, Rejected, VerifiedDelivered string
}

type closureResult struct {
	Adjudication, EntryID, FirstObservation, FirstObservedAt string
	KafkaOffset, KafkaPartition, KafkaTopic                  *string
	LastObservation, LastObservedAt                          string
	LedgerIngestSequence, ReasonCode                         *string
	ManifestID                                               string
}

func closureDigest(header closureHeader, results []closureResult) (string, error) {
	if header.ManifestID == "" || !validClosureTime(header.ActivatedAt) || !validClosureTime(header.ClosedAt) || !validClosureTime(header.SourceSnapshotAt) {
		return "", errors.New("invalid Evidence migration closure header")
	}
	entryCount, err := canonicalUint(header.EntryCount)
	if err != nil || entryCount != uint64(len(results)) {
		return "", errors.New("evidence migration closure entry count mismatch")
	}
	counts := map[string]uint64{"conflict": 0, "coverage_gap": 0, "ledger_committed": 0, "rejected": 0, "verified_delivered": 0}
	ordered := append([]closureResult(nil), results...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].EntryID < ordered[j].EntryID })
	for i, result := range ordered {
		if result.EntryID == "" || (i > 0 && ordered[i-1].EntryID == result.EntryID) || result.ManifestID != header.ManifestID {
			return "", errors.New("invalid or duplicate Evidence migration closure result")
		}
		if _, found := counts[result.Adjudication]; !found {
			return "", errors.New("invalid Evidence migration closure adjudication")
		}
		counts[result.Adjudication]++
		if !validClosureTime(result.FirstObservedAt) || !validClosureTime(result.LastObservedAt) {
			return "", errors.New("invalid Evidence migration closure result timestamp")
		}
		for _, value := range []string{result.Adjudication, result.EntryID, result.FirstObservation, result.FirstObservedAt, result.LastObservation, result.LastObservedAt, result.ManifestID} {
			if err := validatePrintableASCII(value); err != nil {
				return "", err
			}
		}
		if result.ReasonCode != nil {
			if err := validatePrintableASCII(*result.ReasonCode); err != nil {
				return "", err
			}
		}
		if result.LedgerIngestSequence != nil {
			if err := validatePrintableASCII(*result.LedgerIngestSequence); err != nil {
				return "", err
			}
			if _, err := canonicalUint(*result.LedgerIngestSequence); err != nil {
				return "", err
			}
		}
		coords := []*string{result.KafkaTopic, result.KafkaPartition, result.KafkaOffset}
		present := 0
		for _, value := range coords {
			if value != nil {
				present++
				if err := validatePrintableASCII(*value); err != nil {
					return "", err
				}
			}
		}
		if present != 0 && present != len(coords) {
			return "", errors.New("incomplete Evidence migration Kafka coordinate")
		}
		if present == len(coords) {
			if *result.KafkaTopic != "openbkn.evidence.v1" {
				return "", errors.New("invalid Evidence migration Kafka topic")
			}
			if _, err := canonicalUint(*result.KafkaPartition); err != nil {
				return "", err
			}
			if _, err := canonicalUint(*result.KafkaOffset); err != nil {
				return "", err
			}
		}
	}
	for name, value := range map[string]string{
		"conflict": header.TerminalCounts.Conflict, "coverage_gap": header.TerminalCounts.CoverageGap,
		"ledger_committed": header.TerminalCounts.LedgerCommitted, "rejected": header.TerminalCounts.Rejected,
		"verified_delivered": header.TerminalCounts.VerifiedDelivered,
	} {
		count, err := canonicalUint(value)
		if err != nil || count != counts[name] {
			return "", fmt.Errorf("evidence migration closure count mismatch: %s", name)
		}
	}
	for _, value := range []string{header.ContractSHA, header.EntriesDigest, header.EntryCount, header.ManifestID, header.SourceSnapshotAt} {
		if err := validatePrintableASCII(value); err != nil {
			return "", err
		}
	}
	raw, err := canonicalClosureBytes(header, ordered)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalClosureBytes(header closureHeader, results []closureResult) ([]byte, error) {
	var b []byte
	b = append(b, `{"manifest_header":{`...)
	fields := []struct{ key, value string }{
		{"activated_at", header.ActivatedAt}, {"closed_at", header.ClosedAt}, {"contract_sha", header.ContractSHA},
		{"entries_digest", header.EntriesDigest}, {"entry_count", header.EntryCount}, {"manifest_id", header.ManifestID}, {"source_snapshot_at", header.SourceSnapshotAt},
	}
	for i, field := range fields {
		if i > 0 {
			b = append(b, ',')
		}
		var err error
		b, err = appendClosurePair(b, field.key, field.value)
		if err != nil {
			return nil, err
		}
	}
	b = append(b, `,"terminal_counts":{`...)
	counts := []struct{ key, value string }{
		{"conflict", header.TerminalCounts.Conflict}, {"coverage_gap", header.TerminalCounts.CoverageGap},
		{"ledger_committed", header.TerminalCounts.LedgerCommitted}, {"rejected", header.TerminalCounts.Rejected}, {"verified_delivered", header.TerminalCounts.VerifiedDelivered},
	}
	for i, field := range counts {
		if i > 0 {
			b = append(b, ',')
		}
		var err error
		b, err = appendClosurePair(b, field.key, field.value)
		if err != nil {
			return nil, err
		}
	}
	b = append(b, `}},"results":[`...)
	for i, result := range results {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '{')
		pairs := []struct{ key, value string }{
			{"adjudication", result.Adjudication}, {"entry_id", result.EntryID}, {"first_observation", result.FirstObservation},
			{"first_observed_at", result.FirstObservedAt},
		}
		for j, field := range pairs {
			if j > 0 {
				b = append(b, ',')
			}
			var err error
			b, err = appendClosurePair(b, field.key, field.value)
			if err != nil {
				return nil, err
			}
		}
		nullableFields := []struct {
			key   string
			value *string
		}{{"kafka_offset", result.KafkaOffset}, {"kafka_partition", result.KafkaPartition}, {"kafka_topic", result.KafkaTopic}}
		for _, field := range nullableFields {
			b = append(b, ',')
			var err error
			b, err = appendClosureNullablePair(b, field.key, field.value)
			if err != nil {
				return nil, err
			}
		}
		pairs = []struct{ key, value string }{
			{"last_observation", result.LastObservation}, {"last_observed_at", result.LastObservedAt},
		}
		for _, field := range pairs {
			b = append(b, ',')
			var err error
			b, err = appendClosurePair(b, field.key, field.value)
			if err != nil {
				return nil, err
			}
		}
		b = append(b, ',')
		var err error
		b, err = appendClosureNullablePair(b, "ledger_ingest_sequence", result.LedgerIngestSequence)
		if err != nil {
			return nil, err
		}
		b = append(b, ',')
		b, err = appendClosurePair(b, "manifest_id", result.ManifestID)
		if err != nil {
			return nil, err
		}
		b = append(b, ',')
		b, err = appendClosureNullablePair(b, "reason_code", result.ReasonCode)
		if err != nil {
			return nil, err
		}
		b = append(b, '}')
	}
	b = append(b, `]}`...)
	return b, nil
}

func appendClosurePair(b []byte, key, value string) ([]byte, error) {
	var err error
	b, err = quote(b, key)
	if err != nil {
		return nil, err
	}
	b = append(b, ':')
	b, err = quote(b, value)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func appendClosureNullablePair(b []byte, key string, value *string) ([]byte, error) {
	var err error
	b, err = quote(b, key)
	if err != nil {
		return nil, err
	}
	b = append(b, ':')
	if value == nil {
		return append(b, "null"...), nil
	}
	return quote(b, *value)
}

func canonicalUint(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, errors.New("noncanonical unsigned decimal value")
	}
	return parsed, nil
}

func validClosureTime(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Location() == time.UTC && parsed.Nanosecond()%int(time.Millisecond) == 0 && parsed.Format("2006-01-02T15:04:05.000Z") == value
}
