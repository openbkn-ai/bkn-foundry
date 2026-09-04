// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knresources

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeVega implements interfaces.DrivenVega and is only for testing this package.
// byID / errByID covers a single getResource/getErr, used for use cases where multiple resources are retrieved by kn_id;
// Retrieving resources is concurrent, so all record fields are locked.
type fakeVega struct {
	mu             sync.Mutex
	listReq        *interfaces.VegaListResourcesReq
	listResp       *interfaces.VegaListResourcesResp
	listErr        error
	getResource    *interfaces.VegaResource
	getErr         error
	byID           map[string]*interfaces.VegaResource
	errByID        map[string]error
	connectorType  string
	connectorErr   error
	gotResourceID  string
	gotResourceIDs []string
}

// fetchedIDs returns the sorted fetched IDs to avoid order jitter caused by concurrency.
func (f *fakeVega) fetchedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.gotResourceIDs...)
	sort.Strings(out)
	return out
}

// fakeBkn only implements the methods used in this package, and the rest remains compiled through embedded interfaces (isomorphic to the knmetrics stub).
type fakeBkn struct {
	interfaces.BknBackendAccess
	detail  *interfaces.KnowledgeNetworkDetail
	err     error
	gotKnID string
}

func (f *fakeBkn) GetKnowledgeNetworkDetail(_ context.Context, knID string) (*interfaces.KnowledgeNetworkDetail, error) {
	f.gotKnID = knID
	return f.detail, f.err
}

func (f *fakeVega) RawQuery(_ context.Context, _ *interfaces.VegaRawQueryReq) (*interfaces.VegaRawQueryResp, error) {
	return nil, errors.New("not used")
}

func (f *fakeVega) GetResourceConnectorType(_ context.Context, resourceID string) (string, error) {
	f.gotResourceID = resourceID
	return f.connectorType, f.connectorErr
}

func (f *fakeVega) ListResources(_ context.Context, req *interfaces.VegaListResourcesReq) (*interfaces.VegaListResourcesResp, error) {
	f.listReq = req
	return f.listResp, f.listErr
}

func (f *fakeVega) GetResource(_ context.Context, resourceID string) (*interfaces.VegaResource, error) {
	f.mu.Lock()
	f.gotResourceID = resourceID
	f.gotResourceIDs = append(f.gotResourceIDs, resourceID)
	byID, errByID := f.byID, f.errByID
	f.mu.Unlock()

	if err, ok := errByID[resourceID]; ok {
		return nil, err
	}
	if res, ok := byID[resourceID]; ok {
		return res, nil
	}
	if byID != nil || errByID != nil {
		return nil, errors.New("resource " + resourceID + " not found")
	}
	return f.getResource, f.getErr
}

