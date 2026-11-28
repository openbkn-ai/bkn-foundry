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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
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

type vegaCursorPageStub struct {
	params        *interfaces.ResourceDataQueryParams
	failNextPage  bool
	missingPaging bool
}

type changingRowFilterStub struct {
	calls int
}

func (s *changingRowFilterStub) ResolveRowFilters(_ context.Context,
	refs []string) ([]interfaces.RowFilterDecisionEntry, error) {
	s.calls++
	digest := "sha256:stable"
	if s.calls >= 3 {
		digest = "sha256:changed"
	}
	return []interfaces.RowFilterDecisionEntry{{
		ObjectTypeRef: refs[0], Predicate: interfaces.RowFilterPredicate{Kind: "true"},
		EffectiveRowFilterDigest: digest,
	}}, nil
}

func (v *vegaCursorPageStub) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{}, nil
}

func (v *vegaCursorPageStub) QueryResourceData(_ context.Context, resourceID string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	if resourceID != "resource-1" {
		return nil, fmt.Errorf("unexpected resource %q", resourceID)
	}
	if params.Paging.Cursor == "" {
		first := *params
		v.params = &first
		if v.missingPaging {
			return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"customer_id": "customer-1"}}}, nil
		}
		cursor := "cursor-1"
		return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"customer_id": "customer-1"}},
			QuerySource: "local_index", Paging: &interfaces.ResourceDataPagingResponse{NextCursor: &cursor}}, nil
	}
	if params.Paging != (interfaces.ResourceDataPagingRequest{Cursor: "cursor-1"}) {
		return nil, fmt.Errorf("unexpected stream continuation: %#v", params)
	}
	if v.failNextPage {
		return nil, errors.New("cursor continuation failed")
	}
	return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"customer_id": "customer-2"}},
		QuerySource: "local_index", Paging: &interfaces.ResourceDataPagingResponse{}}, nil
}

func TestStreamObjectsByObjectTypeIDProjectsEveryManagedBatch(t *testing.T) {
	vega := &vegaCursorPageStub{}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.PagingMode = new(interfaces.ResourceDataPagingModeSingle)
	query.NeedTotal = false
	query.IncludeTypeInfo = true
	var batches []interfaces.Objects
	err := service.StreamObjectsByObjectTypeID(resourcePagingContext(), query, func(batch interfaces.Objects) error {
		batches = append(batches, batch)
		return nil
	})
	if err != nil || len(batches) != 2 || batches[0].Datas[0]["id"] != "customer-1" ||
		batches[1].Datas[0]["id"] != "customer-2" || batches[0].ObjectType == nil ||
		batches[0].EffectivePermissions["id"] != interfaces.PropertyAccessFull {
		t.Fatalf("managed object batches=%#v error=%v", batches, err)
	}
	if vega.params == nil || vega.params.Paging.Mode != interfaces.ResourceDataPagingModeCursor ||
		vega.params.NeedTotal || vega.params.IgnoreLocalIndex != nil ||
		len(vega.params.Sort) == 0 || vega.params.Sort[len(vega.params.Sort)-1].Field != "customer_id" {
		t.Fatalf("managed stream plan=%#v", vega.params)
	}
	body, err := jsoncodec.MarshalString(vega.params)
	if err != nil || strings.Contains(body, `"limit"`) || strings.Contains(body, `"need_total"`) {
		t.Fatalf("unexpected initial stream JSON %s, err=%v", body, err)
	}
}

func TestStreamObjectsByObjectTypeIDStopsWhenAuthorizationChanges(t *testing.T) {
	vega := &vegaCursorPageStub{}
	service := resourcePagingService(t, vega, 1)
	filters := &changingRowFilterStub{}
	service.rowFilters = filters
	query := resourcePagingQuery(1)
	consumed := 0
	err := service.StreamObjectsByObjectTypeID(resourcePagingContext(), query, func(interfaces.Objects) error {
		consumed++
		return nil
	})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusConflict || consumed != 1 || filters.calls != 3 {
		t.Fatalf("stream authorization change: error=%v consumed=%d filter_calls=%d", err, consumed, filters.calls)
	}
}

func TestStreamObjectsByObjectTypeIDRejectsFailedCursorContinuation(t *testing.T) {
	vega := &vegaCursorPageStub{failNextPage: true}
	service := resourcePagingService(t, vega, 1)
	consumed := 0
	err := service.StreamObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1),
		func(interfaces.Objects) error { consumed++; return nil })
	if err == nil || consumed != 1 {
		t.Fatalf("failed cursor continuation: error=%v consumed=%d", err, consumed)
	}
}

func TestStreamObjectsByObjectTypeIDRejectsMissingPagingEnvelope(t *testing.T) {
	vega := &vegaCursorPageStub{missingPaging: true}
	service := resourcePagingService(t, vega, 1)
	consumed := 0
	err := service.StreamObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1),
		func(interfaces.Objects) error { consumed++; return nil })
	if err == nil || consumed != 0 {
		t.Fatalf("missing cursor envelope: error=%v consumed=%d", err, consumed)
	}
}

