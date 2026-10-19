// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	osclient "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestOpenSearchQueryScoreAndSearchAfter(t *testing.T) {
	for _, scoreSort := range []bool{true, false} {
		name := "explicit primary key sort"
		if scoreSort {
			name = "relevance sort with primary key tie breaker"
		}
		t.Run(name, func(t *testing.T) {
			requests := 0
			client, err := osclient.NewClient(osclient.Config{
				Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					assert.Equal(t, "/table-index/_search", request.URL.Path)
					var body map[string]any
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					assert.Equal(t, true, body["track_scores"])
					assert.Equal(t, []any{"id"}, body["_source"])
					wantSort := []any{map[string]any{"id": map[string]any{"order": "asc"}}}
					if scoreSort {
						wantSort = append([]any{map[string]any{"_score": map[string]any{"order": "desc"}}}, wantSort...)
					}
					assert.Equal(t, wantSort, body["sort"])
					response := `{"hits":{"total":{"value":3},"hits":[{"_id":"1","_score":0.875,"_source":{"id":1},"sort":[0.875,1]},{"_id":"2","_score":0.875,"_source":{"id":2},"sort":[0.875,2]}]}}`
					if requests == 0 {
						assert.NotContains(t, body, "search_after")
					} else {
						wantAfter := []any{float64(2)}
						if scoreSort {
							wantAfter = []any{0.875, float64(2)}
						}
						assert.Equal(t, wantAfter, body["search_after"])
						response = `{"hits":{"total":{"value":3},"hits":[{"_id":"3","_score":0.5,"_source":{"id":3},"sort":[0.5,3]}]}}`
					}
					if !scoreSort {
						response = strings.ReplaceAll(response, `"sort":[0.875,1]`, `"sort":[1]`)
						response = strings.ReplaceAll(response, `"sort":[0.875,2]`, `"sort":[2]`)
						response = strings.ReplaceAll(response, `"sort":[0.5,3]`, `"sort":[3]`)
					}
					requests++
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				}),
			})
			require.NoError(t, err)
			connector := &OpenSearchConnector{client: client}
			resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
			params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id", "_score"},
				Sort: []*interfaces.SortField{{Field: "id", Direction: "asc"}}, Paging: interfaces.PagingRequest{Limit: 2}}
			if scoreSort {
				params.Sort = append([]*interfaces.SortField{{Field: "_score", Direction: "desc"}}, params.Sort...)
			}
			first, err := connector.ExecuteQuery(context.Background(), "table-index", resource, params)
			require.NoError(t, err)
			require.Len(t, first.Entries, 2)
			assert.Equal(t, 0.875, first.Entries[0]["_score"])
			assert.Equal(t, 0.875, first.Entries[1]["_score"])
			params.SearchAfter = first.SearchAfter
			last, err := connector.ExecuteQuery(context.Background(), "table-index", resource, params)
			require.NoError(t, err)
			require.Len(t, last.Entries, 1)
			assert.Equal(t, json.Number("3"), last.Entries[0]["id"])
			assert.Equal(t, 0.5, last.Entries[0]["_score"])
			assert.Equal(t, 2, requests)
		})
	}
}

func TestValidateStoredFilterConditionPreservesErrorSource(t *testing.T) {
	field := &interfaces.Property{Name: "body", OriginalName: "BODY", Type: interfaces.DataType_Text}
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{field}}
	cfg := &interfaces.FilterCondCfg{Name: "body", Operation: filter_condition.OperationEqual,
		ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: "example"}}
	actual, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"body": field})
	require.NoError(t, err)
	params := &interfaces.ResourceDataQueryParams{FixedFilterCondCfg: cfg, FixedActualFilterCond: actual}
	err = (&OpenSearchConnector{}).validateStoredFilterCondition(resource, params)
	var stored *interfaces.StoredConditionBuildError
	require.ErrorAs(t, err, &stored)
	assert.Contains(t, stored.Error(), "no keyword feature")
}

