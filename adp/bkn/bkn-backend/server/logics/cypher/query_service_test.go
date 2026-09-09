// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/interfaces"
)

type recordingVega struct {
	interfaces.VegaBackendAccess
	request  *interfaces.RawQueryRequest
	response *interfaces.RawQueryResponse
	err      error
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

// stubPermission answers the child-resource filter the way bkn-safe would.
// denied lists knowledge-network child ids (object types, relation types) the
// caller may not query; everything else passes.
type stubPermission struct {
	interfaces.PermissionService
	filtered   []string
	operations []string
	denied     map[string]bool
	err        error
}

func (p *stubPermission) FilterResources(_ context.Context, _ string, ids []string,
	ops []string, _ bool, _ []string) (map[string]interfaces.PermissionResourceOps, error) {

	p.filtered = append(p.filtered, ids...)
	p.operations = ops
	if p.err != nil {
		return nil, p.err
	}
	matched := map[string]interfaces.PermissionResourceOps{}
	for _, id := range ids {
		if p.denied[id] {
			continue
		}
		matched[id] = interfaces.PermissionResourceOps{ResourceID: id, Operations: ops}
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
// built from what came back. vega-backend then checks each resource for
// view_detail under the caller's own identity; neither check replaces the
// other.
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
