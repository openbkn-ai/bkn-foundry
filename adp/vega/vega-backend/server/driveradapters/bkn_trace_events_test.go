// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

func TestResourceDataArtifactContentPreservesActualQueryAndBusinessResult(t *testing.T) {
	resource := &interfaces.Resource{
		ID: "res_purchase_order", CatalogID: "cat_supplychain",
	}
	params := &interfaces.ResourceDataQueryParams{
		Limit: 20,
		FilterCondition: map[string]any{
			"field": "supplier_id", "operation": "eq", "value": "SUP-001",
		},
		OutputFields: []string{"order_id", "amount"},
		NeedTotal:    true,
	}
	result := &interfaces.ResourceDataQueryResult{
		Entries: []map[string]any{{
			"order_id": "PO-2024-001",
			"amount":   128000,
		}},
		TotalCount: 1,
		NeedTotal:  true,
	}

	queryBody, resultBody := resourceDataArtifactContent(resource, params, result)
	raw, err := json.Marshal(map[string]any{"query": queryBody, "result": resultBody})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"resource_id":"res_purchase_order"`,
		`"catalog_id":"cat_supplychain"`,
		`"supplier_id"`,
		`"SUP-001"`,
		`"output_fields":["order_id","amount"]`,
		`"order_id":"PO-2024-001"`,
		`"amount":128000`,
		`"total_count":1`,
	} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("authorized artifact content missing %s: %s", expected, raw)
		}
	}
}

func TestResourceDataEvidenceTruncatedUsesCursorOrTotal(t *testing.T) {
	if resourceDataEvidenceTruncated(&interfaces.ResourceDataQueryResult{
		Entries: []map[string]any{{"id": "row_1"}},
		Paging:  &interfaces.PagingResponse{},
	}) {
		t.Fatalf("empty PagingResponse must not mark evidence as truncated")
	}

	nextCursor := "cursor_1"
	if !resourceDataEvidenceTruncated(&interfaces.ResourceDataQueryResult{
		Entries: []map[string]any{{"id": "row_1"}},
		Paging:  &interfaces.PagingResponse{NextCursor: &nextCursor},
	}) {
		t.Fatalf("non-empty NextCursor must mark evidence as truncated")
	}

	if !resourceDataEvidenceTruncated(&interfaces.ResourceDataQueryResult{
		Entries:    []map[string]any{{"id": "row_1"}},
		TotalCount: 2,
		Paging:     &interfaces.PagingResponse{},
	}) {
		t.Fatalf("returned rows less than total count must mark evidence as truncated")
	}
}

func TestRawQueryArtifactContentPreservesActualQueryAndBusinessResult(t *testing.T) {
	totalCount := int64(1)
	req := &interfaces.RawQueryRequest{
		Query:           "SELECT supplier_id, amount FROM {{res_purchase_order}} WHERE supplier_id = 'SUP-001'",
		QueryFormat:     interfaces.QueryFormatSQL,
		InputDialect:    "trino",
		QueryTimeoutSec: 30,
		NeedTotal:       true,
		Paging: interfaces.PagingRequest{
			Mode:   interfaces.PagingModeSingle,
			Offset: 0,
			Limit:  20,
		},
	}
	resp := &interfaces.RawQueryResponse{
		Columns: []interfaces.ColumnInfo{
			{Name: "supplier_id", Type: "string"},
			{Name: "amount", Type: "integer"},
		},
		Entries: []map[string]any{{
			"supplier_id": "SUP-001",
			"amount":      128000,
		}},
		TotalCount: &totalCount,
		Paging:     &interfaces.PagingResponse{},
	}

	queryBody, resultBody := rawQueryArtifactContent(req, resp)
	raw, err := json.Marshal(map[string]any{"query": queryBody, "result": resultBody})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"query":"SELECT supplier_id, amount FROM {{res_purchase_order}} WHERE supplier_id = 'SUP-001'"`,
		`"query_format":"sql"`,
		`"input_dialect":"trino"`,
		`"query_timeout_sec":30`,
		`"need_total":true`,
		`"supplier_id":"SUP-001"`,
		`"amount":128000`,
		`"total_count":1`,
		`"columns":[{"name":"supplier_id","type":"string"},{"name":"amount","type":"integer"}]`,
	} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("authorized raw-query artifact content missing %s: %s", expected, raw)
		}
	}
}

func TestRawQueryEvidenceUsesExecutedResourceBindings(t *testing.T) {
	nextCursor := "cursor_next"
	req := &interfaces.RawQueryRequest{
		Query:       "SELECT * FROM {{res_purchase_order}}",
		QueryFormat: interfaces.QueryFormatSQL,
		Paging: interfaces.PagingRequest{
			Mode:  interfaces.PagingModeCursor,
			Limit: 1,
		},
	}
	resp := &interfaces.RawQueryResponse{
		Entries:     []map[string]any{{"order_id": "PO-2024-001"}},
		ResourceIDs: []string{"res_purchase_order", "res_supplier"},
		Paging:      &interfaces.PagingResponse{NextCursor: &nextCursor},
	}

	subject, refs := rawQueryEvidenceDetails(req, resp)

	if subject.Operation != "data.raw_query" ||
		subject.ReturnedCount != 1 ||
		!subject.Truncated ||
		subject.QueryHash != bkntrace.HashValue(req.Query) {
		t.Fatalf("unexpected subject: %#v", subject)
	}
	if len(refs) != 2 ||
		refs[0].RefID != "resource:res_purchase_order" ||
		refs[1].RefID != "resource:res_supplier" {
		t.Fatalf("unexpected executed resource refs: %#v", refs)
	}
}
