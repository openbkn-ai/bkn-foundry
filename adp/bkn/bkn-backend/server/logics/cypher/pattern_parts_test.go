// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// sqlService is the relation model on the SQL executor.
func sqlService(vega *recordingVega) *cypherQueryService {
	service := graphService(vega)
	service.appSetting = &common.AppSetting{ServerSetting: common.ServerSetting{CypherExecutor: common.CypherExecutorSQL}}
	return service
}

func TestTwoWholeTablesAreRefused(t *testing.T) {
	for query, refused := range map[string]bool{
		// Every row of one table against every row of the other.
		"MATCH (o:Order), (c:Customer) RETURN count(*) AS n": true,
		// A condition under OR narrows neither side.
		"MATCH (o:Order), (c:Customer) WHERE o.customer_code = c.code OR o.amount > 3 RETURN count(*) AS n": true,
		// A relationship, an equality or a shared node connects them.
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN count(*) AS n":                              false,
		"MATCH (o:Order), (c:Customer) WHERE o.customer_code = c.code RETURN count(*) AS n":            false,
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer), (c)<-[:PLACED_BY]-(p:Order) RETURN count(*) AS n": false,
		// One side read whole against a narrowed one is a listing.
		"MATCH (o:Order), (c:Customer {code: 'A'}) RETURN o.id, c.name LIMIT 5":   false,
		"MATCH (o:Order), (c:Customer) WHERE c.name = 'Acme' RETURN o.id LIMIT 5": false,
		// So are two narrowed sides.
		"MATCH (o:Order {id: 1}), (c:Customer {code: 'A'}) RETURN o.id, c.name": false,
	} {
		vega := &recordingVega{}
		_, err := sqlService(vega).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
		})
		var httpErr *rest.HTTPError
		details := ""
		if errors.As(err, &httpErr) {
			details, _ = httpErr.BaseError.ErrorDetails.(string)
		}
		gotRefused := strings.Contains(details, "a.code = b.code")
		if gotRefused != refused {
			t.Errorf("%s: refused = %v, err = %v", query, gotRefused, err)
		}
		if refused && vega.request != nil {
			t.Errorf("%s: a refused query reached vega-backend", query)
		}
	}
}

func TestQueryWithoutLimitSaysWhenItFilledTheDefault(t *testing.T) {
	rows := func(n int) *interfaces.RawQueryResponse {
		entries := make([]map[string]any, n)
		for i := range entries {
			entries[i] = map[string]any{"id": i}
		}
		return &interfaces.RawQueryResponse{Columns: []interfaces.RawQueryColumn{{Name: "id"}}, Entries: entries}
	}
	for _, c := range []struct {
		query   string
		rows    int
		limited bool
	}{
		{"MATCH (o:Order) RETURN o.id AS id", interfaces.CYPHER_DEFAULT_LIMIT, true},
		{"MATCH (o:Order) RETURN o.id AS id", interfaces.CYPHER_DEFAULT_LIMIT - 1, false},
		// The caller asked for exactly these rows.
		{"MATCH (o:Order) RETURN o.id AS id LIMIT 1000", interfaces.CYPHER_DEFAULT_LIMIT, false},
	} {
		result, err := sqlService(&recordingVega{response: rows(c.rows)}).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: c.query,
		})
		if err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if result.Limited != c.limited {
			t.Errorf("%s with %d rows: limited = %v", c.query, c.rows, result.Limited)
		}
	}
}
