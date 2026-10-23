// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

// vegaSessionPoolStub models Vega's global cursor session pool: every initial
// cursor request that is not the final page keeps a session, and requests
// beyond the limit are rejected with 429.
type vegaSessionPoolStub struct {
	limit         int
	sessions      int
	singleReads   int
	lastSessionID string
}

func (v *vegaSessionPoolStub) QueryResourceData(_ context.Context, _ string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	entries := []map[string]any{{"customer_id": "customer-1"}}
	if params.Paging.Mode != interfaces.ResourceDataPagingModeCursor {
		v.singleReads++
		return &interfaces.DatasetQueryResponse{Entries: entries, TotalCount: 18989,
			Paging: &interfaces.ResourceDataPagingResponse{}}, nil
	}
	if v.sessions >= v.limit {
		return nil, interfaces.NewVegaDownstreamError(http.StatusTooManyRequests,
			`{"error_code":"VegaBackend.Query.CursorSessionLimitExceeded","error_details":"cursor session limit reached, please retry later"}`)
	}
	v.sessions++
	v.lastSessionID = fmt.Sprintf("session-%d", v.sessions)
	expiresAt := time.Now().Add(30 * time.Minute).Unix()
	return &interfaces.DatasetQueryResponse{Entries: entries, TotalCount: 18989,
		Paging: &interfaces.ResourceDataPagingResponse{NextCursor: &v.lastSessionID, ExpiresAtSec: &expiresAt}}, nil
}

func (v *vegaSessionPoolStub) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{SchemaDefinition: []map[string]any{}}, nil
}

func resourcePagingService(t *testing.T, vba interfaces.VegaBackendAccess, getCalls int) *objectTypeService {
	t.Helper()
	objectType := accessPlanObjectType()
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Times(getCalls).
		Return(objectType, true, nil)
	return &objectTypeService{
		omAccess: models, vba: vba, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{}, cursor: testQueryCursorCodec(t, time.Now()),
	}
}

func resourcePagingContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
}

func resourcePagingQuery(limit int) *interfaces.ObjectQueryBaseOnObjectType {
	return &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", Properties: []string{"id"},
		PageQuery: interfaces.PageQuery{Limit: limit, NeedTotal: true},
	}
}

// Regression for #1937: one-shot callers (MCP tool calls) never continue the
// returned cursor. Each first page used to open a Vega cursor session, so the
// global session limit was exhausted after about 1000 calls and every later
// request failed with 429.
func TestOneShotResourceQueriesDoNotExhaustVegaCursorSessions(t *testing.T) {
	const sessionLimit = 1000
	vega := &vegaSessionPoolStub{limit: sessionLimit}
	service := resourcePagingService(t, vega, sessionLimit+1)
	ctx := resourcePagingContext()
	for i := 0; i <= sessionLimit; i++ {
		result, err := service.GetObjectsByObjectTypeID(ctx, resourcePagingQuery(1))
		if err != nil {
			t.Fatalf("one-shot query %d failed after %d Vega sessions: %v", i+1, vega.sessions, err)
		}
		if result.Cursor == "" {
			t.Fatalf("query %d must still offer a continuation", i+1)
		}
	}
	if vega.sessions != 0 || vega.singleReads != sessionLimit+1 {
		t.Fatalf("Vega sessions = %d, single reads = %d", vega.sessions, vega.singleReads)
	}
}

func TestVegaCursorSessionLimitIsReportedAsTooManyRequests(t *testing.T) {
	vega := &vegaSessionPoolStub{limit: 0}
	service := resourcePagingService(t, vega, 2)
	ctx := resourcePagingContext()
	first, err := service.GetObjectsByObjectTypeID(ctx, resourcePagingQuery(1))
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	next := resourcePagingQuery(1)
	next.Cursor = first.Cursor
	_, err = service.GetObjectsByObjectTypeID(ctx, next)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusTooManyRequests ||
		httpErr.BaseError.ErrorCode != oerrors.OntologyQuery_ObjectType_TooManyRequests ||
		httpErr.BaseError.ErrorDetails != "cursor session limit reached, please retry later" {
		t.Fatalf("error = %#v", err)
	}
}

