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

type stubPermission struct {
	interfaces.PermissionService
	resource   interfaces.PermissionResource
	operations []string
	err        error
}

func (p *stubPermission) CheckPermission(_ context.Context, resource interfaces.PermissionResource, ops []string) error {
	p.resource = resource
	p.operations = ops
	return p.err
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

// The knowledge network is checked here for query_data; vega-backend checks
// each resource for view_detail with the caller's own identity. Neither check
// replaces the other.
func TestQueryChecksKNPermissionBeforeCompiling(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{err: rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)}
	service := testService(t, vega, permission)

	_, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id",
	})
	if got := statusOf(t, err); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
	if permission.resource.Type != interfaces.RESOURCE_TYPE_KN || permission.resource.ID != "kn_1" {
		t.Fatalf("checked resource = %+v", permission.resource)
	}
	if len(permission.operations) != 1 || permission.operations[0] != interfaces.OPERATION_TYPE_QUERY_DATA {
		t.Fatalf("checked operations = %v", permission.operations)
	}
	if vega.request != nil {
		t.Fatal("a denied query still reached vega-backend")
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
		{name: "outside the subset", query: "MATCH (o:Order) RETURN count(*)", status: http.StatusBadRequest},
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