func TestStreamObjectsByObjectTypeIDRejectsCancellationAfterFinalPage(t *testing.T) {
	vega := &vegaCursorPageStub{}
	service := resourcePagingService(t, vega, 1)
	ctx, cancel := context.WithCancel(resourcePagingContext())
	defer cancel()
	consumed := 0
	err := service.StreamObjectsByObjectTypeID(ctx, resourcePagingQuery(1), func(interfaces.Objects) error {
		consumed++
		if consumed == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || consumed != 2 {
		t.Fatalf("canceled final page: error=%v consumed=%d", err, consumed)
	}
}

func (v *vegaSessionPoolStub) QueryResourceData(_ context.Context, _ string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	entries := []map[string]any{{"customer_id": "customer-1"}}
	if params.Paging.Mode != interfaces.ResourceDataPagingModeCursor {
		v.singleReads++
		return &interfaces.DatasetQueryResponse{Entries: entries, TotalCount: new(int64(18989)),
			Paging: &interfaces.ResourceDataPagingResponse{}}, nil
	}
	if v.sessions >= v.limit {
		return nil, interfaces.NewVegaDownstreamError(http.StatusTooManyRequests,
			`{"error_code":"VegaBackend.Query.CursorSessionLimitExceeded","error_details":"cursor session limit reached, please retry later"}`)
	}
	v.sessions++
	v.lastSessionID = fmt.Sprintf("session-%d", v.sessions)
	expiresAt := time.Now().Add(30 * time.Minute).Unix()
	return &interfaces.DatasetQueryResponse{Entries: entries, TotalCount: new(int64(18989)),
		Paging: &interfaces.ResourceDataPagingResponse{NextCursor: &v.lastSessionID, ExpiresAtSec: &expiresAt}}, nil
}

func (v *vegaSessionPoolStub) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{SchemaDefinition: []map[string]any{}}, nil
}

func resourcePagingService(t *testing.T, vba interfaces.VegaBackendAccess, getCalls int) *objectTypeService {
	t.Helper()
	objectType := accessPlanObjectType()
	for i := range objectType.DataProperties {
		if objectType.DataProperties[i].Name == "notes" {
			objectType.DataProperties[i].ConditionOperations = []string{cond.OperationIn}
		}
	}
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
		if result.Cursor != "" || result.Paging == nil || result.Paging.NextCursor != nil {
			t.Fatalf("query %d returned a continuation in default single mode", i+1)
		}
	}
	if vega.sessions != 0 || vega.singleReads != sessionLimit+1 {
		t.Fatalf("Vega sessions = %d, single reads = %d", vega.sessions, vega.singleReads)
	}
}

func TestVegaCursorSessionLimitIsReportedAsTooManyRequests(t *testing.T) {
	vega := &vegaSessionPoolStub{limit: 0}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
	_, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusTooManyRequests ||
		httpErr.ErrorCode != oerrors.OntologyQuery_ObjectType_TooManyRequests ||
		httpErr.ErrorDetails != "cursor session limit reached, please retry later" {
		t.Fatalf("error = %#v", httpErr)
	}
}

