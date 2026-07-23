// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"testing"

	"vega-backend/interfaces"
)

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