func TestListResources_MapsAndForwardsFilters(t *testing.T) {
	fake := &fakeVega{
		listResp: &interfaces.VegaListResourcesResp{
			TotalCount: 2,
			Entries: []interfaces.VegaResource{
				{ID: "r1", Name: "orders", Category: "table", Status: "active", CatalogID: "c1"},
				{ID: "r2", Name: "events", Category: "topic", Status: "stale", CatalogID: "c1"},
			},
		},
	}
	svc := NewKnResourcesServiceWith(fake, nil)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{
		CatalogID: "c1",
		Type:      "table",
		Offset:    5,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// type(input parameter) maps to the Vega category; pagination is passed through.
	if fake.listReq.Category != "table" || fake.listReq.CatalogID != "c1" {
		t.Fatalf("filters not forwarded: %+v", fake.listReq)
	}
	if fake.listReq.Offset != 5 || fake.listReq.Limit != 10 {
		t.Fatalf("paging not forwarded: %+v", fake.listReq)
	}
	if resp.TotalCount != 2 || len(resp.Entries) != 2 {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	// vega category → output type.
	if resp.Entries[0].Type != "table" || resp.Entries[0].ResourceID != "r1" {
		t.Fatalf("entry0 mapping wrong: %+v", resp.Entries[0])
	}
	if resp.Entries[1].Type != "topic" || resp.Entries[1].Status != "stale" {
		t.Fatalf("entry1 mapping wrong: %+v", resp.Entries[1])
	}
}

func TestListResources_NilReqEmptyEntries(t *testing.T) {
	fake := &fakeVega{listResp: &interfaces.VegaListResourcesResp{TotalCount: 0, Entries: nil}}
	svc := NewKnResourcesServiceWith(fake, nil)

	resp, err := svc.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Entries == nil {
		t.Fatal("entries should be non-nil empty slice")
	}
	if len(resp.Entries) != 0 || resp.TotalCount != 0 {
		t.Fatalf("expected empty, got %+v", resp)
	}
}

func TestListResources_VegaErrorPropagates(t *testing.T) {
	fake := &fakeVega{listErr: errors.New("boom")}
	svc := NewKnResourcesServiceWith(fake, nil)
	if _, err := svc.ListResources(context.Background(), &ListResourcesReq{}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

// ot creates an object type with data_source; passing dsType/dsID empty indicates that the dimension is missing.
func ot(id, dsType, dsID string) *interfaces.ObjectType {
	o := &interfaces.ObjectType{ID: id}
	if dsType != "" || dsID != "" {
		o.DataSource = &interfaces.ResourceInfo{Type: dsType, ID: dsID}
	}
	return o
}

// Core regression of #781: When the bound table is ranked outside the paging window in the account resource pool, query by kn_id.
// They must still be returned. fakeVega's listResp is intentionally empty - fall back to the list endpoint once implemented.
// After filtering again, this use case will be suspended immediately.
func TestListResources_ByKnID_ResolvesBindingsWithoutListEndpoint(t *testing.T) {
	fake := &fakeVega{
		listResp: &interfaces.VegaListResourcesResp{}, // List endpoint returns empty pool.
		byID: map[string]*interfaces.VegaResource{
			"r1": {ID: "r1", Name: "orders", Category: "table", Status: "active", CatalogID: "c1"},
			"r2": {ID: "r2", Name: "shipments", Category: "table", Status: "active", CatalogID: "c1"},
		},
	}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("shipment", "resource", "r2"),
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: " kn1 ", Limit: 50, Offset: 0})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if bkn.gotKnID != "kn1" {
		t.Fatalf("kn_id not trimmed/forwarded: %q", bkn.gotKnID)
	}
	if fake.listReq != nil {
		t.Fatal("kn_id path must not touch the vega list endpoint")
	}
	if resp.TotalCount != 2 || len(resp.Entries) != 2 {
		t.Fatalf("expected 2 bound resources, got %+v", resp)
	}
	if resp.Entries[0].ResourceID != "r1" || resp.Entries[1].ResourceID != "r2" {
		t.Fatalf("binding order not preserved: %+v", resp.Entries)
	}
	if resp.Entries[0].Name != "orders" || resp.Entries[0].Type != "table" {
		t.Fatalf("entry mapping wrong: %+v", resp.Entries[0])
	}
}

func TestListResources_ByKnID_DedupesSharedResource(t *testing.T) {
	fake := &fakeVega{byID: map[string]*interfaces.VegaResource{
		"r1": {ID: "r1", Name: "orders", Category: "table"},
	}}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("order_line", "resource", "r1"),
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// total_count is the number of resources after deduplication, not the number of object types.
	if resp.TotalCount != 1 || len(resp.Entries) != 1 {
		t.Fatalf("expected deduped single entry, got %+v", resp)
	}
	if got := fake.fetchedIDs(); len(got) != 1 {
		t.Fatalf("shared resource must be fetched once, fetched %v", got)
	}
}

func TestListResources_ByKnID_ClassifiesUnresolvedBindings(t *testing.T) {
	fake := &fakeVega{
		byID:    map[string]*interfaces.VegaResource{"r1": {ID: "r1", Name: "orders", Category: "table"}},
		errByID: map[string]error{"gone": infraErr.DefaultHTTPError(context.Background(), http.StatusNotFound, "not found")},
	}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("forecast", "resource", ""),    // Data_source.id empty -> unbound.
			ot("legacy", "", ""),              // There is no data_source -> unbound at all.
			ot("old_view", "data_view", "v1"), // Abandoned form -> stale_binding.
			ot("deleted", "resource", "gone"), // Can't get it back -> missing.
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// A dangling binding cannot bring down the rest of the table.
	if resp.TotalCount != 1 || len(resp.Entries) != 1 || resp.Entries[0].ResourceID != "r1" {
		t.Fatalf("resolvable binding must still be returned: %+v", resp)
	}
	if len(resp.Unbound) != 2 || resp.Unbound[0].ObjectTypeID != "forecast" || resp.Unbound[1].ObjectTypeID != "legacy" {
		t.Fatalf("unbound wrong: %+v", resp.Unbound)
	}
	if len(resp.StaleBinding) != 1 || resp.StaleBinding[0].SourceType != "data_view" ||
		resp.StaleBinding[0].ResourceID != "v1" || resp.StaleBinding[0].ObjectTypeID != "old_view" {
		t.Fatalf("stale_binding wrong: %+v", resp.StaleBinding)
	}
	if len(resp.Missing) != 1 || resp.Missing[0].ObjectTypeID != "deleted" ||
		resp.Missing[0].ResourceID != "gone" || resp.Missing[0].Reason == "" {
		t.Fatalf("missing wrong: %+v", resp.Missing)
	}
	// The obsolete form must not be used to adjust the resource endpoint of vega.
	for _, id := range fake.fetchedIDs() {
		if id == "v1" {
			t.Fatal("stale data_view binding must not be fetched from vega")
		}
	}
}

// When vega is unavailable as a whole, it must not return "success + empty list" - that is what this issue wants to eliminate.
// Dumb failure: The agent cannot distinguish between "the backend is down" and "this network has no tables".
func TestListResources_ByKnID_AllFetchesFailDownstreamReturnsError(t *testing.T) {
	fake := &fakeVega{errByID: map[string]error{
		"r1": infraErr.DefaultHTTPError(context.Background(), http.StatusInternalServerError, "vega down"),
		"r2": infraErr.DefaultHTTPError(context.Background(), http.StatusInternalServerError, "vega down"),
	}}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("shipment", "resource", "r2"),
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	if _, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"}); err == nil {
		t.Fatal("expected a downstream outage to surface as an error, not an empty list")
	}
}

// On the other hand: a network only has one table tied to it, and this table happened to be deleted, which is a conclusive modeling fact.
// It should be left in missing and should not be disguised as a service failure.
func TestListResources_ByKnID_AllFetchesNotFoundStaysClassified(t *testing.T) {
	fake := &fakeVega{errByID: map[string]error{
		"gone": infraErr.DefaultHTTPError(context.Background(), http.StatusNotFound, "resource not found"),
	}}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{ot("order", "resource", "gone")},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("a deleted resource is a modelling fact, not an outage: %v", err)
	}
	if len(resp.Missing) != 1 || resp.Missing[0].ObjectTypeID != "order" || resp.TotalCount != 0 {
		t.Fatalf("expected the binding classified as missing, got %+v", resp)
	}
}

