// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// datasetVega refuses every statement as vega-backend refuses one over a
// dataset or an index, and serves the resource data API as it does.
type datasetVega struct {
	accountDataAPI
	statements int
}

func (v *datasetVega) RawQuery(context.Context, *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.statements++
	return nil, interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyUnsupportedSource, http.StatusBadRequest)
}

func datasetService(vega *datasetVega) *cypherQueryService {
	service := relationService(nil, &stubPermission{}, nil)
	service.vba = vega
	service.appSetting = &common.AppSetting{ServerSetting: common.ServerSetting{CypherExecutor: common.CypherExecutorSQL}}
	for _, objectType := range service.schema.(*fakeSchemaSource).objectTypes {
		switch objectType.OTID {
		case "ot_order":
			objectType.PrimaryKeys = []string{"id"}
		case "ot_customer":
			objectType.PrimaryKeys = []string{"code"}
		}
	}
	return service
}

// A query over resources SQL cannot read runs through the resource data API,
// whichever executor is configured.
func TestQueryOutsideSQLRunsThroughTheDataAPI(t *testing.T) {
	spans := recordSpans(t)
	vega := &datasetVega{accountDataAPI: accountDataAPI{api: newFakeDataAPI(relationServiceData())}}
	result, err := datasetService(vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.statements != 1 {
		t.Errorf("statements sent = %d, want the one SQL attempt", vega.statements)
	}
	if want := []map[string]any{{"id": "o-1", "name": "Ann"}}; !reflect.DeepEqual(result.Entries, want) {
		t.Fatalf("entries = %v, want %v", result.Entries, want)
	}
	if spans.attrs["cypher.executor"] != "bindjoin" || spans.attrs["cypher.fallback"] != "non_sql_source" {
		t.Errorf("span attrs = %v", spans.attrs)
	}
}

// What only the SQL path answers is refused there, naming what is left.
func TestQueryOutsideSQLRefusesWhatOnlySQLAnswers(t *testing.T) {
	for _, query := range []string{
		"MATCH (o:Order) RETURN sum(CASE WHEN o.amount > 3 THEN 1 ELSE 0 END) AS n",
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS n RETURN count(*) AS groups",
		// collect picks its conclusions through a derived table first.
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c, collect(o) AS orders",
	} {
		vega := &datasetVega{accountDataAPI: accountDataAPI{api: newFakeDataAPI(relationServiceData())}}
		_, err := datasetService(vega).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
		})
		var httpErr *rest.HTTPError
		if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
			httpErr.BaseError.ErrorCode != "BknBackend.Cypher.Unsupported" {
			t.Errorf("%s: err = %v", query, err)
			continue
		}
		if details, _ := httpErr.BaseError.ErrorDetails.(string); !strings.Contains(details, "数据集") {
			t.Errorf("%s: details = %q, want them to say why", query, details)
		}
		if len(vega.readers) != 0 {
			t.Errorf("%s: read the data API, %v", query, vega.readers)
		}
	}
}

// Nodes and relationships come back over the data API as they do over SQL:
// the executor reads their keys and properties as plain columns, and the
// values are built from them.
func TestQueryOutsideSQLReturnsNodesAndRelationships(t *testing.T) {
	spans := recordSpans(t)
	for query, want := range map[string][]string{
		"MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN o, r, c": {
			`"object_type_id":"ot_order","key":{"id"`, `"kind":"relation"`, `"object_type_id":"ot_customer","key":{"code"`,
		},
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c AS source, 'ORDERED' AS relationType, o AS target, count(*) AS n": {
			`"relationType":"ORDERED"`, `"source":{"kind":"object"`, `"target":{"kind":"object"`,
		},
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c, count(o) AS n": {
			`"object_type_id":"ot_customer"`, `"n":1`,
		},
	} {
		// The resources say they are datasets, so the query goes straight to
		// the data API, the route that has to mark the span itself.
		vega := &datasetVega{accountDataAPI: accountDataAPI{
			api: newFakeDataAPI(relationServiceData()),
			resources: map[string]*interfaces.VegaResource{
				"res_order":    {ID: "res_order", Category: "dataset", CatalogID: "cat_ds"},
				"res_customer": {ID: "res_customer", Category: "dataset", CatalogID: "cat_ds"},
			},
		}}
		result, err := datasetService(vega).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
		})
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if vega.statements != 0 {
			t.Errorf("%s: statements sent = %d, want none", query, vega.statements)
		}
		body, _ := json.Marshal(result.Entries)
		for _, part := range want {
			if !strings.Contains(string(body), part) {
				t.Errorf("%s: entries = %s, want %s", query, body, part)
			}
		}
		if len(result.Entries) != 1 || result.ValueTypes == nil || result.Execution == nil {
			t.Errorf("%s: result = %+v", query, result)
		}
		if len(vega.readers) == 0 {
			t.Errorf("%s: did not read the data API", query)
		}
		// Marked as a graph query, as the SQL path marks one.
		if spans.attrs["cypher.graph_values"] != "true" || spans.attrs["cypher.executor"] != "bindjoin" {
			t.Errorf("%s: span attrs = %v", query, spans.attrs)
		}
	}
}

