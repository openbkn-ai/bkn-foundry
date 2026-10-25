// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

// TraceRootServiceOptions is a bounded selection, not a complete service catalog.
type TraceRootServiceOptions struct {
	Entries []string `json:"entries"`
	HasMore bool     `json:"has_more"`
}