func TestListResources_ByKnID_DeniedBindingIsOmitted(t *testing.T) {
	ctx := context.Background()
	fake := &fakeVega{
		byID: map[string]*interfaces.VegaResource{
			"r1": {ID: "r1", Name: "orders", Category: "table"},
		},
		errByID: map[string]error{
			"secret": infraErr.DefaultHTTPError(ctx, http.StatusForbidden, "forbidden"),
		},
	}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("secret_order", "resource", "secret"),
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(ctx, &ListResourcesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("an explicit resource denial should be filtered, got %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ResourceID != "r1" {
		t.Fatalf("expected only the allowed resource, got %+v", resp)
	}
	if len(resp.Missing) != 0 {
		t.Fatalf("denied binding must not leak through missing diagnostics: %+v", resp.Missing)
	}
}

func TestListResources_ByKnID_AuthorizationDependencyFailureFailsWholeRequest(t *testing.T) {
	ctx := context.Background()
	downstreamErr := infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "vega unavailable")
	fake := &fakeVega{
		byID: map[string]*interfaces.VegaResource{
			"r1": {ID: "r1", Name: "orders", Category: "table"},
		},
		errByID: map[string]error{"r2": downstreamErr},
	}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("shipment", "resource", "r2"),
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	_, err := svc.ListResources(ctx, &ListResourcesReq{KnID: "kn1"})
	if !errors.Is(err, downstreamErr) {
		t.Fatalf("expected the whole aggregate to fail, got %v", err)
	}
}

func TestListResources_ByKnID_IncompleteAuthorizedResourceFailsWholeRequest(t *testing.T) {
	fake := &fakeVega{byID: map[string]*interfaces.VegaResource{"r1": nil}}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{ot("order", "resource", "r1")},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"})
	status, ok := infraErr.HTTPStatus(err)
	if resp != nil || !ok || status != http.StatusServiceUnavailable {
		t.Fatalf("resp=%+v error=%v, want complete failure with 503", resp, err)
	}
}

