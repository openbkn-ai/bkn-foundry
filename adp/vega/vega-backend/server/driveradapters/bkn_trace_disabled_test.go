// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"os"
	"testing"

	"vega-backend/interfaces"
)

func TestVegaEvidenceEmittersReturnBeforeWorkWhenDisabled(t *testing.T) {
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", "")
	_ = os.Unsetenv("BKN_TRACE_EVIDENCE_INGEST_URL")

	resource := &interfaces.Resource{
		ID:        "res_customer_table",
		CatalogID: "cat_prod",
		Category:  interfaces.ResourceCategoryTable,
	}

	emitResourceReadEvidence(nil, context.Background(), "data.catalog.get", []*interfaces.Resource{resource}, 1, map[string]any{"unsafe": "would_hash"})
	emitResourceDataEvidence(nil, context.Background(), resource, nil, &interfaces.ResourceDataQueryResult{
		Entries: []map[string]any{{"customer_id": "C-10086"}},
		Paging:  &interfaces.PagingResponse{},
	})
}