func TestObjectQueryContinuesWithNativeResourceCursor(t *testing.T) {
	const nativeCursor = "vega-native-cursor"
	warnings := []string{"The API resource uses its configured order."}
	for _, tc := range []struct {
		name      string
		mappedKey bool
		total     *int64
	}{
		{name: "mapped primary key and known total", mappedKey: true, total: new(int64(4))},
		{name: "unmapped primary key and unknown total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expiry := time.Now().Add(time.Minute).Unix()
			vega := &vegaStubForOTQuery{responses: []*interfaces.DatasetQueryResponse{
				{Entries: []map[string]any{{"customer_id": "customer-2"}, {"customer_id": "customer-1"}},
					TotalCount: tc.total, IsSorted: false, Warnings: warnings,
					Paging: &interfaces.ResourceDataPagingResponse{NextCursor: new(nativeCursor), ExpiresAtSec: &expiry}},
				{Entries: []map[string]any{{"customer_id": "customer-4"}, {"customer_id": "customer-3"}},
					TotalCount: tc.total, Paging: &interfaces.ResourceDataPagingResponse{}},
			}}
			model := accessPlanObjectType()
			for i := range model.DataProperties {
				if model.DataProperties[i].Name == "notes" {
					model.DataProperties[i].MappedField.Name = "customer_note"
					model.DataProperties[i].ConditionOperations = []string{cond.OperationEq}
				}
			}
			if !tc.mappedKey {
				model.PrimaryKeys = []string{"unmapped_key"}
			}
			model.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
			models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
			models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Times(3).
				Return(model, true, nil)
			service := &objectTypeService{
				omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
				propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{}, cursor: testQueryCursorCodec(t, time.Now()),
			}
			query := func() *interfaces.ObjectQueryBaseOnObjectType {
				q := resourcePagingQuery(2)
				q.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
				q.Offset = 2
				q.NeedTotal = tc.total != nil
				q.Condition = map[string]any{"field": "notes", "operation": "==", "value": "matched"}
				q.ActualCondition = &cond.CondCfg{Name: "notes", Operation: cond.OperationEq, Value: "matched"}
				return q
			}
			ctx := resourcePagingContext()
			first, err := service.GetObjectsByObjectTypeID(ctx, query())
			if err != nil || first.Cursor == "" || len(first.Datas) != 2 ||
				first.Datas[0]["id"] != "customer-2" || first.Datas[1]["id"] != "customer-1" ||
				!reflect.DeepEqual(first.Warnings, warnings) || first.Paging == nil || first.Paging.ExpiresAtSec == nil ||
				*first.Paging.ExpiresAtSec != expiry {
				t.Fatalf("first page = %#v, error = %v", first, err)
			}
			initial := vega.lastParams
			if initial.Paging.Mode != interfaces.ResourceDataPagingModeCursor ||
				initial.Paging.Limit != 2 || initial.Paging.Offset != 2 || initial.NeedTotal != (tc.total != nil) ||
				initial.FilterCondition["field"] != "customer_note" || len(initial.Sort) == 0 {
				t.Fatalf("first Vega request = %#v", initial)
			}
			wire, err := json.Marshal(first)
			if err != nil || strings.Contains(string(wire), nativeCursor) || !strings.Contains(string(wire), `"warnings":[`) ||
				!strings.Contains(string(wire), `"is_sorted":false`) {
				t.Fatalf("first response = %s, error = %v", wire, err)
			}

			changed := query()
			changed.Cursor = first.Cursor
			changed.Condition["value"] = "different"
			changed.ActualCondition.Value = "different"
			_, err = service.GetObjectsByObjectTypeID(ctx, changed)
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
				httpErr.ErrorDetails != "query cursor is invalid or expired" ||
				len(vega.paramsHistory) != 1 {
				t.Fatalf("changed condition reached Vega or came back with the wrong error: calls = %d, error = %v", len(vega.paramsHistory), err)
			}

			next := query()
			next.Cursor = first.Cursor
			last, err := service.GetObjectsByObjectTypeID(ctx, next)
			if err != nil || last.Cursor != "" || last.Paging == nil || last.Paging.NextCursor != nil || len(last.Datas) != 2 ||
				last.Datas[0]["id"] != "customer-4" || last.Datas[1]["id"] != "customer-3" {
				t.Fatalf("last page = %#v, error = %v", last, err)
			}
			expected := &interfaces.ResourceDataQueryParams{Paging: interfaces.ResourceDataPagingRequest{Cursor: nativeCursor}}
			if len(vega.paramsHistory) != 2 || !reflect.DeepEqual(vega.lastParams, expected) {
				t.Fatalf("continuation requests = %#v, last = %#v", vega.paramsHistory, vega.lastParams)
			}
			wire, err = json.Marshal(last)
			if err != nil || strings.Contains(string(wire), `"warnings"`) || !strings.Contains(string(wire), `"is_sorted":false`) {
				t.Fatalf("last response = %s, error = %v", wire, err)
			}
		})
	}
}

func TestObjectQueryExplicitSingleNeverReturnsContinuation(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries:  []map[string]any{{"customer_id": "customer-1"}},
		Paging:   &interfaces.ResourceDataPagingResponse{NextCursor: new("unexpected-resource-cursor")},
		IsSorted: true,
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.PagingMode = new(interfaces.ResourceDataPagingModeSingle)
	query.Offset = 2

	result, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	if err != nil || len(result.Datas) != 1 || !result.IsSorted || result.Cursor != "" ||
		result.Paging == nil || result.Paging.NextCursor != nil || result.Paging.ExpiresAtSec != nil {
		t.Fatalf("single page = %#v, error = %v", result, err)
	}
	if len(vega.paramsHistory) != 1 || vega.lastParams.Paging.Mode != interfaces.ResourceDataPagingModeSingle ||
		vega.lastParams.Paging.Offset != 2 || vega.lastParams.Paging.Limit != 1 {
		t.Fatalf("single page request = %#v", vega.paramsHistory)
	}
}

func TestObjectQueryExplicitCursorTreatsFullPageWithNullCursorAsTerminal(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"customer_id": "customer-1"}},
		Paging:  &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
	query.NeedTotal = false

	result, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	if err != nil || len(result.Datas) != 1 || result.Cursor != "" ||
		result.Paging == nil || result.Paging.NextCursor != nil {
		t.Fatalf("cursor page = %#v, error = %v", result, err)
	}
	if len(vega.paramsHistory) != 1 || vega.lastParams.Paging.Mode != interfaces.ResourceDataPagingModeCursor {
		t.Fatalf("cursor page request = %#v", vega.paramsHistory)
	}
}