// Past Vega's index window the second page cannot be addressed by offset. It
// must reopen the cursor at the first page's offset and continue past it.
func TestResourceContinuationBeyondIndexWindowReplaysFromFirstPage(t *testing.T) {
	const limit = 6000
	fullPage := make([]map[string]any, limit)
	for i := range fullPage {
		fullPage[i] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", i)}
	}
	sessionID := "session-1"
	vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{
		{Entries: fullPage, TotalCount: 3 * limit, Paging: &interfaces.ResourceDataPagingResponse{}},
		{Entries: fullPage, TotalCount: 3 * limit, Paging: &interfaces.ResourceDataPagingResponse{NextCursor: &sessionID}},
		{Entries: fullPage[:1], TotalCount: 3 * limit, Paging: &interfaces.ResourceDataPagingResponse{}},
	}}
	service := resourcePagingService(t, vega, 2)
	ctx := resourcePagingContext()
	first, err := service.GetObjectsByObjectTypeID(ctx, resourcePagingQuery(limit))
	if err != nil || first.Cursor == "" {
		t.Fatalf("first page = %v, cursor %q", err, first.Cursor)
	}
	next := resourcePagingQuery(limit)
	next.Cursor = first.Cursor
	second, err := service.GetObjectsByObjectTypeID(ctx, next)
	if err != nil || len(second.Datas) != 1 || len(vega.paramsHistory) != 3 {
		t.Fatalf("second page = %d rows, %v, requests = %d", len(second.Datas), err, len(vega.paramsHistory))
	}
	replayed, continued := vega.paramsHistory[1], vega.paramsHistory[2]
	if replayed.Paging.Mode != interfaces.ResourceDataPagingModeCursor || replayed.Paging.Offset != 0 {
		t.Fatalf("replay request = %#v", replayed.Paging)
	}
	if continued.Paging.Cursor != sessionID {
		t.Fatalf("continuation request = %#v", continued.Paging)
	}
}

func TestContinuousObjectScanDoesNotReplayFirstPage(t *testing.T) {
	const firstPageSize = 10000
	firstPage := make([]map[string]any, firstPageSize)
	for i := range firstPage {
		firstPage[i] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", i)}
	}
	sessionID := "session-1"
	lastPage := make([]map[string]any, 2190)
	for i := range lastPage {
		lastPage[i] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", firstPageSize+i)}
	}
	vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{
		{Entries: firstPage, TotalCount: 12190, Paging: &interfaces.ResourceDataPagingResponse{NextCursor: &sessionID}},
		{Entries: lastPage, TotalCount: 12190,
			Paging: &interfaces.ResourceDataPagingResponse{}},
	}}
	service := resourcePagingService(t, vega, 2)
	ctx := resourcePagingContext()
	firstQuery := resourcePagingQuery(firstPageSize)
	firstQuery.NeedTotal = false
	first, err := service.GetObjectsByObjectTypeIDForScan(ctx, firstQuery)
	if err != nil || len(first.Datas) != firstPageSize || first.Cursor == "" {
		t.Fatalf("first page = %d rows, cursor %q, error %v", len(first.Datas), first.Cursor, err)
	}
	next := resourcePagingQuery(firstPageSize)
	next.NeedTotal = false
	next.Cursor = first.Cursor
	second, err := service.GetObjectsByObjectTypeIDForScan(ctx, next)
	if err != nil || len(second.Datas) != 2190 || second.Cursor != "" || second.Datas[0]["id"] != "customer-10000" {
		t.Fatalf("second page = %d rows, cursor %q, error %v", len(second.Datas), second.Cursor, err)
	}
	if len(vega.paramsHistory) != 2 ||
		vega.paramsHistory[0].Paging.Mode != interfaces.ResourceDataPagingModeCursor ||
		vega.paramsHistory[0].Paging.Offset != 0 ||
		vega.paramsHistory[1].Paging.Cursor != sessionID {
		t.Fatalf("continuous scan sent unexpected Vega requests: %#v", vega.paramsHistory)
	}
}

