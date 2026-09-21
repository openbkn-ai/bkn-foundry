// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/locale"
)

// spanRecorder keeps the attributes set on any span started while it is the
// global tracer provider, which is enough to read what the query span says.
type spanRecorder struct {
	attrs map[string]string
}

type recordingSpan struct {
	noop.Span
	recorder *spanRecorder
}

func (s recordingSpan) SetAttributes(attributes ...attribute.KeyValue) {
	for _, kv := range attributes {
		s.recorder.attrs[string(kv.Key)] = kv.Value.Emit()
	}
}

type recordingTracer struct {
	noop.Tracer
	recorder *spanRecorder
}

func (t recordingTracer) Start(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	span := recordingSpan{recorder: t.recorder}
	return trace.ContextWithSpan(ctx, span), span
}

type recordingTracerProvider struct {
	noop.TracerProvider
	recorder *spanRecorder
}

func (p recordingTracerProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	return recordingTracer{recorder: p.recorder}
}

func recordSpans(t *testing.T) *spanRecorder {
	t.Helper()
	recorder := &spanRecorder{attrs: map[string]string{}}
	otel.SetTracerProvider(recordingTracerProvider{recorder: recorder})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	return recorder
}

var registerLocaleOnce sync.Once

// withLocale loads the message catalog, so a refusal's details read as they
// would in production rather than as a bare message id.
func withLocale(t *testing.T) {
	t.Helper()
	registerLocaleOnce.Do(locale.Register)
}

// recordingVega records which of the two ways a statement was sent to the raw
// query route: under the caller's own identity (request) or under an account
// the service named (proxyRequest, proxyAccount).
type recordingVega struct {
	interfaces.VegaBackendAccess
	request  *interfaces.RawQueryRequest
	response *interfaces.RawQueryResponse
	err      error

	proxyAccount *interfaces.AccountInfo
	proxyRequest *interfaces.RawQueryRequest
	proxyErr     error
}

func (v *recordingVega) RawQuery(_ context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.request = req
	if v.err != nil {
		return nil, v.err
	}
	if v.response != nil {
		return v.response, nil
	}
	return &interfaces.RawQueryResponse{}, nil
}

func (v *recordingVega) RawQueryAs(_ context.Context, account interfaces.AccountInfo,
	req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.proxyAccount, v.proxyRequest = &account, req
	if v.proxyErr != nil {
		return nil, v.proxyErr
	}
	if v.response != nil {
		return v.response, nil
	}
	return &interfaces.RawQueryResponse{}, nil
}

// stubProxies answers the proxy mapping lookup the way the knowledge network
// service would once it has checked the bindings against the published model:
// every binding comes back except those whose child is listed in unpublished.
type stubProxies struct {
	mapping     *interfaces.KNProxyAccount
	err         error
	unpublished map[string]bool
	knID        string
	bindings    []interfaces.KNProxyBinding
}

func (p *stubProxies) ResolveKNProxyBindings(_ context.Context, knID string,
	bindings []interfaces.KNProxyBinding) (*interfaces.KNProxyAccount, []interfaces.KNProxyBinding, error) {
	p.knID, p.bindings = knID, bindings
	if p.err != nil {
		return nil, nil, p.err
	}
	resolved := make([]interfaces.KNProxyBinding, 0, len(bindings))
	for _, binding := range bindings {
		if !p.unpublished[binding.ChildID] {
			resolved = append(resolved, binding)
		}
	}
	return p.mapping, resolved, nil
}

func readyProxy() *interfaces.KNProxyAccount {
	return &interfaces.KNProxyAccount{
		KNID:                  "kn_1",
		ProxyAccountID:        "proxy-1",
		ProxyAccountType:      interfaces.KNProxyAccountTypeApp,
		LifecycleStatus:       interfaces.KNProxyLifecycleActive,
		Version:               4,
		SyncStatus:            interfaces.KNProxySyncReady,
		PublishedModelVersion: "sha256:model",
		SyncedModelVersion:    "sha256:model",
	}
}

// callerContext is a request from an ordinary user, which is what the handler
// hands the service.
func callerContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER})
}

// stubPermission answers the child-resource filter the way bkn-safe would.
// denied lists knowledge-network child ids (object types, relation types) the
// caller may not query; everything else passes. levels sets the effective
// level of a property, written as "<object type ref>.<property>"; every other
// property is full.
type stubPermission struct {
	interfaces.PermissionService
	filtered   []string
	operations []string
	denied     map[string]bool
	err        error

	levels        map[string]string
	propertyErr   error
	propertyCalls map[string]int
	propertyAsked map[string][]string

	rowFilters     map[string]interfaces.RowFilterPredicate
	rowFilterErr   error
	rowFilterCalls [][]string
}

