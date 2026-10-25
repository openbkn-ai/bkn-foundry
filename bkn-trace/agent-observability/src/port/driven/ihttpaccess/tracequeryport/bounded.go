// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package tracequeryport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
)

var ErrSpanResponseBudget = errors.New("span response exceeds budget")

type BoundedTraceQueryPort interface {
	SearchTraceSpans(context.Context, json.RawMessage) (opensearchvo.SearchResult, error)
}