func TestOpenSearchQueryTracksTotalOnlyWhenRequested(t *testing.T) {
	queries := make(chan map[string]any, 3)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var query map[string]any
		require.NoError(t, sonic.Unmarshal(body, &query))
		queries <- query
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		response := `{"hits":{"total":{"value":0},"hits":[]}}`
		if requestCount == 3 {
			response = `{"hits":{"total":{"value":4},"hits":[]},"aggregations":{"count":{"value":4}}}`
		}
		_, err = w.Write([]byte(response))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "status", Type: interfaces.DataType_String}}}

	_, err = connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{})
	require.NoError(t, err)
	assert.NotContains(t, <-queries, "track_total_hits")

	_, err = connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{NeedTotal: true})
	require.NoError(t, err)
	assert.Equal(t, true, (<-queries)["track_total_hits"])

	result, err := connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{
		NeedTotal:   true,
		Aggregation: &interfaces.Aggregation{Property: "status", Aggr: "count"},
	})
	require.NoError(t, err)
	assert.Equal(t, true, (<-queries)["track_total_hits"])
	assert.Equal(t, int64(4), result.Total)
}

func TestOpenSearchQueryUsesTextKeywordField(t *testing.T) {
	for _, fieldName := range []string{"body", "body_alias"} {
		t.Run(fieldName, func(t *testing.T) {
			queries := make(chan map[string]any, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var query map[string]any
				require.NoError(t, sonic.Unmarshal(body, &query))
				queries <- query
				w.Header().Set("Content-Type", "application/json")
				_, err = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]},"aggregations":{"__value":{"value":0},"group_by_body":{"buckets":[]},"group_by_body_alias":{"buckets":[]}}}`))
				require.NoError(t, err)
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			host, portText, err := net.SplitHostPort(serverURL.Host)
			require.NoError(t, err)
			port, err := strconv.Atoi(portText)
			require.NoError(t, err)
			connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}
			resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{
				Name: "body",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "raw",
					FeatureType: interfaces.PropertyFeatureType_Keyword,
				}},
			}}}

			resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{
				Name: "body_alias", OriginalName: "body_alias", OriginalType: "alias", Type: interfaces.DataType_Text,
				Attributes: map[string]any{"path": "body"},
			})

			_, err = connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{
				Sort: []*interfaces.SortField{{Field: fieldName, Direction: "asc"}},
			})
			require.NoError(t, err)
			sortQuery := <-queries
			sortFields := sortQuery["sort"].([]any)
			assert.Contains(t, sortFields[0].(map[string]any), "body.raw")

			_, err = connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{
				Aggregation: &interfaces.Aggregation{Property: fieldName, Aggr: "count_distinct"},
			})
			require.NoError(t, err)
			aggregationQuery := <-queries
			aggregation := aggregationQuery["aggs"].(map[string]any)["__value"].(map[string]any)
			assert.Equal(t, "body.raw", aggregation["cardinality"].(map[string]any)["field"])

			_, err = connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{
				GroupBy: []*interfaces.GroupByItem{{Property: fieldName}},
			})
			require.NoError(t, err)
			groupQuery := <-queries
			group := groupQuery["aggs"].(map[string]any)["group_by_"+fieldName].(map[string]any)
			assert.Equal(t, "body.raw", group["terms"].(map[string]any)["field"])

		})
	}
}

func TestOpenSearchQueryRejectsTextSortWithoutKeyword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("query must be rejected before sending an OpenSearch request")
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text}}}

	result, err := connector.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{
		Sort: []*interfaces.SortField{{Field: "body", Direction: "asc"}},
	})

	require.Error(t, err)
	assert.Nil(t, result)
	var buildErr *interfaces.ConditionBuildError
	require.ErrorAs(t, err, &buildErr)
}

func TestExecuteQueryWithDslPreservesLargeIntegerSourceField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"hits":{"total":{"value":1},"hits":[{"_id":"doc-1","_score":1.5,"_source":{"identifier":110101199001152345}}]}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}, enabled: true}

	result, err := connector.ExecuteQueryWithDsl(context.Background(), "events", `{"query":{"match_all":{}}}`)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, json.Number("110101199001152345"), result.Entries[0]["identifier"])
	assert.Equal(t, float64(1.5), result.Entries[0]["_score"])
	assert.Equal(t, int64(1), result.Total)
}

func TestValidateAnalyzerCallsOpenSearch(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_analyze", r.URL.Path)
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"tokens":[]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}

	available, err := connector.ValidateAnalyzer(context.Background(), "ik_max_word")
	require.NoError(t, err)
	assert.True(t, available)
	assert.Equal(t, 1, requests)
}

func TestValidateAnalyzerReportsUnavailableForOpenSearchResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_analyze", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, err := w.Write([]byte(`{"error":"analyzer not found"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}

	available, err := connector.ValidateAnalyzer(context.Background(), "hanlp_index")
	require.NoError(t, err)
	assert.False(t, available)
}

func TestValidateAnalyzerReturnsDependencyErrorForNonBadRequestResponse(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/_analyze", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				_, err := w.Write([]byte(`{"error":"dependency failure"}`))
				require.NoError(t, err)
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			host, portText, err := net.SplitHostPort(serverURL.Host)
			require.NoError(t, err)
			port, err := strconv.Atoi(portText)
			require.NoError(t, err)
			connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}

			available, err := connector.ValidateAnalyzer(context.Background(), "hanlp_index")
			require.Error(t, err)
			assert.False(t, available)
		})
	}
}