func (p *stubPermission) ResolvePropertyAccessLevels(_ context.Context, objectTypeRef string,
	properties []string) (map[string]string, error) {
	if p.propertyCalls == nil {
		p.propertyCalls, p.propertyAsked = map[string]int{}, map[string][]string{}
	}
	p.propertyCalls[objectTypeRef]++
	p.propertyAsked[objectTypeRef] = append([]string(nil), properties...)
	if p.propertyErr != nil {
		return nil, p.propertyErr
	}
	levels := make(map[string]string, len(properties))
	for _, property := range properties {
		level, ok := p.levels[objectTypeRef+"."+property]
		if !ok {
			level = interfaces.PROPERTY_ACCESS_FULL
		}
		levels[property] = level
	}
	return levels, nil
}

func (p *stubPermission) ResolveRowFilters(_ context.Context,
	objectTypeRefs []string) ([]interfaces.RowFilterDecisionEntry, error) {
	p.rowFilterCalls = append(p.rowFilterCalls, append([]string(nil), objectTypeRefs...))
	if p.rowFilterErr != nil {
		return nil, p.rowFilterErr
	}
	entries := make([]interfaces.RowFilterDecisionEntry, 0, len(objectTypeRefs))
	for _, ref := range objectTypeRefs {
		predicate := interfaces.RowFilterPredicate{Kind: "true"}
		if configured, ok := p.rowFilters[ref]; ok {
			predicate = configured
		}
		entries = append(entries, interfaces.RowFilterDecisionEntry{
			ObjectTypeRef:            ref,
			Predicate:                predicate,
			EffectiveRowFilterDigest: "test-digest",
		})
	}
	return entries, nil
}

func (p *stubPermission) FilterVisibleResources(_ context.Context, _ string, ids []string,
	visibilityOperations []string) (map[string]interfaces.PermissionResourceOps, error) {

	p.filtered = append(p.filtered, ids...)
	p.operations = visibilityOperations
	if p.err != nil {
		return nil, p.err
	}
	matched := map[string]interfaces.PermissionResourceOps{}
	for _, id := range ids {
		if p.denied[id] {
			continue
		}
		matched[id] = interfaces.PermissionResourceOps{ResourceID: id}
	}
	return matched, nil
}

func testService(t *testing.T, vega *recordingVega, permission *stubPermission) *cypherQueryService {
	t.Helper()

	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"),
		dataProperty("amount", "f_total"),
	)
	return &cypherQueryService{
		ps:     permission,
		schema: &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{order}},
		vba:    vega,
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T (%v), want *rest.HTTPError", err, err)
	}
	return httpErr.HTTPCode
}

func TestQueryRunsCompiledStatement(t *testing.T) {
	vega := &recordingVega{response: &interfaces.RawQueryResponse{
		Columns: []interfaces.RawQueryColumn{{Name: "id", Type: "string"}},
		Entries: []map[string]any{{"id": "1"}},
	}}
	service := testService(t, vega, &stubPermission{})

	result, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID:   "kn_1",
		Branch: "main",
		Query:  "MATCH (o:Order) RETURN o.id AS id LIMIT 5",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entries) != 1 || len(result.Columns) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(string(result.TraceDescriptor), `"producer_profile":"openbkn.bkn-backend.run_cypher@0.1.5"`) ||
		!strings.Contains(string(result.TraceDescriptor), `"object_ref":"object:kn_1:ot_order"`) {
		t.Fatalf("trace descriptor = %s", result.TraceDescriptor)
	}

	if want := "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 LIMIT 5"; vega.request.Query != want {
		t.Fatalf("statement = %s, want %s", vega.request.Query, want)
	}
	if vega.request.InputDialect != interfaces.VEGA_DIALECT_MYSQL {
		t.Fatalf("dialect = %s", vega.request.InputDialect)
	}
	// vega-backend pages the statement it is handed, defaulting to 20 rows, so
	// the page has to be asked for explicitly or the result comes back cut
	// down with nothing to say so.
	if vega.request.Paging.Limit != 5 || vega.request.Paging.Mode != interfaces.VEGA_PAGING_MODE_SINGLE {
		t.Fatalf("paging = %+v, want a single page of 5", vega.request.Paging)
	}
}

