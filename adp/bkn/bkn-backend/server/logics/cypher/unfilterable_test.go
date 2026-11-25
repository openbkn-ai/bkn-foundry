// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// #3365: a dataset field can be readable while its data API refuses a
// condition on it (JSON, by default). The order's amount is stored that way.
func unfilterableAmount(fieldType string, features ...interfaces.PropertyFeature) map[string]*interfaces.VegaResource {
	filterable, unfilterable := true, false
	return map[string]*interfaces.VegaResource{
		"res_order": {ID: "res_order", Category: "dataset", SchemaDefinition: []*interfaces.Property{
			{Name: "f_id", Type: "string", Filterable: &filterable},
			{Name: "f_cust_code", Type: "string", Filterable: &filterable},
			{Name: "f_total", Type: fieldType, Filterable: &unfilterable, Features: features},
		}},
		"res_customer": {ID: "res_customer", Category: "dataset", SchemaDefinition: []*interfaces.Property{
			{Name: "f_code", Type: "string", Filterable: &filterable},
			{Name: "f_name", Type: "string", Filterable: &filterable},
		}},
	}
}

func unfilterableService(resources map[string]*interfaces.VegaResource) (*cypherQueryService, *datasetVega) {
	vega := &datasetVega{accountDataAPI: accountDataAPI{api: newFakeDataAPI(relationServiceData()), resources: resources}}
	return datasetService(vega), vega
}

// A condition on such a field is refused before anything is read, naming the
// property and its object type rather than failing as a dependency.
func TestQueryOutsideSQLRefusesAConditionOnAnUnfilterableProperty(t *testing.T) {
	for name, query := range map[string]string{
		"where":            "MATCH (o:Order) WHERE o.amount = 5 RETURN o.id AS id",
		"inline":           "MATCH (o:Order {amount: 5}) RETURN o.id AS id",
		"under or":         "MATCH (o:Order) WHERE o.amount = 5 OR o.id = 'o-1' RETURN o.id AS id",
		"null check":       "MATCH (o:Order) WHERE o.amount IS NULL RETURN o.id AS id",
		"across a link":    "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WHERE o.amount = 5 RETURN c.name AS name",
		"optional matched": "MATCH (c:Customer) OPTIONAL MATCH (c)<-[:PLACED_BY]-(o:Order) WHERE o.amount = 5 RETURN c.name AS name, o.id AS id",
	} {
		t.Run(name, func(t *testing.T) {
			service, vega := unfilterableService(unfilterableAmount("json"))
			_, err := service.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
			})
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
				httpErr.ErrorCode != "BknBackend.Cypher.Unsupported" {
				t.Fatalf("err = %v, want a 400 refusal", err)
			}
			details, _ := httpErr.ErrorDetails.(string)
			if !strings.Contains(details, "amount") || !strings.Contains(details, "Order") || strings.Contains(details, "f_total") {
				t.Errorf("details = %q, want the property and object type by name, not the column", details)
			}
			if len(vega.readers) != 0 {
				t.Errorf("read the data API %d times before refusing", len(vega.readers))
			}
		})
	}
}

// The field is still returned, and other fields of the same resource are
// still filtered on.
func TestQueryOutsideSQLStillReturnsAnUnfilterableProperty(t *testing.T) {
	for query, want := range map[string][]map[string]any{
		"MATCH (o:Order) RETURN DISTINCT o.amount AS v ORDER BY v": {{"v": 5}, {"v": 7}},
		"MATCH (o:Order) WHERE o.id = 'o-2' RETURN o.amount AS v":  {{"v": 7}},
	} {
		service, _ := unfilterableService(unfilterableAmount("json"))
		result, err := service.Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
		})
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		// Numbers come back as the executor reads them; the values are what count.
		if fmt.Sprint(result.Entries) != fmt.Sprint(want) {
			t.Errorf("%s: entries = %v, want %v", query, result.Entries, want)
		}
	}
}

// A dataset's text field whose own switch is off is still compared through its
// keyword subfield in the local index, which vega-backend checks instead, so it is
// not refused. Without a queryable index the field itself is checked, and refused;
// so is a table's, which Cypher reads from the data source even when it has an index.
func TestQueryOutsideSQLLeavesATextFieldWithAKeywordSubfield(t *testing.T) {
	for name, c := range map[string]struct {
		category, status string
		refused          bool
	}{
		"dataset available": {"dataset", interfaces.ResourceLocalIndexStatusAvailable, false},
		"dataset readonly":  {"dataset", interfaces.ResourceLocalIndexStatusReadOnly, false},
		"dataset no index":  {"dataset", "", true},
		"table available":   {"table", interfaces.ResourceLocalIndexStatusAvailable, true},
	} {
		t.Run(name, func(t *testing.T) {
			resources := unfilterableAmount("text",
				interfaces.PropertyFeature{FeatureName: "keyword", FeatureType: interfaces.FieldFeatureType_Keyword})
			resources["res_order"].Category = c.category
			resources["res_order"].LocalIndexStatus = c.status
			service, vega := unfilterableService(resources)
			_, err := service.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) WHERE o.amount = 5 RETURN o.id AS id",
			})
			if c.refused {
				var httpErr *httperrors.HTTPError
				if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
					httpErr.ErrorCode != "BknBackend.Cypher.Unsupported" || len(vega.readers) != 0 {
					t.Fatalf("err = %v after %d reads, want a 400 refusal before any read", err, len(vega.readers))
				}
				return
			}
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(vega.readers) == 0 {
				t.Error("the condition never reached the data API")
			}
		})
	}
}

// rejectingVega refuses every resource data read as a bad request, as
// vega-backend refuses a condition it cannot evaluate.
type rejectingVega struct {
	datasetVega
	lookupFails bool
}

func (v *rejectingVega) QueryResourceDataAs(context.Context, interfaces.AccountInfo, string,
	*interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	return nil, interfaces.NewDependencyError("vega", "resource_data", interfaces.DependencyDownstreamError, http.StatusBadRequest)
}

func (v *rejectingVega) GetResourcesByIDs(ctx context.Context, ids []string) ([]*interfaces.VegaResource, error) {
	if v.lookupFails {
		return nil, errors.New("vega-backend unavailable")
	}
	return v.datasetVega.GetResourcesByIDs(ctx, ids)
}

// A read the data source refuses as a bad request is the statement's to
// change: it reads as a parameter error, not as a failed dependency, whether
// or not the schemas could be checked first.
func TestQueryOutsideSQLReportsARejectedConditionAsAParameterError(t *testing.T) {
	for name, lookupFails := range map[string]bool{"schemas read": false, "schemas unavailable": true} {
		t.Run(name, func(t *testing.T) {
			vega := &rejectingVega{lookupFails: lookupFails}
			vega.resources = map[string]*interfaces.VegaResource{"res_order": {ID: "res_order", Category: "dataset"}}
			service := datasetService(&vega.datasetVega)
			service.vba = vega
			_, err := service.Query(callerContext(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) WHERE o.amount = 5 RETURN o.id AS id",
			})
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
				httpErr.ErrorCode != "BknBackend.Cypher.InvalidParameter" {
				t.Fatalf("err = %v, want a 400 parameter error", err)
			}
			if details, _ := httpErr.ErrorDetails.(string); !strings.Contains(details, "WHERE") {
				t.Errorf("details = %q, want them to point at the condition", details)
			}
		})
	}
}