func TestObjectQueryRejectsInvalidPagingModeBeforeResourceLookup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   interfaces.ResourceDataPagingMode
		cursor string
	}{
		{name: "unknown", mode: "unknown"},
		{name: "empty"},
		{name: "single with continuation", mode: interfaces.ResourceDataPagingModeSingle, cursor: "opaque"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := resourcePagingQuery(1)
			query.PagingMode = &tc.mode
			query.Cursor = tc.cursor
			service := resourcePagingService(t, &vegaStubForOTQuery{}, 0)

			_, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
				t.Fatalf("paging validation error = %v", err)
			}
		})
	}
}

func TestObjectQueryRejectsContinuationWithoutPagingMode(t *testing.T) {
	service := resourcePagingService(t, &vegaStubForOTQuery{}, 0)
	query := resourcePagingQuery(1)
	query.Cursor = "opaque"
	_, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("implicit single accepted a cursor: %v", err)
	}
}

func TestObjectQueryExplicitCursorDoesNotFallBack(t *testing.T) {
	vega := &vegaStubForOTQuery{err: interfaces.NewVegaDownstreamError(http.StatusNotImplemented, "")}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.PagingMode = new(interfaces.ResourceDataPagingModeCursor)

	_, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	if err == nil || len(vega.paramsHistory) != 1 || vega.lastParams.Paging.Mode != interfaces.ResourceDataPagingModeCursor {
		t.Fatalf("cursor error = %v, requests = %#v", err, vega.paramsHistory)
	}
}

func TestObjectQueryForwardsExplicitSortAndResourceWarning(t *testing.T) {
	warnings := []string{"The API resource uses its configured order."}
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"customer_id": "customer-1"}}, Warnings: warnings,
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(2)
	query.Sort = []*interfaces.SortParams{{Field: "id", Direction: interfaces.DESC_DIRECTION}}

	result, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
	if err != nil || !reflect.DeepEqual(result.Warnings, warnings) {
		t.Fatalf("explicit sort result = %#v, error = %v", result, err)
	}
	expectedSort := []*interfaces.SortParams{{Field: "customer_id", Direction: interfaces.DESC_DIRECTION}}
	if len(vega.paramsHistory) != 1 || !reflect.DeepEqual(vega.lastParams.Sort, expectedSort) {
		t.Fatalf("explicit sort was not forwarded unchanged: %#v", vega.paramsHistory)
	}
}

func TestObjectContinuationReportsDownstreamClientErrors(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"customer_id": "customer-1"}}, TotalCount: new(int64(2)),
		Paging: &interfaces.ResourceDataPagingResponse{NextCursor: new("native-cursor")},
	}}
	service := resourcePagingService(t, vega, 4)
	ctx := resourcePagingContext()
	query := resourcePagingQuery(1)
	query.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
	first, err := service.GetObjectsByObjectTypeID(ctx, query)
	if err != nil || first.Cursor == "" {
		t.Fatalf("first page = %#v, error = %v", first, err)
	}
	vega.err = interfaces.NewVegaDownstreamError(http.StatusBadRequest,
		`{"error_code":"VegaBackend.Query.InvalidParameter","error_details":"invalid filter"}`)
	next := resourcePagingQuery(1)
	next.Cursor = first.Cursor
	next.PagingMode = new(interfaces.ResourceDataPagingModeCursor)

	_, err = service.GetObjectsByObjectTypeID(ctx, next)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || httpErr.ErrorDetails != "invalid filter" ||
		len(vega.paramsHistory) != 2 || vega.lastParams.Paging != (interfaces.ResourceDataPagingRequest{Cursor: "native-cursor"}) {
		t.Fatalf("unexpected continuation result: err = %v, requests = %#v", err, vega.paramsHistory)
	}

	vega.err = interfaces.NewVegaDownstreamError(http.StatusConflict,
		`{"error_code":"VegaBackend.Query.CursorInUse","error_details":"internal connector detail"}`)
	_, err = service.GetObjectsByObjectTypeID(ctx, next)
	httpErr = nil
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusConflict || httpErr.ErrorCode != httperrors.PublicError_Conflict ||
		httpErr.ErrorDetails != "cursor is in use, please retry later" ||
		len(httpErr.Metadata) != 0 ||
		len(vega.paramsHistory) != 3 || vega.lastParams.Paging != (interfaces.ResourceDataPagingRequest{Cursor: "native-cursor"}) {
		t.Fatalf("unexpected busy cursor result: err = %v, requests = %#v", err, vega.paramsHistory)
	}

	vega.err = interfaces.NewVegaDownstreamError(http.StatusNotFound, "")
	_, err = service.GetObjectsByObjectTypeID(ctx, next)
	httpErr = nil
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusNotFound ||
		httpErr.ErrorDetails != "Resource or cursor is unavailable. Restart the query without a cursor." ||
		len(vega.paramsHistory) != 4 || vega.lastParams.Paging != (interfaces.ResourceDataPagingRequest{Cursor: "native-cursor"}) {
		t.Fatalf("unexpected unavailable cursor result: err = %v, requests = %#v", err, vega.paramsHistory)
	}
}