func TestQueryAppliesRowFiltersToEveryMatchedNode(t *testing.T) {
	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"), dataProperty("region", "f_region"), dataProperty("amount", "f_amount"))
	customer := objectType("ot_customer", "Customer", resource("res_customer", "customers"),
		dataProperty("id", "f_id"), dataProperty("region", "f_region"))
	relation := relationType("rt_belongs_to", "BELONGS_TO")
	relation.SourceObjectTypeID = order.OTID
	relation.TargetObjectTypeID = customer.OTID
	relation.MappingRules = []interfaces.Mapping{{
		SourceProp: interfaces.SimpleProperty{Name: "id"},
		TargetProp: interfaces.SimpleProperty{Name: "id"},
	}}

	east, cn := "east", "cn"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order": {
			Kind: "in", Property: "region",
			Values: []interfaces.RowFilterValue{{Type: "string", String: &east}},
		},
		"kn_1/ot_customer": {
			Kind: "in", Property: "region",
			Values: []interfaces.RowFilterValue{{Type: "string", String: &cn}},
		},
	}}
	vega := &recordingVega{}
	service := &cypherQueryService{
		ps: permission,
		schema: &fakeSchemaSource{
			objectTypes:   []*interfaces.ObjectType{order, customer},
			relationTypes: []*interfaces.RelationType{relation},
		},
		vba: vega,
	}

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query: "MATCH (o:Order)-[:BELONGS_TO]->(c:Customer) WHERE o.amount > 10 RETURN count(*) AS total",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got, want := len(permission.rowFilterCalls), 1; got != want {
		t.Fatalf("row-filter calls = %d, want %d", got, want)
	}
	if got, want := permission.rowFilterCalls[0], []string{"kn_1/ot_order", "kn_1/ot_customer"}; !slices.Equal(got, want) {
		t.Fatalf("row-filter refs = %v, want %v", got, want)
	}
	if strings.Contains(string(result.TraceDescriptor), "region") {
		t.Fatalf("trace descriptor leaked row-filter-only property: %s", result.TraceDescriptor)
	}
	want := "SELECT COUNT(*) AS `total` FROM {{.res_order}} t0 JOIN {{.res_customer}} t1 ON t0.`f_id` = t1.`f_id` " +
		"WHERE t0.`f_amount` > 10 AND t0.`f_region` IN ('east') AND t1.`f_region` IN ('cn') LIMIT 1000"
	if got := vega.request.Query; got != want {
		t.Fatalf("statement = %s\nwant      = %s", got, want)
	}
}

func TestQueryRowFilterFalseIsPushedIntoSQL(t *testing.T) {
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order": {Kind: "false"},
	}}
	vega := &recordingVega{}
	service := testService(t, vega, permission)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main", Query: "MATCH (o:Order) RETURN count(*) AS total",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got, want := vega.request.Query, "SELECT COUNT(*) AS `total` FROM {{.res_order}} t0 WHERE 1 = 0 LIMIT 1000"; got != want {
		t.Fatalf("statement = %s, want %s", got, want)
	}
}

func TestQueryRejectsInvalidRowFilterResult(t *testing.T) {
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order": {Kind: "in", Property: "missing", Values: []interfaces.RowFilterValue{{Type: "string", String: stringPtr("east")}}},
	}}
	service := testService(t, &recordingVega{}, permission)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main", Query: "MATCH (o:Order) RETURN o.id",
	})
	if status := statusOf(t, err); status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
}

func stringPtr(value string) *string { return &value }

func TestQueryAppliesDefaultLimit(t *testing.T) {
	vega := &recordingVega{}
	service := testService(t, vega, &stubPermission{})

	if _, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id",
	}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.HasSuffix(vega.request.Query, " LIMIT 1000") {
		t.Fatalf("statement = %s, want a default limit", vega.request.Query)
	}
	if vega.request.Paging.Limit != interfaces.CYPHER_DEFAULT_LIMIT {
		t.Fatalf("paging limit = %d", vega.request.Paging.Limit)
	}
}

// Object types are authorized individually, for query_data, and the schema is
// built from what came back. That knowledge-network grant is what lets the
// statement run at all; the resource behind it is read through the network's
// proxy account, or checked against the caller when there is no proxy.
func TestQueryAuthorizesEachObjectType(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{}
	service := testService(t, vega, permission)

	if _, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id",
	}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if want := interfaces.KNChildResourceID("kn_1", "ot_order"); !slices.Contains(permission.filtered, want) {
		t.Fatalf("filtered = %v, want it to contain %q", permission.filtered, want)
	}
	if len(permission.operations) != 1 || permission.operations[0] != interfaces.OPERATION_TYPE_QUERY_DATA {
		t.Fatalf("checked operations = %v", permission.operations)
	}
}

// An empty knowledge network is not a refusal: there the label really is
// unknown, and saying so is both true and more useful.
func TestQueryOnEmptyNetworkReportsAnUnknownLabel(t *testing.T) {
	service := testService(t, &recordingVega{}, &stubPermission{})
	service.schema = &fakeSchemaSource{}

	_, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := statusOf(t, err); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
	if !strings.Contains(err.Error(), "unknown label") {
		t.Fatalf("error = %v, want it to read as an unknown label", err)
	}
}

// A caller who may read nothing in the network is told so, rather than being
// told that every label they name does not exist.
func TestQueryRefusesWhenNothingIsReadable(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{denied: map[string]bool{
		interfaces.KNChildResourceID("kn_1", "ot_order"): true,
	}}
	service := testService(t, vega, permission)

	_, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := statusOf(t, err); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
	if vega.request != nil {
		t.Fatal("a denied query still reached vega-backend")
	}
}