func TestValidateAnalyzerReturnsDependencyErrorForUnrelatedBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_analyze", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, err := w.Write([]byte(`{"error":"invalid analyze request"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}

	available, err := connector.ValidateAnalyzer(context.Background(), "hanlp_index")
	require.Error(t, err)
	assert.False(t, available)
}

func TestExecuteRawQueryFlattensAggregationsIntoEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
			"hits":{"total":{"value":4},"hits":[]},
			"aggregations":{"by_status":{"buckets":[{"key":"open","doc_count":3}]}}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}

	result, err := connector.ExecuteRawQuery(context.Background(), "events", map[string]any{
		"size": 0,
		"aggs": map[string]any{"by_status": map[string]any{"terms": map[string]any{"field": "status"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"status": "open", "__value": json.Number("3")}}, result.Entries)
	require.NotNil(t, result.TotalCount)
	assert.Equal(t, int64(4), *result.TotalCount)
}

func TestExecuteRawQueryPreservesLargeIntegerSourceField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"hits":{"total":{"value":1},"hits":[{"_source":{"identifier":110101199001152345},"sort":[110101199001152345]}]}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	connector := &OpenSearchConnector{Config: &opensearchConfig{Host: host, Port: port}}

	result, err := connector.ExecuteRawQuery(context.Background(), "events", map[string]any{"query": map[string]any{"match_all": map[string]any{}}})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, json.Number("110101199001152345"), result.Entries[0]["identifier"])
	assert.Equal(t, []any{json.Number("110101199001152345")}, result.SearchAfter)
	assert.Equal(t, int64(1), *result.TotalCount)
}

