// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import "os"

type EvidenceConfig struct {
	Store string
}

func NewEvidenceConfig() EvidenceConfig {
	store := os.Getenv("BKN_TRACE_EVIDENCE_STORE")
	if store == "" {
		store = "memory"
	}
	return EvidenceConfig{Store: store}
}