// An object type the caller may not query is absent from the schema rather
// than refused, so the endpoint cannot be used to find out what a model holds.
func TestQueryHidesUnreadableObjectTypes(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{denied: map[string]bool{
		interfaces.KNChildResourceID("kn_1", "ot_secret"): true,
	}}
	service := testService(t, vega, permission)
	service.schema = &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{
		objectType("ot_order", "Order", resource("res_order", "orders"), dataProperty("id", "f_id")),
		objectType("ot_secret", "Secret", resource("res_secret", "secrets"), dataProperty("id", "f_id")),
	}}

	_, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (s:Secret) RETURN s.id",
	})
	if err == nil {
		t.Fatal("a hidden object type was queryable")
	}
	if got := statusOf(t, err); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 naming it unknown", got)
	}
	if !strings.Contains(err.Error(), "unknown label") {
		t.Fatalf("error = %v, want it to read as an unknown label", err)
	}
	if vega.request != nil {
		t.Fatal("a hidden object type still reached vega-backend")
	}
}

func TestQueryRejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  string
		status int
	}{
		{name: "empty", query: "", status: http.StatusBadRequest},
		{name: "syntax error", query: "MATCH (o:Order RETURN o.id", status: http.StatusBadRequest},
		{name: "outside the subset", query: "MATCH (o:Order) RETURN lower(o.id)", status: http.StatusBadRequest},
		{name: "not in the model", query: "MATCH (i:Invoice) RETURN i.id", status: http.StatusBadRequest},
		{name: "writing", query: "CREATE (o:Order) RETURN o.id", status: http.StatusBadRequest},
		{name: "limit above the ceiling", query: "MATCH (o:Order) RETURN o.id LIMIT 20000", status: http.StatusBadRequest},
		{name: "too long", query: "MATCH (o:Order) RETURN o.id //" + strings.Repeat("x", MaxQueryLength), status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &recordingVega{}
			service := testService(t, vega, &stubPermission{})

			_, err := service.Query(context.Background(), interfaces.CypherQuery{KNID: "kn_1", Query: tc.query})
			if got := statusOf(t, err); got != tc.status {
				t.Fatalf("status = %d, want %d", got, tc.status)
			}
			if vega.request != nil {
				t.Fatalf("a rejected query still reached vega-backend: %s", vega.request.Query)
			}
		})
	}
}

// The statement names physical tables and columns, and the dependency's error
// may quote it, so nothing from that error reaches the caller.
func TestQueryHidesDependencyErrorDetail(t *testing.T) {
	vega := &recordingVega{err: errors.New("syntax error near `orders`.`f_total`")}
	service := testService(t, vega, &stubPermission{})

	_, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := statusOf(t, err); got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", got)
	}
	if strings.Contains(err.Error(), "f_total") || strings.Contains(err.Error(), "orders") {
		t.Fatalf("error leaked the statement: %v", err)
	}
}

func errorCodeOf(t *testing.T, err error) string {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T (%v), want *rest.HTTPError", err, err)
	}
	return httpErr.BaseError.ErrorCode
}

func errorDetailsOf(t *testing.T, err error) string {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T (%v), want *rest.HTTPError", err, err)
	}
	details, _ := httpErr.BaseError.ErrorDetails.(string)
	return details
}

// relationService is testService over a model with a relation, which is what
// a proxy read needs to be about more than one binding.
func relationService(vega *recordingVega, permission *stubPermission, proxies interfaces.KNProxyBindingResolver) *cypherQueryService {
	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"),
		dataProperty("customer_code", "f_cust_code"),
		dataProperty("amount", "f_total"),
	)
	customer := objectType("ot_customer", "Customer", resource("res_customer", "customers"),
		dataProperty("code", "f_code"),
		dataProperty("name", "f_name"),
	)
	placedBy := relationType("rt_placed_by", "PLACED_BY")
	placedBy.MappingRules = []interfaces.Mapping{{
		SourceProp: interfaces.SimpleProperty{Name: "customer_code"},
		TargetProp: interfaces.SimpleProperty{Name: "code"},
	}}
	return &cypherQueryService{
		ps: permission,
		schema: &fakeSchemaSource{
			objectTypes:   []*interfaces.ObjectType{order, customer},
			relationTypes: []*interfaces.RelationType{placedBy},
		},
		proxies: proxies,
		vba:     vega,
	}
}

// amountAt sets the caller's level on Order.amount, the property the tests
// below name.
func amountAt(level string) *stubPermission {
	return &stubPermission{levels: map[string]string{
		interfaces.KNChildResourceID("kn_1", "ot_order") + ".amount": level,
	}}
}