func TestOpenSearchFlattenNestedGroupByRows(t *testing.T) {
	conn := &OpenSearchConnector{}

	t.Run("returns empty rows for malformed buckets", func(t *testing.T) {
		rows := conn.flattenNestedGroupByRows(map[string]any{"buckets": "bad"}, &interfaces.ResourceDataQueryParams{}, "__value")

		assert.Empty(t, rows)
	})

	t.Run("uses key_as_string and truncates by limit", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			Paging: interfaces.PagingRequest{Limit: 1},
			Aggregation: &interfaces.Aggregation{
				Alias: "__value",
			},
			GroupBy: []*interfaces.GroupByItem{
				{Property: "created_at"},
			},
		}
		rootAgg := map[string]any{
			"buckets": []any{
				map[string]any{"key_as_string": "2026-07-09", "__value": map[string]any{"value": float64(3)}},
				map[string]any{"key_as_string": "2026-07-10", "__value": map[string]any{"value": float64(4)}},
			},
		}

		rows := conn.flattenNestedGroupByRows(rootAgg, params, "__value")

		require.Len(t, rows, 1)
		assert.Equal(t, "2026-07-09", rows[0]["created_at"])
		assert.Equal(t, float64(3), rows[0]["__value"])
	})

	t.Run("returns parent row when child aggregation is missing", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{
				{Property: "country"},
				{Property: "city"},
			},
		}
		rootAgg := map[string]any{
			"buckets": []any{
				map[string]any{"key": "CN"},
			},
		}

		rows := conn.flattenNestedGroupByRows(rootAgg, params, "")

		require.Len(t, rows, 1)
		assert.Equal(t, "CN", rows[0]["country"])
		assert.NotContains(t, rows[0], "city")
	})
	t.Run("flatten nested group by rows two dimensions", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		params := &interfaces.ResourceDataQueryParams{
			Paging: interfaces.PagingRequest{Limit: 10},
			Aggregation: &interfaces.Aggregation{
				Property: "id",
				Aggr:     "count",
				Alias:    "__value",
			},
			GroupBy: []*interfaces.GroupByItem{
				{Property: "kn_id"},
				{Property: "module_type"},
			},
		}

		rootAgg := map[string]any{
			"buckets": []any{
				map[string]any{
					"key": "yzm_mock_system",
					"group_by_module_type": map[string]any{
						"buckets": []any{
							map[string]any{
								"key": "a",
								"__value": map[string]any{
									"value": float64(3),
								},
							},
							map[string]any{
								"key": "b",
								"__value": map[string]any{
									"value": float64(2),
								},
							},
						},
					},
				},
			},
		}

		rows := conn.flattenNestedGroupByRows(rootAgg, params, "__value")
		require.Len(t, rows, 2)

		assert.Equal(t, "yzm_mock_system", rows[0]["kn_id"])
		assert.Equal(t, "a", rows[0]["module_type"])
		assert.Equal(t, float64(3), rows[0]["__value"])
		assert.Equal(t, "yzm_mock_system", rows[1]["kn_id"])
		assert.Equal(t, "b", rows[1]["module_type"])
		assert.Equal(t, float64(2), rows[1]["__value"])
	})
}

func TestNestedTermsSize(t *testing.T) {
	tests := []struct {
		name       string
		levelIndex int
		numLevels  int
		limit      int
		want       int
	}{
		{name: "single level default", levelIndex: 0, numLevels: 1, limit: 0, want: 10},
		{name: "single level uses limit", levelIndex: 0, numLevels: 1, limit: 7, want: 7},
		{name: "inner level uses limit", levelIndex: 1, numLevels: 2, limit: 5, want: 5},
		{name: "outer level minimum", levelIndex: 0, numLevels: 2, limit: 0, want: 1000},
		{name: "outer level scaled lower bound", levelIndex: 0, numLevels: 2, limit: 1, want: 100},
		{name: "outer level scaled upper bound", levelIndex: 0, numLevels: 2, limit: 200, want: 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nestedTermsSize(tt.levelIndex, tt.numLevels, tt.limit))
		})
	}
}