func TestListResources_ByKnID_AppliesTypeFilter(t *testing.T) {
	fake := &fakeVega{byID: map[string]*interfaces.VegaResource{
		"r1": {ID: "r1", Name: "orders", Category: "table"},
		"r2": {ID: "r2", Name: "events", Category: "topic"},
	}}
	bkn := &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{
		ObjectTypes: []*interfaces.ObjectType{
			ot("order", "resource", "r1"),
			ot("event", "resource", "r2"),
		},
	}}
	svc := NewKnResourcesServiceWith(fake, bkn)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1", Type: "TABLE"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.TotalCount != 1 || len(resp.Entries) != 1 || resp.Entries[0].ResourceID != "r1" {
		t.Fatalf("type filter not applied: %+v", resp)
	}
}

func TestListResources_ByKnID_DetailErrorPropagates(t *testing.T) {
	svc := NewKnResourcesServiceWith(&fakeVega{}, &fakeBkn{err: errors.New("kn not found")})
	if _, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"}); err == nil {
		t.Fatal("expected knowledge network error to propagate")
	}
}

func TestListResources_ByKnID_NoBackendConfigured(t *testing.T) {
	svc := NewKnResourcesServiceWith(&fakeVega{}, nil)
	_, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"})
	if !errors.Is(err, ErrKnBackendUnavailable) {
		t.Fatalf("expected ErrKnBackendUnavailable, got %v", err)
	}
}

func TestListResources_ByKnID_EmptyNetworkReturnsEmptyNotNil(t *testing.T) {
	svc := NewKnResourcesServiceWith(&fakeVega{}, &fakeBkn{detail: &interfaces.KnowledgeNetworkDetail{}})
	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Entries == nil {
		t.Fatal("entries should be non-nil empty slice")
	}
	if resp.TotalCount != 0 {
		t.Fatalf("expected 0, got %d", resp.TotalCount)
	}
}

func TestDescribeResource_EmptyID(t *testing.T) {
	svc := NewKnResourcesServiceWith(&fakeVega{}, nil)
	if _, err := svc.DescribeResource(context.Background(), "  "); !errors.Is(err, ErrResourceIDRequired) {
		t.Fatalf("expected ErrResourceIDRequired, got %v", err)
	}
}

func TestDescribeResource_MapsColumnsAndConnector(t *testing.T) {
	fake := &fakeVega{
		getResource: &interfaces.VegaResource{
			ID: "r1",
			SchemaDefinition: []interfaces.VegaResourceColumn{
				{Name: "id", Type: "bigint", Description: "主键"},
				{Name: "amount", Type: "decimal"},
			},
		},
		connectorType: "mysql",
	}
	svc := NewKnResourcesServiceWith(fake, nil)

	resp, err := svc.DescribeResource(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.ResourceID != "r1" || resp.ConnectorType != "mysql" {
		t.Fatalf("header mapping wrong: %+v", resp)
	}
	if len(resp.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(resp.Columns))
	}
	if resp.Columns[0].Name != "id" || resp.Columns[0].Type != "bigint" || resp.Columns[0].Description != "主键" {
		t.Fatalf("col0 wrong: %+v", resp.Columns[0])
	}
	if resp.Columns[1].Name != "amount" || resp.Columns[1].Description != "" {
		t.Fatalf("col1 wrong: %+v", resp.Columns[1])
	}
}

func TestDescribeResource_EmptySchemaIsEmptyColumns(t *testing.T) {
	fake := &fakeVega{
		getResource:   &interfaces.VegaResource{ID: "rf", SchemaDefinition: nil},
		connectorType: "postgresql",
	}
	svc := NewKnResourcesServiceWith(fake, nil)

	resp, err := svc.DescribeResource(context.Background(), "rf")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Columns == nil {
		t.Fatal("columns should be non-nil empty slice")
	}
	if len(resp.Columns) != 0 {
		t.Fatalf("expected 0 columns, got %d", len(resp.Columns))
	}
}

func TestDescribeResource_GetResourceErrorPropagates(t *testing.T) {
	fake := &fakeVega{getErr: errors.New("403 forbidden")}
	svc := NewKnResourcesServiceWith(fake, nil)
	if _, err := svc.DescribeResource(context.Background(), "r1"); err == nil {
		t.Fatal("expected error to propagate")
	}
}