// Every clause of the subset that can name a property.
var queriesNamingAmount = []struct {
	name  string
	query string
}{
	{name: "returned", query: "MATCH (o:Order) RETURN o.amount"},
	{name: "filtered", query: "MATCH (o:Order) WHERE o.amount > 10 RETURN o.id"},
	{name: "filtered inline", query: "MATCH (o:Order {amount: 10}) RETURN o.id"},
	{name: "string-matched", query: "MATCH (o:Order) WHERE o.amount CONTAINS '1' RETURN o.id"},
	{name: "string-matched under NOT", query: "MATCH (o:Order) WHERE NOT o.amount STARTS WITH '1' RETURN o.id"},
	{name: "null-checked", query: "MATCH (o:Order) WHERE o.amount IS NULL RETURN o.id"},
	{name: "sorted", query: "MATCH (o:Order) RETURN o.id ORDER BY o.amount"},
	{name: "aggregated", query: "MATCH (o:Order) RETURN sum(o.amount) AS total"},
	{name: "across a relation", query: "MATCH (c:Customer)<-[:PLACED_BY]-(o:Order) RETURN c.name, o.amount"},
}

// #1545: a compiled statement reads the column as it is, and nothing masks
// it. A property the caller may know exists but not read in full -- masked or
// schema -- is refused wherever the query names it, by its logical name, and
// nothing reaches vega-backend.
func TestQueryRefusesPropertyWithoutFullAccess(t *testing.T) {
	withLocale(t)
	for _, level := range []string{interfaces.PROPERTY_ACCESS_MASKED, interfaces.PROPERTY_ACCESS_SCHEMA} {
		for _, tc := range queriesNamingAmount {
			t.Run(level+" "+tc.name, func(t *testing.T) {
				vega := &recordingVega{}
				service := relationService(vega, amountAt(level), &stubProxies{mapping: readyProxy()})

				_, err := service.Query(callerContext(), interfaces.CypherQuery{
					KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: tc.query,
				})
				if got := statusOf(t, err); got != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", got)
				}
				if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_PropertyForbidden {
					t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_PropertyForbidden)
				}
				if !strings.Contains(err.Error(), "ot_order.amount") {
					t.Fatalf("error = %v, want it to name the property", err)
				}
				if strings.Contains(err.Error(), "f_total") || strings.Contains(err.Error(), "orders") {
					t.Fatalf("error leaked a physical name: %v", err)
				}
				if vega.request != nil || vega.proxyRequest != nil {
					t.Fatal("a query naming a property the caller may not read still reached vega-backend")
				}
			})
		}
	}
}

// #1374: a property at none does not exist for the caller. Naming it gets the
// very answer a property outside the model gets -- the same code and the same
// words -- so the refusal cannot be used to learn that it is there.
func TestQueryReportsNoneLevelPropertyAsUnknown(t *testing.T) {
	for _, tc := range queriesNamingAmount {
		t.Run(tc.name, func(t *testing.T) {
			vega := &recordingVega{}
			hidden := relationService(vega, amountAt(interfaces.PROPERTY_ACCESS_NONE), nil)
			_, err := hidden.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: tc.query,
			})

			// The same model without the property at all.
			absent := relationService(&recordingVega{}, &stubPermission{}, nil)
			order := absent.schema.(*fakeSchemaSource).objectTypes[0]
			order.DataProperties = slices.DeleteFunc(slices.Clone(order.DataProperties),
				func(property *interfaces.DataProperty) bool { return property.Name == "amount" })
			_, want := absent.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: tc.query,
			})

			if got := statusOf(t, err); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", got)
			}
			if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_InvalidQuery {
				t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_InvalidQuery)
			}
			if got, wantDetails := errorDetailsOf(t, err), errorDetailsOf(t, want); got != wantDetails {
				t.Fatalf("details = %q, want the same as for a missing property: %q", got, wantDetails)
			}
			if vega.request != nil || vega.proxyRequest != nil {
				t.Fatal("a query naming a hidden property still reached vega-backend")
			}
		})
	}
}

// A near miss is answered with suggestions, and a property at none is never
// among them, nor anywhere else in the answer. At full it is offered.
func TestQueryNeverSuggestsNoneLevelProperty(t *testing.T) {
	query := interfaces.CypherQuery{KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.amoun"}

	_, err := relationService(&recordingVega{}, amountAt(interfaces.PROPERTY_ACCESS_NONE), nil).
		Query(callerContext(), query)
	if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_InvalidQuery {
		t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_InvalidQuery)
	}
	if strings.Contains(err.Error(), "amount") {
		t.Fatalf("error = %v, it mentions a property the caller may not know exists", err)
	}

	_, err = relationService(&recordingVega{}, &stubPermission{}, nil).Query(callerContext(), query)
	if !strings.Contains(errorDetailsOf(t, err), `did you mean amount`) {
		t.Fatalf("error = %v, want a visible property to be suggested", err)
	}
}