func TestObjectCursorDoesNotReplayFirstPage(t *testing.T) {
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
		{Entries: firstPage, TotalCount: new(int64(12190)), Paging: &interfaces.ResourceDataPagingResponse{NextCursor: &sessionID}},
		{Entries: lastPage, TotalCount: new(int64(12190)),
			Paging: &interfaces.ResourceDataPagingResponse{}},
	}}
	service := resourcePagingService(t, vega, 2)
	ctx := resourcePagingContext()
	firstQuery := resourcePagingQuery(firstPageSize)
	firstQuery.NeedTotal = false
	firstQuery.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
	first, err := service.GetObjectsByObjectTypeID(ctx, firstQuery)
	if err != nil || len(first.Datas) != firstPageSize || first.Cursor == "" {
		t.Fatalf("first page = %d rows, cursor %q, error %v", len(first.Datas), first.Cursor, err)
	}
	next := resourcePagingQuery(firstPageSize)
	next.NeedTotal = false
	next.Cursor = first.Cursor
	next.PagingMode = firstQuery.PagingMode
	second, err := service.GetObjectsByObjectTypeID(ctx, next)
	if err != nil || len(second.Datas) != 2190 || second.Cursor != "" || second.Datas[0]["id"] != "customer-10000" {
		t.Fatalf("second page = %d rows, cursor %q, error %v", len(second.Datas), second.Cursor, err)
	}
	if len(vega.paramsHistory) != 2 ||
		vega.paramsHistory[0].Paging.Mode != interfaces.ResourceDataPagingModeCursor ||
		vega.paramsHistory[0].Paging.Offset != 0 || vega.paramsHistory[0].Paging.Limit != firstPageSize ||
		vega.paramsHistory[1].Paging.Cursor != sessionID {
		t.Fatalf("cursor query sent unexpected Vega requests: %#v", vega.paramsHistory)
	}
}

func TestObjectCursorBoundaryRowCounts(t *testing.T) {
	for _, total := range []int{9999, 10000, 10001} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			firstSize := min(total, 10000)
			firstRows := make([]map[string]any, firstSize)
			for index := range firstRows {
				firstRows[index] = map[string]any{"customer_id": fmt.Sprintf("customer-%d", index)}
			}
			var nextCursor *string
			responses := []*interfaces.DatasetQueryResponse{{Entries: firstRows, TotalCount: new(int64(total)),
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
					TotalCount: new(int64(total)), Paging: &interfaces.ResourceDataPagingResponse{}})
			}
			vega := &vegaStubForOTQuery{responses: responses}
			service := resourcePagingService(t, vega, len(responses))
			query := resourcePagingQuery(10000)
			query.NeedTotal = false
			query.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
			first, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), query)
			if err != nil || len(first.Datas) != firstSize || (first.Cursor != "") != (nextCursor != nil) {
				t.Fatalf("first page: rows=%d cursor=%q err=%v", len(first.Datas), first.Cursor, err)
			}
			counted := len(first.Datas)
			if first.Cursor != "" {
				next := resourcePagingQuery(10000)
				next.NeedTotal = false
				next.Cursor = first.Cursor
				next.PagingMode = query.PagingMode
				second, nextErr := service.GetObjectsByObjectTypeID(resourcePagingContext(), next)
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
	result, err := service.ScanObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(10000), 10000)
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
		Value: "customer-1"}
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

func TestObjectCountRowsUsesAuthorizedVegaTotal(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"customer_id": "customer-1"}}, TotalCount: new(int64(100001)), QuerySource: "local_index",
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(10000)
	query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationEq,
		Value: "customer-1"}
	result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), query, "", "count_rows", nil)
	if err != nil || len(result.Datas) != 1 || result.Datas[0]["__value"] != json.Number("100001") ||
		result.QuerySource != "local_index" {
		t.Fatalf("authorized row count = %#v, error %v", result, err)
	}
	params := vega.lastParams
	if params == nil || params.FilterCondition == nil || params.Paging.Mode != interfaces.ResourceDataPagingModeSingle ||
		params.Paging.Limit != 1 || !params.NeedTotal || params.Aggregation != nil ||
		params.IgnoreLocalIndex != nil || len(params.GroupBy) != 0 {
		t.Fatalf("count request = %#v", params)
	}
}