func TestOpenSearchApplyTermsOrderToGroupAggNode(t *testing.T) {
	conn := &OpenSearchConnector{}

	t.Run("applies key and metric order recursively", func(t *testing.T) {
		node := map[string]any{
			"terms": map[string]any{"field": "country"},
			"aggs": map[string]any{
				"group_by_city": map[string]any{
					"terms": map[string]any{"field": "city"},
					"aggs": map[string]any{
						"total": map[string]any{"sum": map[string]any{"field": "amount"}},
					},
				},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			Aggregation: &interfaces.Aggregation{Alias: "total"},
			Sort: []*interfaces.SortField{
				{Field: "country", Direction: "DESC"},
				{Field: "total", Direction: "sideways"},
				{Field: "city", Direction: "asc"},
			},
		}

		conn.applyTermsOrderToGroupAggNode(node, params, "total")

		assert.Equal(t, []map[string]any{{"_key": "desc"}}, node["terms"].(map[string]any)["order"])
		child := node["aggs"].(map[string]any)["group_by_city"].(map[string]any)
		assert.Equal(t, []map[string]any{{"total": "asc"}, {"_key": "asc"}}, child["terms"].(map[string]any)["order"])
	})

	t.Run("skips having filter child", func(t *testing.T) {
		node := map[string]any{
			"terms": map[string]any{"field": "country"},
			"aggs": map[string]any{
				"having_filter": map[string]any{"bucket_selector": map[string]any{}},
			},
		}

		conn.applyTermsOrderToGroupAggNode(node, &interfaces.ResourceDataQueryParams{
			Sort: []*interfaces.SortField{{Field: "country", Direction: "asc"}},
		}, "total")

		assert.Equal(t, []map[string]any{{"_key": "asc"}}, node["terms"].(map[string]any)["order"])
	})
}

func TestOpenSearchBuildHavingBucketSelector(t *testing.T) {
	conn := &OpenSearchConnector{}
	tests := []struct {
		name       string
		having     *interfaces.HavingClause
		wantScript string
	}{
		{name: "eq", having: &interfaces.HavingClause{Operation: "==", Value: 10}, wantScript: "params.total == 10"},
		{name: "gte", having: &interfaces.HavingClause{Operation: ">=", Value: 10}, wantScript: "params.total >= 10"},
		{name: "in", having: &interfaces.HavingClause{Operation: "in", Value: []any{"a", 2}}, wantScript: "['a', 2].contains(params.total.toString())"},
		{name: "not in", having: &interfaces.HavingClause{Operation: "not_in", Value: []any{"a", 2}}, wantScript: "!['a', 2].contains(params.total.toString())"},
		{name: "range", having: &interfaces.HavingClause{Operation: "range", Value: []any{1, 5}}, wantScript: "params.total >= 1 && params.total <= 5"},
		{name: "out range", having: &interfaces.HavingClause{Operation: "out_range", Value: []any{1, 5}}, wantScript: "params.total < 1 || params.total > 5"},
		{name: "unsupported", having: &interfaces.HavingClause{Operation: "like", Value: "x"}, wantScript: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conn.buildHavingBucketSelector(tt.having, "total")

			selector := got["bucket_selector"].(map[string]any)
			assert.Equal(t, map[string]any{"total": "total"}, selector["buckets_path"])
			assert.Equal(t, tt.wantScript, selector["script"].(map[string]any)["source"])
		})
	}
}

func TestFormatInValuesForScript(t *testing.T) {
	t.Run("format in values for script", func(t *testing.T) {
		assert.Equal(t, "[]", formatInValuesForScript(nil))
		assert.Equal(t, "['a', 2, true]", formatInValuesForScript([]any{"a", 2, true}))
	})
}