// Levels are read once per object type, for all its data properties, whatever
// the query does with it; the check after compiling asks nothing new. The join
// keys a relation type maps come from the model, not from the caller, so they
// are neither checked nor mentioned, at any level.
func TestQueryReadsPropertyLevelsOncePerObjectType(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{levels: map[string]string{
		interfaces.KNChildResourceID("kn_1", "ot_order") + ".customer_code": interfaces.PROPERTY_ACCESS_NONE,
		interfaces.KNChildResourceID("kn_1", "ot_customer") + ".code":       interfaces.PROPERTY_ACCESS_MASKED,
	}}
	service := relationService(vega, permission, nil)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer), (p:Order) WHERE c.name = 'a' RETURN o.id, p.amount",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	orderRef := interfaces.KNChildResourceID("kn_1", "ot_order")
	customerRef := interfaces.KNChildResourceID("kn_1", "ot_customer")
	if want := map[string]int{orderRef: 1, customerRef: 1}; !maps.Equal(permission.propertyCalls, want) {
		t.Fatalf("property-level calls = %v, want %v", permission.propertyCalls, want)
	}
	if want := []string{"id", "customer_code", "amount"}; !slices.Equal(permission.propertyAsked[orderRef], want) {
		t.Fatalf("asked for %v, want every data property %v", permission.propertyAsked[orderRef], want)
	}
	if vega.request == nil {
		t.Fatal("the statement did not run")
	}
}

// #1564: a public object type read hides a logic property computed from a
// property at none. Cypher cannot query a logic property either way, but its
// refusal must not confirm one the object type read would not show: it reads
// as unknown then, and names the logic property only when it is visible.
func TestQueryAgreesWithPublicReadsOnHiddenLogicProperties(t *testing.T) {
	withScore := func(permission *stubPermission) *cypherQueryService {
		service := relationService(&recordingVega{}, permission, nil)
		order := service.schema.(*fakeSchemaSource).objectTypes[0]
		order.LogicProperties = []*interfaces.LogicProperty{{
			Name: "score", Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
			Parameters: []interfaces.Parameter{{Name: "p", ValueFrom: interfaces.VALUE_FROM_PROPERTY, Value: "amount"}},
		}}
		return service
	}
	query := interfaces.CypherQuery{KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.score"}

	_, err := withScore(amountAt(interfaces.PROPERTY_ACCESS_NONE)).Query(callerContext(), query)
	if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_InvalidQuery {
		t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_InvalidQuery)
	}
	if details := errorDetailsOf(t, err); strings.Contains(details, "logic property") ||
		!strings.Contains(details, `has no property "score"`) || strings.Contains(details, "amount") {
		t.Fatalf("details = %q, want the answer for a property that does not exist", details)
	}

	_, err = withScore(amountAt(interfaces.PROPERTY_ACCESS_FULL)).Query(callerContext(), query)
	if details := errorDetailsOf(t, err); !strings.Contains(details, "is a logic property") {
		t.Fatalf("details = %q, want a visible logic property named as one", details)
	}
}

// Full access passes.
func TestQueryReadsFullProperty(t *testing.T) {
	vega := &recordingVega{}
	service := relationService(vega, amountAt(interfaces.PROPERTY_ACCESS_FULL), nil)

	if _, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) WHERE o.amount > 1 RETURN o.amount",
	}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request == nil {
		t.Fatal("the statement did not run")
	}
}

// A property-level decision that cannot be made fails the query closed.
func TestQueryFailsClosedWhenPropertyAccessIsUnavailable(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{propertyErr: rest.NewHTTPError(context.Background(),
		http.StatusInternalServerError, berrors.BknBackend_InternalError_CheckPermissionFailed)}
	service := testService(t, vega, permission)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := statusOf(t, err); got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", got)
	}
	if vega.request != nil || vega.proxyRequest != nil {
		t.Fatal("a query whose property access is undecided still reached vega-backend")
	}
}

// A caller holding the resource grants is answered under their own identity,
// exactly as before there was a proxy: the proxy is not even resolved, so no
// vega version can change what they see.
func TestQueryRunsUnderCallerIdentityFirst(t *testing.T) {
	spans := recordSpans(t)
	vega := &recordingVega{response: &interfaces.RawQueryResponse{
		Columns: []interfaces.RawQueryColumn{{Name: "id"}}, Entries: []map[string]any{{"id": "o-1"}},
	}}
	proxies := &stubProxies{mapping: readyProxy()}
	service := relationService(vega, &stubPermission{}, proxies)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entries) != 1 || vega.request == nil {
		t.Fatalf("the statement did not run under the caller's identity: %+v", result)
	}
	if vega.proxyRequest != nil || proxies.bindings != nil {
		t.Fatal("a caller vega-backend accepted still went to the proxy")
	}
	if path, reason := spans.attrs["cypher.execution_path"], spans.attrs["cypher.fallback_reason"]; path != "caller" ||
		reason != "" {
		t.Fatalf("span says path %q reason %q, want caller and no reason", path, reason)
	}
}

// A failure other than a refusal is not something another identity could
// answer: it is reported as it is, and the proxy is not tried.
func TestQueryDoesNotUseProxyForOtherFailures(t *testing.T) {
	vega := &recordingVega{err: interfaces.NewDependencyError("vega", "raw_query",
		interfaces.DependencyDownstreamError, http.StatusInternalServerError)}
	proxies := &stubProxies{mapping: readyProxy()}
	service := relationService(vega, &stubPermission{}, proxies)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_QueryFailed {
		t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_QueryFailed)
	}
	if vega.proxyRequest != nil || proxies.bindings != nil {
		t.Fatal("a failed run was retried under the proxy")
	}
}

