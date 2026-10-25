// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchtraceaccess

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ihttpaccess/tracequeryport"
)

func (c *Client) SearchTraceSpans(ctx context.Context, q json.RawMessage) (opensearchvo.SearchResult, error) {
	b, e := c.openSearchClient.SearchBounded(ctx, c.traceIndex, q)
	if errors.Is(e, opensearch.ErrSearchResponseBudget) {
		return nil, tracequeryport.ErrSpanResponseBudget
	}
	return b, e
}
