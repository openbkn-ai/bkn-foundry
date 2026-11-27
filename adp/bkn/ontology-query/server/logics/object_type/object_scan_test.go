// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
	"go.uber.org/mock/gomock"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

// scanPages scripts Vega pages of consecutive customers. Every page but the
// last carries a cursor to the next one.
func scanPages(total int, pageSizes ...int) []*interfaces.DatasetQueryResponse {
	pages := make([]*interfaces.DatasetQueryResponse, 0, len(pageSizes))
	next := 0
	for index, size := range pageSizes {
		rows := make([]map[string]any, size)
		for row := range rows {
			rows[row] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", next+row)}
		}
		next += size
		page := &interfaces.DatasetQueryResponse{Entries: rows, TotalCount: new(int64(total)),
			Paging: &interfaces.ResourceDataPagingResponse{}}
		if index < len(pageSizes)-1 {
			session := fmt.Sprintf("session-%d", index+1)
			expiresAt := time.Now().Add(time.Minute).Unix()
			page.Paging.NextCursor, page.Paging.ExpiresAtSec = &session, &expiresAt
		}
		pages = append(pages, page)
	}
	return pages
}

func scannedIDs(objects interfaces.Objects) []any {
	ids := make([]any, len(objects.Datas))
	for index, object := range objects.Datas {
		ids[index] = object["id"]
	}
	return ids
}

// Regression for #2666: a match set one object past a Vega page used to be
// cut to its first page. The scan must read every page, with no gap or repeat
// at the page boundary.
func TestScanObjectsReadsEveryPageAtBoundaries(t *testing.T) {
	cases := []struct {
		total int
		pages []int
	}{
		{total: 9999, pages: []int{9999}},
		{total: 10000, pages: []int{10000, 0}},
		{total: 10001, pages: []int{10000, 1}},
		{total: 25000, pages: []int{10000, 10000, 5000}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.total), func(t *testing.T) {
			vega := &closingScanVega{vegaStubForOTQuery: vegaStubForOTQuery{responses: scanPages(tc.total, tc.pages...)}}
			service := resourcePagingService(t, vega, len(tc.pages))
			result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), 30000)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			ids := scannedIDs(result)
			if len(ids) != tc.total || ids[0] != "customer-0" || ids[tc.total-1] != fmt.Sprintf("customer-%d", tc.total-1) {
				t.Fatalf("scanned %d objects, first %v, last %v", len(ids), ids[0], ids[len(ids)-1])
			}
			seen := make(map[any]struct{}, len(ids))
			for _, id := range ids {
				if _, repeated := seen[id]; repeated {
					t.Fatalf("object %v read twice", id)
				}
				seen[id] = struct{}{}
			}
			if (result.TotalCount == nil || *result.TotalCount != int64(tc.total)) || result.Cursor != "" || result.Paging.NextCursor != nil {
				t.Fatalf("total=%v cursor=%q paging=%#v", result.TotalCount, result.Cursor, result.Paging)
			}
			first := vega.paramsHistory[0].Paging
			if len(vega.paramsHistory) != len(tc.pages) || first.Mode != interfaces.ResourceDataPagingModeCursor ||
				first.Limit != 0 || !vega.paramsHistory[0].NeedTotal {
				t.Fatalf("requests = %d, first paging = %#v", len(vega.paramsHistory), first)
			}
			for index, params := range vega.paramsHistory[1:] {
				if params.Paging.Cursor != fmt.Sprintf("session-%d", index+1) {
					t.Fatalf("page %d continued from %#v", index+2, params.Paging)
				}
			}
			if vega.closes != 0 {
				t.Fatalf("completed scan closed %d extra cursors", vega.closes)
			}
		})
	}
}

