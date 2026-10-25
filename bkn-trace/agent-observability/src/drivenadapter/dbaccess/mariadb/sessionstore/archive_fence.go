// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var errArchiveSQLChanged = errors.New("trace archive SQL source changed since freeze; hot SQL data retained")
var errArchiveSQLUnverifiable = errors.New("trace archive lacks verifiable SQL source metadata; cleanup requires operator review")

// The caller holds the Interaction row lock until the whole batch commits.
// Source versions cover lifecycle/receipt/fact writes; integrity verdicts can
// arrive without a source-version increment and must be compared separately.
func verifyFrozenSQLSource(candidate archivesvc.Candidate, version uint64, currentIntegrity []byte) error {
	var archived struct {
		Interaction struct {
			ID        string          `json:"interaction_id"`
			Version   *uint64         `json:"record_integrity_version"`
			Integrity json.RawMessage `json:"stored_record_integrity"`
		} `json:"interaction"`
	}
	if json.Unmarshal(candidate.Payload, &archived) != nil || archived.Interaction.ID != candidate.ID || archived.Interaction.Version == nil {
		return errArchiveSQLUnverifiable
	}
	if *archived.Interaction.Version != version {
		return errArchiveSQLChanged
	}
	frozen, err := canonicalArchiveIntegrity(archived.Interaction.Integrity)
	if err != nil {
		return err
	}
	current, err := canonicalArchiveIntegrity(currentIntegrity)
	if err != nil {
		return err
	}
	if !bytes.Equal(frozen, current) {
		return errArchiveSQLChanged
	}
	return nil
}

// Compare the entire captured object, retaining exact JSON numbers and ignoring
// key ordering/whitespace. Do not drop unknown verdict fields during comparison.
func canonicalArchiveIntegrity(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > isessionstore.MaxStoredRecordIntegrityBytes {
		return nil, errArchiveSQLUnverifiable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return nil, errArchiveSQLUnverifiable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errArchiveSQLUnverifiable
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, errArchiveSQLUnverifiable
	}
	return result, nil
}