// #1545: a caller holding query_data on the object types and the relation type
// but nothing on the resources behind them is refused under their own
// identity. Once every binding the plan reads through resolves, the same
// statement goes to the same raw query route under the network's proxy
// account, and that answer is the caller's.
func TestQueryRetriesRefusedCallerUnderProxyAccount(t *testing.T) {
	spans := recordSpans(t)
	vega := &recordingVega{
		err: interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyForbidden, http.StatusForbidden),
		response: &interfaces.RawQueryResponse{
			Columns: []interfaces.RawQueryColumn{{Name: "id"}, {Name: "name"}},
			Entries: []map[string]any{{"id": "o-1", "name": "c-1"}},
		},
	}
	proxies := &stubProxies{mapping: readyProxy()}
	service := relationService(vega, &stubPermission{}, proxies)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name LIMIT 3",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if vega.request == nil || vega.proxyRequest == nil {
		t.Fatal("want the caller's run refused and the statement run again under the proxy")
	}
	if want := (interfaces.AccountInfo{ID: "proxy-1", Type: interfaces.KNProxyAccountTypeApp}); *vega.proxyAccount != want {
		t.Fatalf("ran under %+v, want the proxy account %+v", *vega.proxyAccount, want)
	}
	if vega.proxyRequest.Query != vega.request.Query || vega.proxyRequest.Paging != vega.request.Paging {
		t.Fatalf("proxy ran %+v, want the caller's statement %+v", vega.proxyRequest, vega.request)
	}
	if path, reason := spans.attrs["cypher.execution_path"], spans.attrs["cypher.fallback_reason"]; path != "proxy" ||
		reason != "" {
		t.Fatalf("span says path %q reason %q, want proxy and no reason", path, reason)
	}

	// The proxy was resolved for exactly the bindings the plan reads through,
	// so the published model vouches for every resource the statement touches.
	wantBindings := []interfaces.KNProxyBinding{
		{ChildType: "object_type", ChildID: "ot_order", TargetType: "resource", TargetID: "res_order", Operation: "query_data"},
		{ChildType: "object_type", ChildID: "ot_customer", TargetType: "resource", TargetID: "res_customer", Operation: "query_data"},
		{ChildType: "relation_type", ChildID: "rt_placed_by", TargetType: "resource", TargetID: "res_order", Operation: "query_data"},
		{ChildType: "relation_type", ChildID: "rt_placed_by", TargetType: "resource", TargetID: "res_customer", Operation: "query_data"},
	}
	if proxies.knID != "kn_1" || !slices.Equal(proxies.bindings, wantBindings) {
		t.Fatalf("resolved %s %+v, want %+v", proxies.knID, proxies.bindings, wantBindings)
	}
	if !strings.Contains(vega.proxyRequest.Query, "{{.res_order}}") ||
		!strings.Contains(vega.proxyRequest.Query, "{{.res_customer}}") {
		t.Fatalf("statement = %s", vega.proxyRequest.Query)
	}
}

// A refusal under the proxy account too -- a resource the proxy holds no
// view_detail on, such as an indirect relation's backing resource -- is the
// answer: the caller is told the read was forbidden, without the statement.
func TestQueryReportsProxyRefusalAsForbidden(t *testing.T) {
	refused := interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyForbidden, http.StatusForbidden)
	vega := &recordingVega{err: refused, proxyErr: refused}
	service := relationService(vega, &stubPermission{}, &stubProxies{mapping: readyProxy()})

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := statusOf(t, err); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
	if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_Forbidden {
		t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_Forbidden)
	}
	if strings.Contains(err.Error(), "SELECT") || strings.Contains(err.Error(), "res_order") {
		t.Fatalf("error leaked the statement: %v", err)
	}
	if vega.proxyRequest == nil {
		t.Fatal("the proxy was not tried")
	}
}