func TestContinuousObjectScanBoundaryRowCounts(t *testing.T) {
	for _, total := range []int{9999, 10000, 10001} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			firstSize := min(total, 10000)
			firstRows := make([]map[string]any, firstSize)
			for index := range firstRows {
				firstRows[index] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", index)}
			}
			var nextCursor *string
			responses := []*interfaces.DatasetQueryResponse{{Entries: firstRows, TotalCount: int64(total),
				Paging: &interfaces.ResourceDataPagingResponse{}}}
			if total >= 10000 {
				session := "session-1"
				nextCursor = &session
				responses[0].Paging.NextCursor = nextCursor
				lastRows := make([]map[string]any, total-10000)
				for index := range lastRows {
					lastRows[index] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", 10000+index)}
				}
				responses = append(responses, &interfaces.DatasetQueryResponse{Entries: lastRows,
					TotalCount: int64(total), Paging: &interfaces.ResourceDataPagingResponse{}})
			}
			vega := &vegaStubForOTQuery{responses: responses}
			service := resourcePagingService(t, vega, len(responses))
			query := resourcePagingQuery(10000)
			query.NeedTotal = false
			first, err := service.GetObjectsByObjectTypeIDForScan(resourcePagingContext(), query)
			if err != nil || len(first.Datas) != firstSize || (first.Cursor != "") != (nextCursor != nil) {
				t.Fatalf("first page: rows=%d cursor=%q err=%v", len(first.Datas), first.Cursor, err)
			}
			counted := len(first.Datas)
			if first.Cursor != "" {
				next := resourcePagingQuery(10000)
				next.NeedTotal = false
				next.Cursor = first.Cursor
				second, nextErr := service.GetObjectsByObjectTypeIDForScan(resourcePagingContext(), next)
				if nextErr != nil || second.Cursor != "" {
					t.Fatalf("last page: rows=%d cursor=%q err=%v", len(second.Datas), second.Cursor, nextErr)
				}
				counted += len(second.Datas)
				if len(second.Datas) > 0 && second.Datas[0]["id"] != "customer-10000" {
					t.Fatalf("continuation repeated or skipped first row: %#v", second.Datas[0])
				}
			}
			if counted != total || len(vega.paramsHistory) != len(responses) ||
				vega.paramsHistory[0].Paging.Mode != interfaces.ResourceDataPagingModeCursor {
				t.Fatalf("counted=%d requests=%#v", counted, vega.paramsHistory)
			}
		})
	}
}

func TestContinuousObjectScanFallsBackWhenCursorIsUnsupported(t *testing.T) {
	vega := &vegaStubForOTQuery{
		errors: []error{interfaces.NewVegaDownstreamError(http.StatusNotImplemented,
			`{"error_code":"VegaBackend.Query.InvalidParameter"}`)},
		responses: []*interfaces.DatasetQueryResponse{{Entries: []map[string]any{{"customer_id": "customer-1"}},
			Paging: &interfaces.ResourceDataPagingResponse{}}},
	}
	service := resourcePagingService(t, vega, 1)
	result, err := service.GetObjectsByObjectTypeIDForScan(resourcePagingContext(), resourcePagingQuery(10000))
	if err != nil || len(result.Datas) != 1 || len(vega.paramsHistory) != 2 ||
		vega.paramsHistory[0].Paging.Mode != interfaces.ResourceDataPagingModeCursor ||
		vega.paramsHistory[1].Paging.Mode != interfaces.ResourceDataPagingModeSingle {
		t.Fatalf("cursor fallback: rows=%d requests=%#v err=%v", len(result.Datas), vega.paramsHistory, err)
	}
}

func TestObjectAggregateUsesAuthorizedResourceQuery(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries:     []map[string]any{{"customer_id": "customer-1", "__value": int64(4)}},
		QuerySource: "local_index", Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(10000)
	query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationEq,
		ValueOptCfg: cond.ValueOptCfg{Value: "customer-1"}}
	result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), query, "id", "count", []string{"id"})
	if err != nil || len(result.Datas) != 1 || result.Datas[0]["id"] != "customer-1" ||
		result.Datas[0]["__value"] != int64(4) || result.QuerySource != "local_index" {
		t.Fatalf("authorized aggregate = %#v, error %v", result, err)
	}
	params := vega.lastParams
	if params == nil || params.FilterCondition == nil || params.Paging.Mode != interfaces.ResourceDataPagingModeSingle ||
		params.Aggregation["property"] != "customer_id" || len(params.GroupBy) != 1 ||
		params.GroupBy[0]["property"] != "customer_id" {
		t.Fatalf("aggregate request = %#v", params)
	}
}

func TestObjectAggregateRejectsMaskedMeasureBeforeVega(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{}}
	service := resourcePagingService(t, vega, 1)
	service.propertyAccess = propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id":     interfaces.PropertyAccessMasked,
		"mobile": interfaces.PropertyAccessMasked,
		"notes":  interfaces.PropertyAccessSchema,
		"secret": interfaces.PropertyAccessNone,
	}}
	_, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(100), "id", "count", nil)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || len(vega.paramsHistory) != 0 {
		t.Fatalf("masked aggregate error = %v, Vega requests = %d", err, len(vega.paramsHistory))
	}
}