func TestObjectGroupedCountRowsUsesAuthorizedVegaAggregate(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{
			{"phone": "north", "notes": "priority", "__value": json.Number("100001")},
			{"phone": nil, "notes": "standard", "__value": json.Number("2")},
		},
		// Index-backed aggregates may report matched documents rather than
		// result groups as total_count. The object aggregate normalizes this.
		TotalCount: new(int64(100003)), QuerySource: "local_index", Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationEq,
		Value: "customer-1"}
	result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), query, "", "count_rows",
		[]string{"mobile", "notes"})
	if err != nil || len(result.Datas) != 2 || result.Datas[0]["mobile"] != "north" ||
		result.Datas[0]["notes"] != "priority" || result.Datas[0]["__value"] != json.Number("100001") ||
		result.Datas[1]["mobile"] != nil || result.Datas[1]["notes"] != "standard" ||
		result.Datas[1]["__value"] != json.Number("2") || (result.TotalCount == nil || *result.TotalCount != 2) || result.QuerySource != "local_index" {
		t.Fatalf("authorized grouped row count = %#v, error %v", result, err)
	}
	params := vega.lastParams
	if params == nil || params.FilterCondition == nil || params.Paging.Mode != interfaces.ResourceDataPagingModeSingle ||
		params.Paging.Limit != interfaces.MAX_LIMIT || !params.NeedTotal ||
		params.Aggregation["property"] != "*" || params.Aggregation["aggr"] != "count" ||
		params.Aggregation["alias"] != "__value" || len(params.GroupBy) != 2 ||
		params.GroupBy[0]["property"] != "phone" || params.GroupBy[1]["property"] != "notes" ||
		params.IgnoreLocalIndex != nil {
		t.Fatalf("grouped count request = %#v", params)
	}
}

func TestObjectGroupedCountRowsRejectsIncompleteAggregate(t *testing.T) {
	entries := make([]map[string]any, interfaces.MAX_LIMIT)
	for index := range entries {
		entries[index] = map[string]any{"phone": fmt.Sprintf("region-%d", index), "__value": json.Number("1")}
	}
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: entries, TotalCount: new(int64(len(entries))), QuerySource: "source",
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1),
		"", "count_rows", []string{"mobile"})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || len(result.Datas) != 0 ||
		len(vega.paramsHistory) != 1 {
		t.Fatalf("incomplete grouped count result = %#v, error = %v", result, err)
	}
}

func TestObjectGroupedCountRowsRequiresFullDimensionAccess(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{}}
	service := resourcePagingService(t, vega, 1)
	service.propertyAccess = propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id":     interfaces.PropertyAccessFull,
		"mobile": interfaces.PropertyAccessMasked,
		"notes":  interfaces.PropertyAccessSchema,
		"secret": interfaces.PropertyAccessNone,
	}}
	_, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1),
		"", "count_rows", []string{"mobile"})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || len(vega.paramsHistory) != 0 {
		t.Fatalf("masked grouped count error = %v, Vega requests = %d", err, len(vega.paramsHistory))
	}
}

func TestObjectMetricReadsTreatDeniedRowsAsCompleteEmptyResult(t *testing.T) {
	for _, tc := range []struct {
		name         string
		read         func(*objectTypeService, *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error)
		wantCountRow bool
	}{
		{name: "row count", read: func(service *objectTypeService, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
			return service.AggregateObjectsByObjectTypeID(resourcePagingContext(), query, "", "count_rows", nil)
		}, wantCountRow: true},
		{name: "grouped row count", read: func(service *objectTypeService, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
			return service.AggregateObjectsByObjectTypeID(resourcePagingContext(), query, "", "count_rows", []string{"mobile"})
		}},
		{name: "property count", read: func(service *objectTypeService, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
			return service.AggregateObjectsByObjectTypeID(resourcePagingContext(), query, "id", "count_present", nil)
		}},
		{name: "continuous scan", read: func(service *objectTypeService, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
			return service.ScanObjectsByObjectTypeID(resourcePagingContext(), query, query.Limit)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{}}
			service := resourcePagingService(t, vega, 1)
			service.rowFilters = predicateRowFilterStub{predicate: interfaces.RowFilterPredicate{Kind: "false"}}
			query := resourcePagingQuery(10000)
			query.IgnoreLocalIndex = true
			result, err := tc.read(service, query)
			wantRows := 0
			if tc.wantCountRow {
				wantRows = 1
			}
			if err != nil || result.QuerySource != "" || !result.RowFilterApplied ||
				result.Cursor != "" || len(vega.paramsHistory) != 0 || len(result.Datas) != wantRows {
				t.Fatalf("authorized empty result = %#v, Vega reads = %d, error = %v", result, len(vega.paramsHistory), err)
			}
			if tc.wantCountRow && result.Datas[0]["__value"] != json.Number("0") {
				t.Fatalf("empty row count = %#v", result.Datas)
			}
		})
	}
}