// A refused caller whose query the proxy cannot serve is told the read was
// forbidden, and the query span says why the proxy was not tried.
func TestQueryReportsRefusalWhenProxyUnavailable(t *testing.T) {
	notSynchronized := readyProxy()
	notSynchronized.SyncedModelVersion = "sha256:older"
	for _, tc := range []struct {
		name    string
		branch  string
		proxies interfaces.KNProxyBindingResolver
		reason  string
	}{
		{name: "no resolver", branch: interfaces.MAIN_BRANCH, reason: "no_resolver"},
		{name: "mapping missing", branch: interfaces.MAIN_BRANCH, reason: "proxy_unavailable", proxies: &stubProxies{
			err: rest.NewHTTPError(context.Background(), http.StatusNotFound,
				berrors.BknBackend_KnowledgeNetwork_ProxyMappingNotFound),
		}},
		{name: "not synchronized", branch: interfaces.MAIN_BRANCH, reason: "proxy_unavailable", proxies: &stubProxies{
			err: rest.NewHTTPError(context.Background(), http.StatusServiceUnavailable,
				berrors.BknBackend_KnowledgeNetwork_ProxySyncPending),
		}},
		{name: "disabled", branch: interfaces.MAIN_BRANCH, reason: "proxy_unavailable", proxies: &stubProxies{
			err: rest.NewHTTPError(context.Background(), http.StatusServiceUnavailable,
				berrors.BknBackend_KnowledgeNetwork_ProxyDisabled),
		}},
		{name: "mapping returned out of sync", branch: interfaces.MAIN_BRANCH, reason: "proxy_unavailable",
			proxies: &stubProxies{mapping: notSynchronized}},
		{name: "another branch", branch: "draft", reason: "non_main_branch",
			proxies: &stubProxies{mapping: readyProxy()}},
		// The resolver leaves out a binding that is not published; one statement
		// reads through all of them, so one missing refuses the proxy outright.
		{name: "a relation binding not published", branch: interfaces.MAIN_BRANCH, reason: "binding_not_published",
			proxies: &stubProxies{mapping: readyProxy(), unpublished: map[string]bool{"rt_placed_by": true}}},
		{name: "an object type binding not published", branch: interfaces.MAIN_BRANCH, reason: "binding_not_published",
			proxies: &stubProxies{mapping: readyProxy(), unpublished: map[string]bool{"ot_customer": true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := recordSpans(t)
			vega := &recordingVega{err: interfaces.NewDependencyError("vega", "raw_query",
				interfaces.DependencyForbidden, http.StatusForbidden)}
			service := relationService(vega, &stubPermission{}, tc.proxies)

			_, err := service.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: tc.branch,
				Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id",
			})
			if got := errorCodeOf(t, err); got != berrors.BknBackend_Cypher_Forbidden {
				t.Fatalf("code = %s, want %s", got, berrors.BknBackend_Cypher_Forbidden)
			}
			if vega.proxyRequest != nil {
				t.Fatal("the statement ran under a proxy that could not serve it")
			}
			if path, reason := spans.attrs["cypher.execution_path"], spans.attrs["cypher.fallback_reason"]; path != "caller" ||
				reason != tc.reason {
				t.Fatalf("span says path %q reason %q, want caller %q", path, reason, tc.reason)
			}
		})
	}
}

// #1545: vega-backend refusing a resource was reported as a 500 with nothing
// to diagnose. It is a 403 with its own code now, and still carries nothing
// from the statement.
func TestQueryReportsVegaRefusalAsForbidden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "forbidden",
			err:    interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyForbidden, http.StatusForbidden),
			status: http.StatusForbidden,
			code:   berrors.BknBackend_Cypher_Forbidden,
		},
		{
			name: "failed",
			err: interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyDownstreamError,
				http.StatusInternalServerError),
			status: http.StatusInternalServerError,
			code:   berrors.BknBackend_Cypher_QueryFailed,
		},
		{
			name:   "unreachable",
			err:    interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyUnavailable, 0),
			status: http.StatusInternalServerError,
			code:   berrors.BknBackend_Cypher_QueryFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &recordingVega{err: tc.err}
			service := testService(t, vega, &stubPermission{})

			_, err := service.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.amount",
			})
			if got := statusOf(t, err); got != tc.status {
				t.Fatalf("status = %d, want %d", got, tc.status)
			}
			if got := errorCodeOf(t, err); got != tc.code {
				t.Fatalf("code = %s, want %s", got, tc.code)
			}
			if strings.Contains(err.Error(), "f_total") || strings.Contains(err.Error(), "orders") ||
				strings.Contains(err.Error(), "SELECT") {
				t.Fatalf("error leaked the statement: %v", err)
			}
		})
	}
}

// A row filter is ANDed onto the caller's condition, so a caller's OR must stay
// grouped: without the parentheses the filter would bind to one branch only and
// the other would read rows the filter hides.
func TestQueryRowFilterGroupsStringMatchCondition(t *testing.T) {
	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"), dataProperty("region", "f_region"), dataProperty("amount", "f_amount"))
	east := "east"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order": {
			Kind: "in", Property: "region",
			Values: []interfaces.RowFilterValue{{Type: "string", String: &east}},
		},
	}}
	vega := &recordingVega{}
	service := &cypherQueryService{
		ps:     permission,
		schema: &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{order}},
		vba:    vega,
	}
	if _, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query:      "MATCH (o:Order) WHERE o.id STARTS WITH 'a%' OR NOT o.id CONTAINS $s RETURN o.id AS id",
		Parameters: map[string]any{"s": "x_y"},
	}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 WHERE (t0.`f_id` LIKE 'a!%%' ESCAPE '!' OR NOT t0.`f_id` LIKE '%x!_y%' ESCAPE '!') AND t0.`f_region` IN ('east') LIMIT 1000"
	if got := vega.request.Query; got != want {
		t.Fatalf("statement = %s\nwant      = %s", got, want)
	}
}
