// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const frozenContractSHA = "0016ad359b11d162e04bb11a78784c33fad0ec8d"

// ManifestArtifact is the payload-free C1 handoff from the source-only
// snapshot process to the center-store admin command.
type ManifestArtifact struct {
	ManifestID       string                  `json:"manifest_id"`
	ContractSHA      string                  `json:"contract_sha"`
	SourceSnapshotAt string                  `json:"source_snapshot_at"`
	EntryCount       string                  `json:"entry_count"`
	EntriesDigest    string                  `json:"entries_digest"`
	Entries          []ManifestArtifactEntry `json:"entries"`
}

type ManifestArtifactEntry struct {
	Classification       string `json:"classification"`
	ClassificationReason string `json:"classification_reason"`
	EventID              string `json:"event_id"`
	ManifestID           string `json:"manifest_id"`
	PayloadHash          string `json:"payload_hash"`
	ProducerEpoch        string `json:"producer_epoch"`
	ProducerID           string `json:"producer_id"`
	ProducerSequence     string `json:"producer_sequence"`
	ProducerStreamID     string `json:"producer_stream_id"`
	SourcePrimaryKey     string `json:"source_primary_key"`
	SourceService        string `json:"source_service"`
	SourceStatus         string `json:"source_status"`
	SourceTable          string `json:"source_table"`
}

var manifestArtifactFields = []string{"manifest_id", "contract_sha", "source_snapshot_at", "entry_count", "entries_digest", "entries"}
var manifestArtifactEntryFields = []string{"classification", "classification_reason", "event_id", "manifest_id", "payload_hash", "producer_epoch", "producer_id", "producer_sequence", "producer_stream_id", "source_primary_key", "source_service", "source_status", "source_table"}

func (artifact *ManifestArtifact) UnmarshalJSON(data []byte) error {
	type plain ManifestArtifact
	var decoded plain
	if err := decodeExactJSONObject(data, manifestArtifactFields, &decoded); err != nil {
		return err
	}
	*artifact = ManifestArtifact(decoded)
	return nil
}

func (entry *ManifestArtifactEntry) UnmarshalJSON(data []byte) error {
	type plain ManifestArtifactEntry
	var decoded plain
	if err := decodeExactJSONObject(data, manifestArtifactEntryFields, &decoded); err != nil {
		return err
	}
	*entry = ManifestArtifactEntry(decoded)
	return nil
}