func TestObjectCountRowsDoesNotRequireMappedPrimaryProjection(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
	for index := range objectType.DataProperties {
		if objectType.DataProperties[index].Name == "id" {
			objectType.DataProperties[index].MappedField.Name = ""
		}
	}
	models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", "main", "customer").Return(objectType, true, nil)
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{TotalCount: new(int64(0))}}
	service := &objectTypeService{
		omAccess: models, vba: vega, proxy: &objectTypeProxyResolverStub{},
		propertyAccess: fullPropertyAccessStub{}, rowFilters: trueRowFilterStub{}, cursor: testQueryCursorCodec(t, time.Now()),
	}
	_, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(100), "", "count_rows", nil)
	if err != nil || len(vega.paramsHistory) != 1 || vega.lastParams.IgnoreLocalIndex != nil {
		t.Fatalf("row count error = %v, Vega requests = %d, params = %#v", err, len(vega.paramsHistory), vega.lastParams)
	}
}

func TestObjectCountPropertyKeepsEmptySetDistinctFromNullValues(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"__value": json.Number("0")}}, QuerySource: "source",
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	result, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1),
		"id", "count_present", nil)
	if err != nil || len(result.Datas) != 1 || result.Datas[0]["__value"] != json.Number("0") {
		t.Fatalf("property count = %#v, error %v", result, err)
	}
	params := vega.lastParams
	if params == nil || params.Aggregation["property"] != "customer_id" ||
		params.Aggregation["aggr"] != "count" || params.Having["field"] != "count(*)" ||
		params.Having["operation"] != ">" || params.Having["value"] != 0 {
		t.Fatalf("property count request = %#v", params)
	}
}

func TestObjectCountPropertyRequiresPropertyAccess(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{}}
	service := resourcePagingService(t, vega, 1)
	service.propertyAccess = propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessMasked,
	}}
	_, err := service.AggregateObjectsByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), "id", "count_present", nil)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || len(vega.paramsHistory) != 0 {
		t.Fatalf("incomplete primary projection error = %v, Vega requests = %d", err, len(vega.paramsHistory))
	}
}

func TestObjectPropertyAggregateRetainsAuthorization(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"__value": "100001.25"}}, QuerySource: "source",
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationEq,
		Value: "customer-1"}
	result, err := service.AggregatePropertyByObjectTypeID(resourcePagingContext(), query, "id", "sum")
	if err != nil || len(result.Datas) != 1 || result.Datas[0]["__value"] != "100001.25" {
		t.Fatalf("property aggregate = %#v, error %v", result, err)
	}
	params := vega.lastParams
	if params == nil || params.FilterCondition == nil || params.IgnoreLocalIndex != nil ||
		params.Aggregation["property"] != "customer_id" || params.Aggregation["aggr"] != "sum" ||
		params.Having["field"] != "count(*)" || params.Having["operation"] != ">" {
		t.Fatalf("property aggregate request = %#v", params)
	}
}

func TestObjectPropertyAggregateAccuracy(t *testing.T) {
	for _, allow := range []*bool{nil, new(true), new(false)} {
		actual := allow == nil || *allow
		vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"__value": int64(3)}}, IsApproximate: &actual}}
		service := resourcePagingService(t, vega, 1)
		query := resourcePagingQuery(1)
		query.AggregateAllowApproximate = allow
		result, err := service.AggregatePropertyByObjectTypeID(resourcePagingContext(), query, "id", "count_distinct")
		if err != nil || result.IsApproximate != actual {
			t.Fatalf("accuracy was lost: result=%#v err=%v", result, err)
		}
		forwarded, exists := vega.lastParams.Aggregation["allow_approximate"]
		if allow == nil && exists || allow != nil && (!exists || forwarded != *allow) {
			t.Fatalf("allowance was lost: %#v", vega.lastParams.Aggregation)
		}
	}
	for _, tc := range []struct {
		name  string
		allow *bool
	}{
		{name: "old Vega accepts default approximation"},
		{name: "old Vega accepts explicit approximation", allow: new(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"__value": int64(3)}}}}
			service := resourcePagingService(t, vega, 1)
			query := resourcePagingQuery(1)
			query.AggregateAllowApproximate = tc.allow
			result, err := service.AggregatePropertyByObjectTypeID(resourcePagingContext(), query, "id", "count_distinct")
			if err != nil || !result.IsApproximate || len(result.Datas) != 1 || result.Datas[0]["__value"] != int64(3) {
				t.Fatalf("old Vega approximate response was not preserved: result=%#v err=%v", result, err)
			}
		})
	}
	t.Run("old Vega cannot silently satisfy an exact request", func(t *testing.T) {
		vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"__value": int64(3)}}}}
		service := resourcePagingService(t, vega, 1)
		query := resourcePagingQuery(1)
		query.AggregateAllowApproximate = new(false)
		_, err := service.AggregatePropertyByObjectTypeID(resourcePagingContext(), query, "id", "count_distinct")
		var httpErr *httperrors.HTTPError
		if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadGateway {
			t.Fatalf("missing accuracy evidence accepted: %v", err)
		}
	})
}

