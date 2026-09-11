// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"encoding/json"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type evidenceJSONDecoder func(field, data string, target any) error

func decodeLegacyEvidenceJSON(_ string, data string, target any) error {
	unmarshalJSON(data, target)
	return nil
}

// Strict decoding is opt-in only for the new snapshot transaction. The same
// columns/scanners are reused without a second query or changed legacy errors.
func (t *transaction) decodeEvidenceJSON(field, data string, target any) error {
	if !t.strictEvidenceJSON {
		return decodeLegacyEvidenceJSON(field, data, target)
	}
	if data == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(data), target); err != nil {
		// Do not expose malformed source content or error values in logs/UI.
		return fmt.Errorf("%w: %s", isessionstore.ErrInvalidEvidenceJSON, field)
	}
	return nil
}