func TestScanObjectsUsesResourcePageSizeWithoutRequestingTotal(t *testing.T) {
	pages := scanPages(5, 2, 2, 1)
	for _, page := range pages {
		page.TotalCount = nil
	}
	vega := &vegaStubForOTQuery{responses: pages}
	service := resourcePagingService(t, vega, 3)
	query := resourcePagingQuery(1)
	query.NeedTotal = false
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), query, 10)
	if err != nil || result.Incomplete || result.TotalCount != nil || len(result.Datas) != 5 {
		t.Fatalf("scan rows=%d total=%v incomplete=%t err=%v", len(result.Datas), result.TotalCount, result.Incomplete, err)
	}
	requests := vega.paramsHistory
	if len(requests) != 3 || requests[0].NeedTotal ||
		requests[0].Paging.Mode != interfaces.ResourceDataPagingModeCursor {
		t.Fatalf("unexpected Vega requests: %#v", requests)
	}
	initial, err := jsoncodec.MarshalString(requests[0])
	if err != nil || strings.Contains(initial, `"limit"`) || strings.Contains(initial, `"need_total"`) {
		t.Fatalf("unexpected initial scan JSON %s, err=%v", initial, err)
	}
	for index, request := range requests[1:] {
		body, err := jsoncodec.MarshalString(request)
		want := fmt.Sprintf(`{"paging":{"cursor":"session-%d"}}`, index+1)
		if err != nil || body != want {
			t.Fatalf("continuation JSON %s, want %s, err=%v", body, want, err)
		}
	}
}

func TestScanObjectsAcceptsFullTerminalCursorPageWithoutTotal(t *testing.T) {
	page := scanPages(10000, 10000)[0]
	page.TotalCount = nil
	vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{page}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.NeedTotal = false
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), query, 20000)
	if err != nil || result.Incomplete || len(result.Datas) != 10000 {
		t.Fatalf("terminal cursor page: rows=%d incomplete=%t error=%v", len(result.Datas), result.Incomplete, err)
	}
}

func TestScanObjectsFlagsEmptyLegacyPageWithRemainingTotal(t *testing.T) {
	page := scanPages(2, 0)[0]
	vega := &vegaStubForOTQuery{
		errors: []error{interfaces.NewVegaDownstreamError(http.StatusNotImplemented,
			`{"error_code":"VegaBackend.Query.InvalidParameter"}`)},
		responses: []*interfaces.DatasetQueryResponse{page},
	}
	service := resourcePagingService(t, vega, 1)
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), 10)
	if err != nil || !result.Incomplete || len(result.Datas) != 0 || len(vega.paramsHistory) != 2 ||
		vega.paramsHistory[1].Paging.Mode != interfaces.ResourceDataPagingModeSingle ||
		vega.paramsHistory[1].Paging.Limit != interfaces.MAX_LIMIT {
		t.Fatalf("inconsistent legacy page: rows=%d incomplete=%t requests=%d error=%v",
			len(result.Datas), result.Incomplete, len(vega.paramsHistory), err)
	}
}

// The scan stops one object past the limit: that object proves the match set
// is too large, and nothing beyond it is loaded.
func TestScanObjectsStopsOnePastTheLimit(t *testing.T) {
	vega := &closingScanVega{vegaStubForOTQuery: vegaStubForOTQuery{responses: scanPages(18989, 10000, 8000, 989)}}
	service := resourcePagingService(t, vega, 2)
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), 10000)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	ids := scannedIDs(result)
	if len(ids) != 10001 || ids[10000] != "customer-10000" || (result.TotalCount == nil || *result.TotalCount != 18989) {
		t.Fatalf("scanned %d objects, total %v", len(ids), result.TotalCount)
	}
	if len(vega.paramsHistory) != 2 {
		t.Fatalf("read %d pages, want 2", len(vega.paramsHistory))
	}
	vega.assertClosed(t, "session-2")
}

func TestScanObjectsWithNoMatches(t *testing.T) {
	vega := &vegaStubForOTQuery{responses: scanPages(0, 0)}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationEq, Value: "missing"}
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), query, 10000)
	if err != nil || len(result.Datas) != 0 || len(vega.paramsHistory) != 1 {
		t.Fatalf("scan = %d objects, %d requests, err %v", len(result.Datas), len(vega.paramsHistory), err)
	}
}

