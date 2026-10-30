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
	err := ScanResourceData(context.Background(), vega, "resource", scanQuery(), 2,
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
		vega.history[0].Mode != interfaces.ResourceDataPagingModeSingle {
		t.Fatalf("read %d rows in %#v, err %v", read, vega.history, err)
	}
}

func TestScanResourceDataFollowsACursorPastTheFirstPage(t *testing.T) {
	vega := &scriptedVega{responses: []*interfaces.DatasetQueryResponse{
		rowsPage(2, ""), rowsPage(2, "c1"), rowsPage(2, "c2"), rowsPage(1, ""),
	}}
	read, err := scanCounting(vega, 0)
	if err != nil || read != 5 || len(vega.history) != 4 {
		t.Fatalf("read %d rows in %d requests, err %v", read, len(vega.history), err)
	}
	if vega.history[1].Mode != interfaces.ResourceDataPagingModeCursor || vega.history[1].Offset != 0 ||
		vega.history[2].Cursor != "c1" || vega.history[3].Cursor != "c2" {
		t.Fatalf("paging = %#v", vega.history)
	}
	// The first page and the cursor open carry the query; continuations do not.
	for i := 0; i < 2; i++ {
		got := vega.requests[i]
		got.Paging = interfaces.ResourceDataPagingRequest{}
		if !reflect.DeepEqual(got, scanQuery()) {
			t.Fatalf("request %d = %#v, want the scan query", i, vega.requests[i])
		}
	}
}

func TestScanResourceDataContinuesByOffsetWithoutCursorPaging(t *testing.T) {
	unsupported := interfaces.NewVegaDownstreamError(http.StatusNotImplemented, `{"error_code":"VegaBackend.Query.InvalidParameter"}`)
	vega := &scriptedVega{
		responses: []*interfaces.DatasetQueryResponse{rowsPage(2, ""), nil, rowsPage(2, ""), rowsPage(0, "")},
		errs:      []error{nil, unsupported},
	}
	read, err := scanCounting(vega, 0)
	if err != nil || read != 4 || len(vega.history) != 4 ||
		vega.history[2].Offset != 2 || vega.history[3].Offset != 4 {
		t.Fatalf("read %d rows in %#v, err %v", read, vega.history, err)
	}
}

func TestScanResourceDataStopsWhenVisitStops(t *testing.T) {
	vega := &scriptedVega{responses: []*interfaces.DatasetQueryResponse{rowsPage(2, "")}}
	read, err := scanCounting(vega, 2)
	if err != nil || read != 2 || len(vega.history) != 1 {
		t.Fatalf("read %d rows in %d requests, err %v", read, len(vega.history), err)
	}
}

func TestScanResourceDataFailsWhenAContinuationFails(t *testing.T) {
	failure := errors.New("vega unavailable")
	vega := &scriptedVega{
		responses: []*interfaces.DatasetQueryResponse{rowsPage(2, ""), rowsPage(2, "c1"), nil},
		errs:      []error{nil, nil, failure},
	}
	if _, err := scanCounting(vega, 0); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
}