func TestOpenSearchJSONQueryResults(t *testing.T) {
	for _, mode := range []string{"structured", "raw", "dsl"} {
		t.Run(mode, func(t *testing.T) {
			client, err := osclient.NewClient(osclient.Config{
				Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					body := `{"hits":{"total":{"value":1},"hits":[{"_id":"1","_source":{"object":{"n":9007199254740993},"array":[1,true],"text":"{incomplete","empty":"","nothing":null,"number":9007199254740993,"boolean":true,"null_text":"null","id":42,"vector":[1,2]}}]}}`
					if strings.HasSuffix(request.URL.Path, "/_mapping") {
						body = `{"events":{"mappings":{"properties":{"object":{"properties":{"n":{"type":"long"}}},"array":{"type":"nested"},"text":{"type":"object","enabled":false},"empty":{"type":"object","enabled":false},"nothing":{"type":"object"},"number":{"type":"object","enabled":false},"boolean":{"type":"object","enabled":false},"null_text":{"type":"object","enabled":false},"id":{"type":"long"},"vector":{"type":"knn_vector"}}}}}`
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			})
			require.NoError(t, err)
			c := &OpenSearchConnector{client: client, enabled: true}
			resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
				{Name: "object", Type: interfaces.DataType_Json}, {Name: "array", Type: interfaces.DataType_Json},
				{Name: "text", Type: interfaces.DataType_Json}, {Name: "empty", Type: interfaces.DataType_Json}, {Name: "nothing", Type: interfaces.DataType_Json}, {Name: "number", Type: interfaces.DataType_Json}, {Name: "boolean", Type: interfaces.DataType_Json}, {Name: "null_text", Type: interfaces.DataType_Json},
				{Name: "id", Type: interfaces.DataType_Integer}, {Name: "vector", Type: interfaces.DataType_Vector},
			}}
			var entries []map[string]any
			switch mode {
			case "structured":
				got, err := c.ExecuteQuery(context.Background(), "events", resource, &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}})
				require.NoError(t, err)
				entries = got.Entries
			case "raw":
				got, err := c.ExecuteRawQuery(context.Background(), "events", map[string]any{"query": map[string]any{"match_all": map[string]any{}}})
				require.NoError(t, err)
				entries = got.Entries
			case "dsl":
				got, err := c.ExecuteQueryWithDsl(context.Background(), "events", `{"query":{"match_all":{}}}`)
				require.NoError(t, err)
				entries = got.Entries
			}
			require.Len(t, entries, 1)
			assert.Equal(t, `{"n":9007199254740993}`, entries[0]["object"])
			assert.Equal(t, `[1,true]`, entries[0]["array"])
			assert.Equal(t, "{incomplete", entries[0]["text"])
			assert.Equal(t, "", entries[0]["empty"])
			assert.Nil(t, entries[0]["nothing"])
			assert.Equal(t, "9007199254740993", entries[0]["number"])
			assert.Equal(t, "true", entries[0]["boolean"])
			assert.Equal(t, "null", entries[0]["null_text"])
			assert.Equal(t, json.Number("42"), entries[0]["id"])
			assert.Equal(t, []any{json.Number("1"), json.Number("2")}, entries[0]["vector"])
		})
	}
}

