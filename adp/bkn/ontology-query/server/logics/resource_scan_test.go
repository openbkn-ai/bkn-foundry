// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// scriptedVega answers each request with the next scripted response.
type scriptedVega struct {
	responses []*interfaces.DatasetQueryResponse
	errs      []error
	history   []interfaces.ResourceDataPagingRequest
	requests  []interfaces.ResourceDataQueryParams
	closed    []string
}

func (v *scriptedVega) QueryResourceData(_ context.Context, _ string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	index := len(v.history)
	v.history = append(v.history, params.Paging)
	v.requests = append(v.requests, *params)
	// Like Vega, a cursor continuation carries the cursor and nothing else.
	if params.Paging.Cursor != "" && !reflect.DeepEqual(*params,
		interfaces.ResourceDataQueryParams{Paging: interfaces.ResourceDataPagingRequest{Cursor: params.Paging.Cursor}}) {
		return nil, interfaces.NewVegaDownstreamError(http.StatusBadRequest,
			`{"error_details":"cursor continuation must contain only paging.cursor"}`)
	}
	if index < len(v.errs) && v.errs[index] != nil {
		return nil, v.errs[index]
	}
	return v.responses[index], nil
}

func (v *scriptedVega) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{}, nil
}

func (v *scriptedVega) CloseResourceDataCursor(_ context.Context, _, cursor string) error {
	v.closed = append(v.closed, cursor)
	return nil
}

func rowsPage(size int, next string) *interfaces.DatasetQueryResponse {
	page := &interfaces.DatasetQueryResponse{Entries: make([]map[string]any, size),
		Paging: &interfaces.ResourceDataPagingResponse{}}
	if next != "" {
		page.Paging.NextCursor = &next
	}
	return page
}

// scanQuery is the query every scan test reads, so a request that drops or
// repeats it shows up against Vega's rules.
func scanQuery() interfaces.ResourceDataQueryParams {
	ignoreLocalIndex := true
	return interfaces.ResourceDataQueryParams{
		IgnoreLocalIndex: &ignoreLocalIndex,
		FilterCondition:  map[string]any{"field": "campaign_id", "operation": "in", "value": []any{104}},
		Sort:             []*interfaces.SortParams{{Field: "campaign_id"}, {Field: "customer_id"}},
		OutputFields:     []string{"campaign_id", "customer_id"},
		NeedTotal:        true,
	}
}

func scanCounting(vega interfaces.VegaBackendAccess, stopAfter int) (int, error) {
	read := 0
	err := ScanResourceData(context.Background(), vega, "resource", scanQuery(),
		func(rows []map[string]any) (bool, error) {
			read += len(rows)
			return stopAfter <= 0 || read < stopAfter, nil
		})
	return read, err
}

func TestScanResourceDataEndsOnAShortFirstPage(t *testing.T) {
	vega := &scriptedVega{responses: []*interfaces.DatasetQueryResponse{rowsPage(1, "")}}
	read, err := scanCounting(vega, 0)
	if err != nil || read != 1 || len(vega.history) != 1 ||
		vega.history[0].Mode != interfaces.ResourceDataPagingModeCursor {
		t.Fatalf("read %d rows in %#v, err %v", read, vega.history, err)
	}
}

func TestScanResourceDataFollowsACursorPastTheFirstPage(t *testing.T) {
	first := rowsPage(1, "c1")
	first.TotalCount = new(int64(4))
	vega := &scriptedVega{responses: []*interfaces.DatasetQueryResponse{
		first, rowsPage(2, "c2"), rowsPage(1, ""),
	}}
	read, err := scanCounting(vega, 0)
	if err != nil || read != 4 || len(vega.history) != 3 || len(vega.closed) != 0 {
		t.Fatalf("read %d rows in %d requests, err %v", read, len(vega.history), err)
	}
	if vega.history[0].Mode != interfaces.ResourceDataPagingModeCursor || vega.history[0].Limit != 0 ||
		vega.history[1].Cursor != "c1" || vega.history[2].Cursor != "c2" {
		t.Fatalf("paging = %#v", vega.history)
	}
	got := vega.requests[0]
	got.Paging = interfaces.ResourceDataPagingRequest{}
	if !reflect.DeepEqual(got, scanQuery()) {
		t.Fatalf("initial request = %#v, want the scan query", vega.requests[0])
	}
}

func TestScanResourceDataReportsUnsupportedCursorPaging(t *testing.T) {
	unsupported := interfaces.NewVegaDownstreamError(http.StatusNotImplemented, `{"error_code":"VegaBackend.Query.InvalidParameter"}`)
	vega := &scriptedVega{
		errs: []error{unsupported},
	}
	read, err := scanCounting(vega, 0)
	if !errors.Is(err, unsupported) || read != 0 || len(vega.history) != 1 {
		t.Fatalf("read %d rows in %#v, err %v", read, vega.history, err)
	}
}

func TestScanResourceDataStopsWhenVisitStops(t *testing.T) {
	vega := &scriptedVega{responses: []*interfaces.DatasetQueryResponse{rowsPage(2, "remaining")}}
	read, err := scanCounting(vega, 2)
	if err != nil || read != 2 || len(vega.history) != 1 || !reflect.DeepEqual(vega.closed, []string{"remaining"}) {
		t.Fatalf("read %d rows in %d requests, closed %v, err %v", read, len(vega.history), vega.closed, err)
	}
}

func TestScanResourceDataFailsWhenAContinuationFails(t *testing.T) {
	failure := errors.New("vega unavailable")
	vega := &scriptedVega{
		responses: []*interfaces.DatasetQueryResponse{rowsPage(2, "c1"), nil},
		errs:      []error{nil, failure},
	}
	if _, err := scanCounting(vega, 0); !errors.Is(err, failure) || !reflect.DeepEqual(vega.closed, []string{"c1"}) {
		t.Fatalf("error = %v, closed = %v", err, vega.closed)
	}
}