// A failed continuation must fail the scan, not return the pages read so far
// as if they were the whole match set.
func TestScanObjectsFailsWhenAContinuationFails(t *testing.T) {
	pages := scanPages(10001, 10000, 1)
	vega := &failingContinuationVega{first: pages[0],
		err: interfaces.NewVegaDownstreamError(http.StatusInternalServerError, `{"error_code":"VegaBackend.InternalError"}`)}
	service := resourcePagingService(t, vega, 2)
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), 20000)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || len(result.Datas) != 0 {
		t.Fatalf("scan = %d objects, err %v", len(result.Datas), err)
	}
	vega.assertClosed(t, "session-1")
}

type scanCursorCloser struct {
	closes     int
	resourceID string
	cursor     string
	proxy      *interfaces.TrustedProxyContext
}

func (v *scanCursorCloser) CloseResourceDataCursor(ctx context.Context, resourceID, cursor string) error {
	v.closes++
	v.resourceID = resourceID
	v.cursor = cursor
	v.proxy, _ = interfaces.TrustedProxyContextFromContext(ctx)
	return ctx.Err()
}

func (v *scanCursorCloser) assertClosed(t *testing.T, cursor string) {
	t.Helper()
	if v.closes != 1 {
		t.Fatalf("close requests = %d, want 1", v.closes)
	}
	if v.resourceID != "resource-1" || v.cursor != cursor || v.proxy == nil ||
		v.proxy.Binding.TargetID != v.resourceID || v.proxy.Proxy.ID != "test-proxy" {
		t.Fatalf("closed %q on %q with proxy %#v, want %q on resource-1 under its query proxy",
			v.cursor, v.resourceID, v.proxy, cursor)
	}
}

type closingScanVega struct {
	vegaStubForOTQuery
	scanCursorCloser
}

type failingContinuationVega struct {
	scanCursorCloser
	first *interfaces.DatasetQueryResponse
	err   error
	calls int
}

func (v *failingContinuationVega) QueryResourceData(context.Context, string,
	*interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	v.calls++
	if v.calls == 1 {
		return v.first, nil
	}
	return nil, v.err
}

func (v *failingContinuationVega) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{SchemaDefinition: []map[string]any{}}, nil
}

// An object type whose primary key has no resource field cannot be paged
// (see appendResourceSortTieBreakers). A full first page with more matches
// behind it is flagged incomplete rather than returned as the whole set.
func TestScanObjectsFlagsAnUnpageableMatchSet(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.PrimaryKeys = []string{"legacy_code"}
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Return(objectType, true, nil)
	page := scanPages(10001, 10000)[0]
	vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{page}}
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{}, cursor: testQueryCursorCodec(t, time.Now()),
	}
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), 20000)
	if err != nil || !result.Incomplete || len(result.Datas) != 10000 || (result.TotalCount == nil || *result.TotalCount != 10001) {
		t.Fatalf("unpageable scan: incomplete=%t rows=%d total=%v err=%v",
			result.Incomplete, len(result.Datas), result.TotalCount, err)
	}
}

func TestScanObjectsDoesNotTreatUnknownTotalAsComplete(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.PrimaryKeys = []string{"legacy_code"}
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Return(objectType, true, nil)
	page := scanPages(10000, 10000)[0]
	page.TotalCount = nil
	vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{page}}
	service := &objectTypeService{omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{}, propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{}, cursor: testQueryCursorCodec(t, time.Now())}
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), 20000)
	if err != nil || !result.Incomplete || result.TotalCount != nil || len(result.Datas) != 10000 {
		t.Fatalf("unknown-total scan: incomplete=%t total=%v rows=%d error=%v", result.Incomplete, result.TotalCount, len(result.Datas), err)
	}
}
