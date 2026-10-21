// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

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
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c, count(o) AS n",
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