func TestObjectPropertyAggregateRequiresPropertyAccess(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{}}
	service := resourcePagingService(t, vega, 1)
	service.propertyAccess = propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessMasked,
	}}
	_, err := service.AggregatePropertyByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1), "id", "sum")
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || len(vega.paramsHistory) != 0 {
		t.Fatalf("incomplete primary projection error = %v, Vega requests = %d", err, len(vega.paramsHistory))
	}
}

func TestObjectMetricAggregateMapsGroupedPlanAndHaving(t *testing.T) {
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"phone": "east", "__value": "42.5"}},
		Paging:  &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationEq, Value: "customer-1"}
	result, err := service.AggregateObjectMetricByObjectTypeID(resourcePagingContext(), query,
		interfaces.ObjectAggregateQuery{MeasureProperty: "id", Operation: "sum", GroupBy: []string{"mobile"},
			Having: &interfaces.ObjectAggregateHaving{Operation: cond.OperationGt, Value: 10}})
	if err != nil || len(result.Datas) != 1 || result.Datas[0]["mobile"] != "east" ||
		result.Datas[0]["__value"] != "42.5" || (result.TotalCount == nil || *result.TotalCount != 1) {
		t.Fatalf("metric aggregate = %#v, error %v", result, err)
	}
	params := vega.lastParams
	if params == nil || params.FilterCondition == nil || params.Paging.Limit != interfaces.MAX_LIMIT ||
		params.Aggregation["property"] != "customer_id" || params.Aggregation["aggr"] != "sum" ||
		len(params.GroupBy) != 1 || params.GroupBy[0]["property"] != "phone" ||
		params.Having["field"] != "__value" || params.Having["operation"] != cond.OperationGt ||
		params.Having["value"] != 10 {
		t.Fatalf("metric aggregate request = %#v", params)
	}
}

func TestObjectMetricGroupedAggregateRejectsIncompletePage(t *testing.T) {
	entries := make([]map[string]any, interfaces.MAX_LIMIT)
	for index := range entries {
		entries[index] = map[string]any{"phone": fmt.Sprintf("region-%d", index), "__value": json.Number("1")}
	}
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: entries, Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := resourcePagingService(t, vega, 1)
	_, err := service.AggregateObjectMetricByObjectTypeID(resourcePagingContext(), resourcePagingQuery(1),
		interfaces.ObjectAggregateQuery{MeasureProperty: "id", Operation: "count_distinct", GroupBy: []string{"mobile"}})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || len(vega.paramsHistory) != 1 {
		t.Fatalf("incomplete metric aggregate error = %v, Vega requests = %d", err, len(vega.paramsHistory))
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
		Entries: []map[string]any{{"customer_id": "customer-1"}}, TotalCount: new(int64(5)),
		Paging: &interfaces.ResourceDataPagingResponse{},
	}}
	service := &objectTypeService{vba: vega}
	var result interfaces.Objects
	if err := service.getObjectsFromResource(context.Background(), query, objectType, &result,
		plan.fieldPropertyMap(), plan, objectReadPage, nil, nil); err != nil {
		t.Fatal(err)
	}
	if vega.lastParams.Paging.Mode != interfaces.ResourceDataPagingModeSingle || result.ResourceCursor != "" {
		t.Fatalf("paging = %#v, resource cursor = %q", vega.lastParams.Paging, result.ResourceCursor)
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
				Entries: []map[string]any{{"customer_id": "customer-1"}}, TotalCount: new(int64(5)),
				QuerySource: source, Paging: &interfaces.ResourceDataPagingResponse{NextCursor: new("native-cursor")},
			}}
			service := resourcePagingService(t, vega, 3)
			query := resourcePagingQuery(1)
			query.IgnoreLocalIndex = ignore
			query.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
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
			next.PagingMode = new(interfaces.ResourceDataPagingModeCursor)
			second, err := service.GetObjectsByObjectTypeID(resourcePagingContext(), next)
			if err != nil {
				t.Fatal(err)
			}
			if second.QuerySource != source || vega.lastParams.Paging != (interfaces.ResourceDataPagingRequest{Cursor: "native-cursor"}) || vega.lastParams.IgnoreLocalIndex != nil {
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

func TestStreamObjectsByObjectTypeIDPreservesSourcePreference(t *testing.T) {
	vega := &vegaCursorPageStub{}
	service := resourcePagingService(t, vega, 1)
	query := resourcePagingQuery(1)
	query.IgnoreLocalIndex = true
	batches := 0
	err := service.StreamObjectsByObjectTypeID(resourcePagingContext(), query, func(interfaces.Objects) error { batches++; return nil })
	if err != nil || batches != 2 || vega.params == nil || vega.params.IgnoreLocalIndex == nil || !*vega.params.IgnoreLocalIndex || !vega.params.NeedTotal {
		t.Fatalf("source preference lost: params=%#v batches=%d error=%v", vega.params, batches, err)
	}
}