// Where a resource is known not to be in a SQL data source, the query goes
// straight to the data API, and what was learned is remembered.
func TestQueryOutsideSQLKnownFromTheResource(t *testing.T) {
	for name, resources := range map[string]map[string]*interfaces.VegaResource{
		"a dataset": {
			"res_order":    {ID: "res_order", Category: "dataset", CatalogID: "cat_ds"},
			"res_customer": {ID: "res_customer", Category: "dataset", CatalogID: "cat_ds"},
		},
		"two catalogs": {
			"res_order":    {ID: "res_order", Category: "table", CatalogID: "cat_a"},
			"res_customer": {ID: "res_customer", Category: "table", CatalogID: "cat_b"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			vega := &datasetVega{accountDataAPI: accountDataAPI{api: newFakeDataAPI(relationServiceData()), resources: resources}}
			service := datasetService(vega)
			for range 2 {
				result, err := service.Query(callerContext(), interfaces.CypherQuery{
					KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
					Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name AS name",
				})
				if err != nil {
					t.Fatalf("Query: %v", err)
				}
				if len(result.Entries) != 1 {
					t.Fatalf("entries = %v", result.Entries)
				}
			}
			if vega.statements != 0 {
				t.Errorf("statements sent = %d, want none: the resources say SQL cannot read them", vega.statements)
			}
			if _, known := service.resourceSources.Load("res_order"); !known {
				t.Error("where res_order lives was not remembered")
			}
		})
	}
}

// Resources in one SQL data source keep the SQL path.
func TestQueryOnOneSQLSourceStaysOnSQL(t *testing.T) {
	vega := &accountDataAPI{api: newFakeDataAPI(relationServiceData()), resources: map[string]*interfaces.VegaResource{
		"res_order":    {ID: "res_order", Category: "table", CatalogID: "cat_a"},
		"res_customer": {ID: "res_customer", Category: "table", CatalogID: "cat_a"},
	}}
	service := relationService(nil, &stubPermission{}, nil)
	service.vba = vega
	if _, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id AS id",
	}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.sqlRequest == nil || len(vega.readers) != 0 {
		t.Errorf("sql = %v, data API readers = %v", vega.sqlRequest, vega.readers)
	}
}

// slowDatasetVega is a dataset whose data API never answers in time.
type slowDatasetVega struct {
	accountDataAPI
	reads      atomic.Int32
	statements atomic.Int32
}

func (v *slowDatasetVega) QueryResourceDataAs(ctx context.Context, _ interfaces.AccountInfo, _ string,
	_ *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	v.reads.Add(1)
	<-ctx.Done()
	return nil, interfaces.NewDependencyError("vega", "resource_data", interfaces.DependencyTransportKind(ctx.Err()), 0)
}

func (v *slowDatasetVega) RawQuery(context.Context, *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.statements.Add(1)
	return nil, interfaces.NewDependencyError("vega", "raw_query", interfaces.DependencyUnsupportedSource, http.StatusBadRequest)
}

// With the bind-join executor configured, a bind-join that gives up on a
// dataset falls back to SQL, which refuses it for where it lives; it is not
// run a second time, and the refusal is the read budget's.
func TestBindJoinOnADatasetRunsOnce(t *testing.T) {
	spans := recordSpans(t)
	vega := &slowDatasetVega{}
	service := bindJoinService(vega, nil)
	service.bindJoinTimeout = 20 * time.Millisecond

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
	})
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.BaseError.ErrorCode != "BknBackend.Cypher.ReadBudgetExceeded" {
		t.Fatalf("err = %v", err)
	}
	if reads, statements := vega.reads.Load(), vega.statements.Load(); reads != 1 || statements != 1 {
		t.Errorf("data API reads = %d, statements = %d; want the bind-join once and the SQL attempt once", reads, statements)
	}
	if spans.attrs["cypher.fallback"] != "sql_timeout" {
		t.Errorf("span attrs = %v", spans.attrs)
	}
}
