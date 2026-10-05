// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"

// RecordIntegrity is a current read of registered call records, independent
// of historical assembly state, execution success and claim support.
type RecordIntegrity = sessionvo.RecordIntegrity
type MissingRecord = sessionvo.MissingRecord