func TestResourceContinuationBeyondIndexWindowEndsWhenDataShrank(t *testing.T) {
	const limit = 6000
	fullPage := make([]map[string]any, limit)
	for i := range fullPage {
		fullPage[i] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", i)}
	}
	vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{
		{Entries: fullPage, Paging: &interfaces.ResourceDataPagingResponse{}},
		{Entries: fullPage[:10], Paging: &interfaces.ResourceDataPagingResponse{}},
	}}
	service := resourcePagingService(t, vega, 2)
	ctx := resourcePagingContext()
	query := resourcePagingQuery(limit)
	query.NeedTotal = false
	first, err := service.GetObjectsByObjectTypeID(ctx, query)
	if err != nil || first.Cursor == "" {
		t.Fatalf("first page = %v, cursor %q", err, first.Cursor)
	}
	next := resourcePagingQuery(limit)
	next.NeedTotal = false
	next.Cursor = first.Cursor
	second, err := service.GetObjectsByObjectTypeID(ctx, next)
	if err != nil || len(second.Datas) != 0 || second.Cursor != "" || len(vega.paramsHistory) != 2 {
		t.Fatalf("second page = %#v, %v, requests = %d", second, err, len(vega.paramsHistory))
	}
}

func TestUnstableResourceSortNeverReturnsContinuation(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.PrimaryKeys = []string{"legacy_key"}
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	query := resourcePagingQuery(1)
	query.Sort = []*interfaces.SortParams{{Field: "id", Direction: "asc"}}
	plan, err := buildPropertyAccessPlan(context.Background(), fullPropertyAccessStub{}, objectType, query, true)
	if err != nil {
		t.Fatal(err)
	}
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"customer_id": "customer-1"}}, TotalCount: 5,
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := &objectTypeService{vba: vega}
	var result interfaces.Objects
	if err := service.getObjectsFromResource(context.Background(), query, objectType, &result,
		plan.fieldPropertyMap(), plan, false, nil); err != nil {
		t.Fatal(err)
	}
	if vega.lastParams.Paging.Mode != interfaces.ResourceDataPagingModeSingle || result.ResourceNextOffset != 0 {
		t.Fatalf("paging = %#v, next offset = %d", vega.lastParams.Paging, result.ResourceNextOffset)
	}
}

func TestNextResourceOffset(t *testing.T) {
	page := func(rows int, total int64) *interfaces.DatasetQueryResponse {
		return &interfaces.DatasetQueryResponse{Entries: make([]map[string]any, rows), TotalCount: total}
	}
	cases := []struct {
		name      string
		offset    int
		limit     int
		needTotal bool
		resp      *interfaces.DatasetQueryResponse
		want      int
	}{
		{"short page is final", 0, 10, false, page(9, 0), 0},
		{"full page without total may continue", 0, 10, false, page(10, 0), 10},
		{"full page below total continues", 20, 10, true, page(10, 31), 30},
		{"full page reaching total is final", 20, 10, true, page(10, 30), 0},
		{"empty page is final", 0, 10, false, page(0, 0), 0},
	}
	for _, tc := range cases {
		if got := nextResourceOffset(tc.offset, tc.limit, tc.needTotal, tc.resp); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestObjectQueryLocalIndexContract(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(fmt.Sprint(ignore), func(t *testing.T) {
			source := "local_index"
			if ignore {
				source = "source"
			}
			vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"customer_id": "customer-1"}}, TotalCount: 5,
				QuerySource: source, Paging: &interfaces.ResourceDataPagingResponse{},
			}}
			service := resourcePagingService(t, vega, 3)
			query := resourcePagingQuery(1)
			query.IgnoreLocalIndex = ignore
			first, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
			if err != nil {
				t.Fatal(err)
			}
			encoded, marshalErr := json.Marshal(first)
			if marshalErr != nil || strings.Contains(string(encoded), "search_from_index") {
				t.Fatalf("obsolete response field or invalid response: %s, %v", encoded, marshalErr)
			}
			if first.QuerySource != source || first.Cursor == "" {
				t.Fatalf("first page = %#v", first)
			}
			if vega.lastParams.IgnoreLocalIndex == nil || *vega.lastParams.IgnoreLocalIndex != ignore {
				t.Fatalf("Vega params = %#v", vega.lastParams)
			}
			next := resourcePagingQuery(1)
			next.IgnoreLocalIndex = ignore
			next.Cursor = first.Cursor
			second, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), next)
			if err != nil {
				t.Fatal(err)
			}
			if second.QuerySource != source || vega.lastParams.IgnoreLocalIndex == nil || *vega.lastParams.IgnoreLocalIndex != ignore {
				t.Fatalf("second page = %#v, params = %#v", second, vega.lastParams)
			}
			next.IgnoreLocalIndex = !ignore
			if _, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), next); err == nil {
				t.Fatal("cursor accepted a changed query channel")
			}
			if len(vega.paramsHistory) != 2 {
				t.Fatalf("unexpected Vega requests: %d", len(vega.paramsHistory))
			}
		})
	}
}
