// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import "time"

type IdempotencyRecord struct {
	Scope                   string
	Owner                   Owner
	ExternalConversationKey string
	IdempotencyKey          string
	RequestHash             string
	ResourceType            string
	ResourceID              string
	CreatedAt               time.Time
}