func TestOpenSearchJSONQueryMappingFailure(t *testing.T) {
	for _, mode := range []string{"raw", "dsl"} {
		t.Run(mode, func(t *testing.T) {
			client, err := osclient.NewClient(osclient.Config{
				Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					status, body := http.StatusOK, `{"hits":{"total":{"value":1},"hits":[{"_source":{"payload":{"n":1}}}]}}`
					if strings.HasSuffix(request.URL.Path, "/_mapping") {
						status, body = http.StatusForbidden, `{}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			})
			require.NoError(t, err)
			c := &OpenSearchConnector{client: client, enabled: true}
			if mode == "raw" {
				got, err := c.ExecuteRawQuery(context.Background(), "events", map[string]any{"query": map[string]any{"match_all": map[string]any{}}})
				require.ErrorContains(t, err, "failed to read JSON field types")
				assert.Nil(t, got)
			} else {
				got, err := c.ExecuteQueryWithDsl(context.Background(), "events", `{"query":{"match_all":{}}}`)
				require.ErrorContains(t, err, "failed to read JSON field types")
				assert.Nil(t, got)
			}
		})
	}
}

func TestStringifyJSONQueryFields(t *testing.T) {
	t.Run("discovered paths traverse objects and arrays", func(t *testing.T) {
		fields := map[string]interfaces.IndexFieldMeta{}
		parseProperties("", map[string]Property{"outer": {Type: "object", Properties: map[string]Property{"payload": {Type: "nested"}, "vector": {Type: "knn_vector"}}}}, fields)
		types := map[string]string{}
		for name, field := range fields {
			types[name] = (&OpenSearchConnector{}).MapType(field.Type)
		}
		for _, array := range []bool{false, true} {
			child := map[string]any{"payload": []any{map[string]any{"n": json.Number("9007199254740993")}}, "vector": []any{json.Number("1"), json.Number("2")}}
			var outer any = child
			if array {
				outer = []any{child, nil, map[string]any{"payload": "{incomplete"}}
			}
			source := map[string]any{"outer": outer}
			require.NoError(t, stringifyJSONQueryFields(source, types))
			assert.Equal(t, `[{"n":9007199254740993}]`, child["payload"])
			assert.Equal(t, []any{json.Number("1"), json.Number("2")}, child["vector"])
			if array {
				assert.Equal(t, "{incomplete", outer.([]any)[2].(map[string]any)["payload"])
			}
		}
	})
	t.Run("parents are serialized before children", func(t *testing.T) {
		source := map[string]any{"parent": map[string]any{"child": map[string]any{"n": 1}}}
		require.NoError(t, stringifyJSONQueryFields(source, map[string]string{"parent": interfaces.DataType_Json, "parent.child": interfaces.DataType_Json}))
		assert.Equal(t, `{"child":{"n":1}}`, source["parent"])
	})
	t.Run("literal dotted keys and missing values", func(t *testing.T) {
		source := map[string]any{"outer.payload": map[string]any{"n": 1}, "outer": map[string]any{"payload": nil}, "empty": nil}
		require.NoError(t, stringifyJSONQueryFields(source, map[string]string{"outer.payload": interfaces.DataType_Json, "missing.child": interfaces.DataType_Json, "empty.child": interfaces.DataType_Json}))
		assert.Equal(t, `{"n":1}`, source["outer.payload"])
		assert.Nil(t, source["outer"].(map[string]any)["payload"])
		assert.NotContains(t, source, "missing")
		assert.Nil(t, source["empty"])
	})
}

func TestOpenSearchJSONAliasQueries(t *testing.T) {
	for _, mode := range []string{"raw", "dsl"} {
		for caseIndex, mapping := range []string{
			`{"events-000001":{"mappings":{"properties":{"payload":{"type":"object","enabled":false}}}}}`,
			`{"events-000001":{"mappings":{"properties":{"payload":{"type":"object","enabled":false}}}},"events-000002":{"mappings":{"properties":{"payload":{"type":"object","enabled":false}}}}}`,
			`{"events-000001":{"mappings":{"properties":{"payload":{"type":"object","enabled":false}}}},"events-000002":{"mappings":{"properties":{"payload":{"type":"keyword"}}}}}`,
		} {
			t.Run(mode+"/"+[]string{"single index alias", "multiple compatible indices", "conflicting types"}[caseIndex], func(t *testing.T) {
				conflict := strings.Contains(mapping, "keyword")
				client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true, Transport: countRowsTransport(func(r *http.Request) (*http.Response, error) {
					body := `{"hits":{"total":{"value":1},"hits":[{"_source":{"payload":{"n":9007199254740993}}}]}}`
					if strings.HasSuffix(r.URL.Path, "/_mapping") {
						body = mapping
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})})
				require.NoError(t, err)
				c := &OpenSearchConnector{client: client, enabled: true}
				var entries []map[string]any
				if mode == "raw" {
					got, err := c.ExecuteRawQuery(context.Background(), "events", map[string]any{"query": map[string]any{"match_all": map[string]any{}}})
					if conflict {
						require.ErrorContains(t, err, "conflicting mapping types")
						assert.Nil(t, got)
						return
					}
					require.NoError(t, err)
					entries = got.Entries
					assert.Equal(t, interfaces.DataType_Json, got.Columns[0].Type)
				} else {
					got, err := c.ExecuteQueryWithDsl(context.Background(), "events", `{"query":{"match_all":{}}}`)
					if conflict {
						require.ErrorContains(t, err, "conflicting mapping types")
						assert.Nil(t, got)
						return
					}
					require.NoError(t, err)
					entries = got.Entries
				}
				require.Len(t, entries, 1)
				assert.Equal(t, `{"n":9007199254740993}`, entries[0]["payload"])
			})
		}
	}
}