func decodeExactJSONObject(data []byte, expected []string, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("Evidence migration artifact object is invalid")
	}
	fields := make(map[string]json.RawMessage, len(expected))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("Evidence migration artifact object key is invalid")
		}
		if _, exists := fields[key]; exists {
			return errors.New("Evidence migration artifact contains a duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if len(fields) != len(expected) {
		return errors.New("Evidence migration artifact fields do not match C1")
	}
	for _, key := range expected {
		if _, exists := fields[key]; !exists {
			return errors.New("Evidence migration artifact fields do not match C1")
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return err
	}
	return nil
}

func (artifact ManifestArtifact) adminInput(actor string) (manifestAdminInput, error) {
	if artifact.ManifestID == "" || artifact.ContractSHA != frozenContractSHA || actor == "" {
		return manifestAdminInput{}, errors.New("Evidence migration artifact header is invalid")
	}
	if err := validatePrintableASCII(artifact.ManifestID); err != nil {
		return manifestAdminInput{}, err
	}
	if err := validatePrintableASCII(actor); err != nil {
		return manifestAdminInput{}, err
	}
	parsedSnapshotAt, err := time.Parse(time.RFC3339Nano, artifact.SourceSnapshotAt)
	if err != nil || parsedSnapshotAt.UTC().Format("2006-01-02T15:04:05.000Z") != artifact.SourceSnapshotAt {
		return manifestAdminInput{}, errors.New("Evidence migration artifact snapshot timestamp is not canonical UTC milliseconds")
	}
	snapshotAt, err := normalizeSnapshotTime(artifact.SourceSnapshotAt)
	if err != nil {
		return manifestAdminInput{}, err
	}
	count, err := canonicalUint(artifact.EntryCount)
	if err != nil || count != uint64(len(artifact.Entries)) {
		return manifestAdminInput{}, errors.New("Evidence migration artifact entry count mismatch")
	}
	entries := make([]frozenEntry, 0, len(artifact.Entries))
	for _, value := range artifact.Entries {
		entry := frozenEntry{
			Classification: value.Classification, ClassificationReason: value.ClassificationReason,
			EventID: value.EventID, ManifestID: value.ManifestID, PayloadHash: value.PayloadHash,
			ProducerEpoch: value.ProducerEpoch, ProducerID: value.ProducerID, ProducerSequence: value.ProducerSequence,
			ProducerStreamID: value.ProducerStreamID, SourcePrimaryKey: value.SourcePrimaryKey,
			SourceService: value.SourceService, SourceStatus: value.SourceStatus, SourceTable: value.SourceTable,
		}
		if entry.ManifestID != artifact.ManifestID {
			return manifestAdminInput{}, errors.New("Evidence migration artifact entry manifest ID mismatch")
		}
		if entry.Classification != "publish" && entry.Classification != "verify_delivered" && entry.Classification != "coverage_gap" {
			return manifestAdminInput{}, errors.New("Evidence migration artifact classification is invalid")
		}
		if err := validateArtifactEntry(entry); err != nil {
			return manifestAdminInput{}, err
		}
		entries = append(entries, entry)
	}
	digest, err := entriesDigest(entries)
	if err != nil || digest != artifact.EntriesDigest {
		return manifestAdminInput{}, errors.New("Evidence migration artifact entries digest mismatch")
	}
	return manifestAdminInput{
		ManifestID: artifact.ManifestID, ContractSHA: artifact.ContractSHA,
		SourceSnapshotAt: snapshotAt, EntriesDigest: artifact.EntriesDigest, Actor: actor, Entries: entries,
	}, nil
}

func validateArtifactEntry(entry frozenEntry) error {
	type sourceContract struct{ table, producerID, streamID string }
	sources := map[string]sourceContract{
		"bkn-backend":    {table: "bkn_backend_trace_outbox", producerID: "bkn-backend", streamID: "bkn-backend"},
		"ontology-query": {table: "ontology_query_trace_outbox", producerID: "bkn-ontology", streamID: "ontology-query"},
	}
	source, ok := sources[entry.SourceService]
	if !ok || entry.SourceTable != source.table || entry.SourcePrimaryKey == "" || entry.SourceStatus == "" || entry.ClassificationReason == "" {
		return errors.New("Evidence migration artifact source identity is invalid")
	}
	primaryKey, err := canonicalUint(entry.SourcePrimaryKey)
	if err != nil || primaryKey == 0 {
		return errors.New("Evidence migration artifact source primary key is invalid")
	}
	identities := []string{entry.EventID, entry.PayloadHash, entry.ProducerID, entry.ProducerStreamID, entry.ProducerEpoch, entry.ProducerSequence}
	hasIdentity := true
	allEmpty := true
	for _, value := range identities {
		hasIdentity = hasIdentity && value != ""
		allEmpty = allEmpty && value == ""
	}
	switch entry.Classification {
	case "publish", "verify_delivered":
		if !hasIdentity || entry.ProducerID != source.producerID || !validSourceStream(entry.ProducerStreamID, source.streamID) {
			return errors.New("Evidence migration artifact publish identity is incomplete or invalid")
		}
		if len(entry.PayloadHash) != 64 || strings.Trim(entry.PayloadHash, "0123456789abcdef") != "" {
			return errors.New("Evidence migration artifact payload hash is invalid")
		}
		for _, value := range []string{entry.ProducerEpoch, entry.ProducerSequence} {
			number, parseErr := canonicalUint(value)
			if parseErr != nil || number == 0 {
				return errors.New("Evidence migration artifact producer sequence identity is invalid")
			}
		}
		if entry.Classification == "verify_delivered" {
			if entry.SourceStatus != "delivered" || entry.ClassificationReason != "delivered" {
				return errors.New("Evidence migration artifact delivered classification is inconsistent")
			}
		} else if !((entry.SourceStatus == "pending" && entry.ClassificationReason == "pending") ||
			(entry.SourceStatus == "retry" && entry.ClassificationReason == "retry") ||
			(entry.SourceStatus == "processing" && entry.ClassificationReason == "expired_lease")) {
			return errors.New("Evidence migration artifact publish classification is inconsistent")
		}
	case "coverage_gap":
		if !allEmpty {
			return errors.New("Evidence migration coverage-gap entry must not carry Event identity")
		}
		switch entry.ClassificationReason {
		case "conflict", "abandoned", "dlq":
			if entry.SourceStatus != entry.ClassificationReason {
				return errors.New("Evidence migration coverage-gap status and reason differ")
			}
		case "unknown_status":
			if entry.SourceStatus == "pending" || entry.SourceStatus == "retry" || entry.SourceStatus == "processing" || entry.SourceStatus == "delivered" || entry.SourceStatus == "conflict" || entry.SourceStatus == "abandoned" || entry.SourceStatus == "dlq" {
				return errors.New("Evidence migration unknown-status reason is inconsistent")
			}
		case "bad_payload", "source_identity_mismatch":
			if entry.SourceStatus != "pending" && entry.SourceStatus != "retry" && entry.SourceStatus != "processing" && entry.SourceStatus != "delivered" {
				return errors.New("Evidence migration coverage-gap reason is inconsistent with source status")
			}
		default:
			return fmt.Errorf("Evidence migration coverage-gap reason %q is invalid", entry.ClassificationReason)
		}
	}
	return nil
}

func validSourceStream(value, base string) bool {
	return value == base || (strings.HasPrefix(value, base+":") && len(value) > len(base)+1)
}

// ActivateArtifact validates the frozen C1 artifact before creating and
// activating its immutable center-store manifest.
func (s *Store) ActivateArtifact(ctx context.Context, artifact ManifestArtifact, actor string) error {
	input, err := artifact.adminInput(actor)
	if err != nil {
		return err
	}
	return s.CreateDraftAndActivate(ctx, input)
}
