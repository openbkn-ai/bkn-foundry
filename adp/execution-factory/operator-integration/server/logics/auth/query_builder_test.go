// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"fmt"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type queryBuilderTestResource struct {
	ID    string
	Index int
}

func (r *queryBuilderTestResource) GetBizID() string {
	return r.ID
}

func TestIncrementalFetchReportsExactPagination(t *testing.T) {
	resources := make([]*queryBuilderTestResource, interfaces.MaxQuerySize+1)
	resourceIDs := make([]string, len(resources))
	for i := range resources {
		resourceIDs[i] = fmt.Sprintf("skill-%d", i)
		resources[i] = &queryBuilderTestResource{ID: resourceIDs[i], Index: i}
	}

	queryBatch := func(
		_ context.Context, pageSize, _ int, cursor *queryBuilderTestResource,
	) ([]*queryBuilderTestResource, error) {
		start := 0
		if cursor != nil {
			start = cursor.Index + 1
		}
		if start >= len(resources) {
			return nil, nil
		}
		end := min(start+pageSize, len(resources))
		return resources[start:end], nil
	}

	resp, err := NewQueryBuilder[queryBuilderTestResource]().
		SetPage(1, 10).
		SetQueryFunctions(
			func(context.Context) (int64, error) { return int64(len(resources)), nil },
			queryBatch,
		).
		SetAuthFilter(func(context.Context) ([]string, error) { return resourceIDs, nil }).
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(resp.Data) != 10 {
		t.Fatalf("data length = %d; want 10", len(resp.Data))
	}
	if resp.TotalCount != len(resources) {
		t.Fatalf("total_count = %d; want %d", resp.TotalCount, len(resources))
	}
	wantTotalPage := (len(resources) + 9) / 10
	if resp.TotalPage != wantTotalPage {
		t.Fatalf("total_page = %d; want %d", resp.TotalPage, wantTotalPage)
	}
	if !resp.HasNext {
		t.Fatal("has_next = false; want true")
	}
}
