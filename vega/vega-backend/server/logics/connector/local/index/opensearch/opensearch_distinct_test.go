// Copyright openbkn.ai. All rights reserved.
// Licensed under the OpenBKN License. See LICENSE in the repository root for details.

package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	osclient "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestOpenSearchConnectorDistinctAccuracy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow *bool
	}{
		{name: "default approximation permits ignore_above"},
		{name: "explicit approximation permits ignore_above", allow: new(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					assert.NotContains(t, request.URL.Path, "point_in_time")
					var body map[string]any
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					metric := body["aggs"].(map[string]any)["__value"].(map[string]any)["cardinality"]
					assert.Equal(t, map[string]any{"field": "id", "precision_threshold": float64(40000)}, metric)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"timed_out":false,"_shards":{"total":2,"successful":2,"failed":0},"aggregations":{"__value":{"value":15020}}}`))}, nil
				})})
			require.NoError(t, err)
			result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(context.Background(), "orders",
				&interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, OriginalType: "keyword", Attributes: map[string]any{"ignore_above": 5}}}},
				&interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: tc.allow}})
			require.NoError(t, err)
			assert.Equal(t, json.Number("15020"), result.Entries[0]["__value"])
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"is_approximate":true`)
			assert.Equal(t, 1, requests)
		})
	}
	t.Run("exact count paginates a single snapshot and preserves wide cursor keys", func(t *testing.T) {
		searches, closes := 0, 0
		client, err := osclient.NewClient(osclient.Config{
			Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
			Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
				var body []byte
				var err error
				if request.Body != nil {
					body, err = io.ReadAll(request.Body)
				}
				require.NoError(t, err)
				response := ""
				switch {
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/_search/point_in_time"):
					response = `{"pit_id":"snapshot","_shards":{"total":2,"successful":2,"failed":0}}`
				case request.Method == http.MethodDelete:
					closes++
					assert.Contains(t, string(body), "snapshot")
					response = `{"pits":[{"pit_id":"snapshot","successful":true}]}`
				case request.URL.Path == "/_search":
					assert.Contains(t, string(body), `"pit"`)
					assert.Contains(t, string(body), `"composite"`)
					assert.NotContains(t, string(body), "cardinality")
					searches++
					if searches == 1 {
						response = `{"timed_out":false,"_shards":{"total":2,"successful":2,"failed":0},"hits":{"total":{"value":8,"relation":"eq"}},"aggregations":{"__distinct":{"after_key":{"value":9007199254740993},"buckets":[{"key":{"value":null},"doc_count":2},{"key":{"value":9007199254740992},"doc_count":3},{"key":{"value":9007199254740993},"doc_count":3}]}}}`
					} else {
						assert.Contains(t, string(body), `"after":{"value":9007199254740993}`)
						response = `{"timed_out":false,"_shards":{"total":2,"successful":2,"failed":0},"aggregations":{"__distinct":{"buckets":[]}}}`
					}
				default:
					t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			}),
		})
		require.NoError(t, err)
		var aggregation interfaces.Aggregation
		require.NoError(t, json.Unmarshal([]byte(`{"property":"id","aggr":"count_distinct","allow_approximate":false}`), &aggregation))
		result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(context.Background(), "orders",
			&interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "long"}}},
			&interfaces.ResourceDataQueryParams{Aggregation: &aggregation, NeedTotal: true})
		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		assert.Equal(t, int64(2), result.Entries[0]["__value"])
		assert.Equal(t, int64(8), result.TotalCount)
		assert.Equal(t, 2, searches)
		assert.Equal(t, 1, closes)
	})
}

func TestExactDistinctRequestDoesNotExposeTransportDetails(t *testing.T) {
	client, err := osclient.NewClient(osclient.Config{
		Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
		Transport: countRowsTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("private connection details")
		}),
	})
	require.NoError(t, err)
	_, err = (&OpenSearchConnector{client: client}).distinctRequest(context.Background(), http.MethodPost, "/_search", nil)
	require.EqualError(t, err, "exact distinct request failed")
}

func TestOpenSearchConnectorExecuteExactDistinct(t *testing.T) {
	const complete = `"timed_out":false,"_shards":{"total":2,"successful":2,"failed":0}`
	for _, tc := range []struct {
		name      string
		pages     []string
		groups    []*interfaces.GroupByItem
		having    *interfaces.HavingClause
		sort      []*interfaces.SortField
		paging    interfaces.PagingRequest
		want      []map[string]any
		wantError string
	}{
		{name: "empty set", pages: []string{`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{{"__value": int64(0)}}},
		{name: "all null", pages: []string{`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":null},"doc_count":7}],"after_key":{"value":null}}}}`, `{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{{"__value": int64(0)}}},
		{name: "groups include all-null group", groups: []*interfaces.GroupByItem{{Property: "status"}}, pages: []string{
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":"empty","value":null},"doc_count":2},{"key":{"0":"ready","value":"a"},"doc_count":3}],"after_key":{"0":"ready","value":"a"}}}}`,
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":"ready","value":"b"},"doc_count":5}],"after_key":{"0":"ready","value":"b"}}}}`,
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{{"status": "empty", "__value": int64(0)}, {"status": "ready", "__value": int64(2)}}},
		{name: "calendar gap does not create a zero-count group", groups: []*interfaces.GroupByItem{{Property: "day", CalendarInterval: "day"}},
			having: &interfaces.HavingClause{Field: "__value", Operation: "==", Value: 0}, pages: []string{
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":1767225600000,"value":"a"},"doc_count":1},{"key":{"0":1767398400000,"value":"b"},"doc_count":1}],"after_key":{"0":1767398400000,"value":"b"}}}}`,
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{}},
		{name: "calendar all-null group matches zero-count having", groups: []*interfaces.GroupByItem{{Property: "day", CalendarInterval: "day"}},
			having: &interfaces.HavingClause{Field: "__value", Operation: "==", Value: 0}, pages: []string{
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":1767225600000,"value":"a"},"doc_count":1},{"key":{"0":1767398400000,"value":null},"doc_count":2}],"after_key":{"0":1767398400000,"value":null}}}}`,
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{{"day": json.Number("1767398400000"), "__value": int64(0)}}},
		{name: "calendar pagination uses existing groups", groups: []*interfaces.GroupByItem{{Property: "day", CalendarInterval: "day"}},
			sort: []*interfaces.SortField{{Field: "day", Direction: "desc"}}, paging: interfaces.PagingRequest{Offset: 1, Limit: 1}, pages: []string{
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":1767225600000,"value":1},"doc_count":1},{"key":{"0":1767398400000,"value":2},"doc_count":1}],"after_key":{"0":1767398400000,"value":2}}}}`,
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{{"day": json.Number("1767225600000"), "__value": int64(1)}}},
		{name: "calendar gap does not match zero document having", groups: []*interfaces.GroupByItem{{Property: "day", CalendarInterval: "day"}},
			having: &interfaces.HavingClause{Field: "count(*)", Operation: "==", Value: 0}, pages: []string{
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":1767225600000},"doc_count":1},{"key":{"0":1767398400000},"doc_count":1}],"after_key":{"0":1767398400000}}}}`,
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`,
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":1767225600000,"value":1},"doc_count":1},{"key":{"0":1767398400000,"value":2},"doc_count":1}],"after_key":{"0":1767398400000,"value":2}}}}`,
				`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{}},
		{name: "calendar parents contain only existing dates", groups: []*interfaces.GroupByItem{{Property: "status"}, {Property: "day", CalendarInterval: "day"}}, pages: []string{
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":"a","1":1767225600000,"value":1},"doc_count":1},{"key":{"0":"a","1":1767398400000,"value":2},"doc_count":1},{"key":{"0":"b","1":1767484800000,"value":3},"doc_count":1}],"after_key":{"0":"b","1":1767484800000,"value":3}}}}`,
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{
			{"status": "a", "day": json.Number("1767225600000"), "__value": int64(1)},
			{"status": "a", "day": json.Number("1767398400000"), "__value": int64(1)},
			{"status": "b", "day": json.Number("1767484800000"), "__value": int64(1)}}},
		{name: "having uses complete count", having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: 1}, pages: []string{
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":"a"},"doc_count":3}],"after_key":{"value":"a"}}}}`,
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":"b"},"doc_count":5}],"after_key":{"value":"b"}}}}`,
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`}, want: []map[string]any{{"__value": int64(2)}}},
		{name: "second-page failure publishes no prefix", pages: []string{
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":1},"doc_count":3}],"after_key":{"value":1}}}}`,
			`{"timed_out":true}`}, wantError: "budget exceeded"},
		{name: "failed shard", pages: []string{`{"timed_out":false,"_shards":{"total":2,"successful":1,"failed":1}}`}, wantError: "all shards"},
		{name: "missing shard metadata", pages: []string{`{"aggregations":{"__distinct":{"buckets":[]}}}`}, wantError: "shard completion"},
		{name: "missing buckets", pages: []string{`{` + complete + `,"aggregations":{}}`}, wantError: "composite buckets"},
		{name: "missing continuation", pages: []string{`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":1},"doc_count":3}]}}}`}, wantError: "continuation"},
		{name: "repeated continuation", pages: []string{
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":1},"doc_count":3}],"after_key":{"value":1}}}}`,
			`{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"value":1},"doc_count":3}],"after_key":{"value":1}}}}`}, wantError: "did not advance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searches, closes := 0, 0
			client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					response := `{"pit_id":"snapshot","_shards":{"total":2,"successful":2,"failed":0}}`
					if request.Method == http.MethodDelete {
						closes++
						response = `{"pits":[{"pit_id":"snapshot","successful":true}]}`
					} else if request.URL.Path == "/_search" {
						require.Less(t, searches, len(tc.pages))
						response = tc.pages[searches]
						searches++
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})})
			require.NoError(t, err)
			allow := false
			result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(context.Background(), "orders",
				&interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, OriginalType: "keyword"}, {Name: "status", Type: interfaces.DataType_String, OriginalType: "keyword"}, {Name: "day", Type: interfaces.DataType_Datetime, OriginalType: "date"}}},
				&interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: &allow}, GroupBy: tc.groups, Having: tc.having, Sort: tc.sort, Paging: tc.paging})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result.Entries)
				assert.False(t, result.IsApproximate)
			}
			assert.Equal(t, 1, closes)
			assert.Empty(t, exactDistinctSlots)
		})
	}
}

func TestOpenSearchConnectorExactDistinctBudgets(t *testing.T) {
	t.Run("cancelled search closes its PIT with an independent context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closes := 0
		client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true, Transport: countRowsTransport(func(req *http.Request) (*http.Response, error) {
			body := `{"pit_id":"snapshot","_shards":{"total":1,"successful":1,"failed":0}}`
			if req.URL.Path == "/_search" {
				cancel()
				return nil, ctx.Err()
			}
			if req.Method == http.MethodDelete {
				closes++
				assert.NoError(t, req.Context().Err())
				body = `{"pits":[{"pit_id":"snapshot","successful":true}]}`
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		require.NoError(t, err)
		result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(ctx, "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "long"}}}, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: new(false)}})
		assert.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, result)
		assert.Equal(t, 1, closes)
		assert.Empty(t, exactDistinctSlots)
	})
	for _, tc := range []struct{ name, budget string }{{"oversized response", "exact_distinct_response_memory"}, {"group memory", "exact_distinct_group_memory"}} {
		t.Run(tc.name, func(t *testing.T) {
			searches, closes := 0, 0
			client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true, Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
				response := `{"pit_id":"snapshot","_shards":{"total":1,"successful":1,"failed":0}}`
				if request.Method == http.MethodDelete {
					closes++
					response = `{"pits":[{"pit_id":"snapshot","successful":true}]}`
				} else if request.URL.Path == "/_search" {
					searches++
					if tc.budget == "exact_distinct_response_memory" {
						response = strings.Repeat(" ", exactDistinctResponseBytes+1)
					} else {
						key := map[string]any{"0": strconv.Itoa(searches) + strings.Repeat("x", 2<<20), "value": 1}
						encoded, err := json.Marshal(map[string]any{"timed_out": false, "_shards": map[string]any{"total": 1, "successful": 1, "failed": 0}, "aggregations": map[string]any{"__distinct": map[string]any{"buckets": []any{map[string]any{"key": key, "doc_count": 1}}, "after_key": key}}})
						require.NoError(t, err)
						response = string(encoded)
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})})
			require.NoError(t, err)
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: new(false)}}
			if tc.budget == "exact_distinct_group_memory" {
				params.GroupBy = []*interfaces.GroupByItem{{Property: "status"}}
			}
			result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(context.Background(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "long"}, {Name: "status", Type: interfaces.DataType_String, OriginalType: "keyword"}, {Name: "day", Type: interfaces.DataType_Datetime, OriginalType: "date"}}}, params)
			var budget *interfaces.QueryExecutionBudgetError
			require.ErrorAs(t, err, &budget)
			assert.Equal(t, tc.budget, budget.Budget)
			assert.Nil(t, result)
			assert.Equal(t, 1, closes)
			assert.Empty(t, exactDistinctSlots)
		})
	}
	t.Run("cancel while all slots are occupied", func(t *testing.T) {
		for i := 0; i < cap(exactDistinctSlots); i++ {
			exactDistinctSlots <- struct{}{}
		}
		defer func() {
			for i := 0; i < cap(exactDistinctSlots); i++ {
				<-exactDistinctSlots
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := (&OpenSearchConnector{}).executeExactDistinct(ctx, "orders", &interfaces.Resource{}, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, result)
		assert.True(t, errors.Is(err, context.Canceled))
	})
}

func TestOpenSearchConnectorExactDistinctUsesSchema(t *testing.T) {
	for _, tc := range []struct {
		name, native    string
		logicalType     string
		attributes      map[string]any
		wantError       bool
		wantEngineError bool
	}{
		{name: "keyword", native: "keyword"},
		{name: "scaled float", native: "scaled_float", logicalType: interfaces.DataType_Float, attributes: map[string]any{"scaling_factor": 100}},
		{name: "float", native: "float", logicalType: interfaces.DataType_Float},
		{name: "half float", native: "half_float", logicalType: interfaces.DataType_Float},
		{name: "ignore above", native: "keyword", attributes: map[string]any{"ignore_above": 5}, wantError: true},
		{name: "normalizer", native: "keyword", attributes: map[string]any{"normalizer": "lowercase"}, wantError: true},
		{name: "null substitute", native: "keyword", attributes: map[string]any{"null_value": "NULL"}, wantError: true},
		{name: "malformed values", native: "keyword", attributes: map[string]any{"ignore_malformed": true}, wantError: true},
		{name: "disabled doc values is reported by the engine", native: "keyword", attributes: map[string]any{"doc_values": false}, wantEngineError: true},
		{name: "logical type is sufficient without native metadata"},
		{name: "integer without native metadata", logicalType: interfaces.DataType_Integer},
		{name: "decimal without native metadata", logicalType: interfaces.DataType_Decimal},
		{name: "datetime without native metadata", logicalType: interfaces.DataType_Datetime},
		{name: "boolean without native metadata", logicalType: interfaces.DataType_Boolean},
		{name: "JSON is not a scalar aggregation field", logicalType: interfaces.DataType_Json, wantError: true},
		{name: "vector is not a scalar aggregation field", logicalType: interfaces.DataType_Vector, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, closes := 0, 0
			client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					require.NotEqual(t, http.MethodGet, req.Method, "exact queries must trust schema without fetching mapping")
					body := `{"pit_id":"snapshot","_shards":{"total":1,"successful":1,"failed":0}}`
					if req.URL.Path == "/_search" {
						if tc.wantEngineError {
							return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"unsupported aggregation"}`))}, nil
						}
						body = `{"_shards":{"total":1,"successful":1,"failed":0},"aggregations":{"__distinct":{"buckets":[]}}}`
					}
					if req.Method == http.MethodDelete {
						closes++
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})})
			require.NoError(t, err)
			logicalType := tc.logicalType
			if logicalType == "" {
				logicalType = interfaces.DataType_String
			}
			result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(context.Background(), "orders",
				&interfaces.Resource{Category: interfaces.ResourceCategoryIndex, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: logicalType, OriginalType: tc.native, Attributes: tc.attributes}}},
				&interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: new(false)}})
			if tc.wantError {
				var condition *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &condition)
				assert.Nil(t, result)
				assert.Zero(t, requests, "schema rejection must precede PIT creation")
				if tc.attributes["ignore_above"] != nil {
					assert.ErrorContains(t, err, "cannot guarantee exact count_distinct")
					assert.ErrorContains(t, err, "ignore_above may omit values")
				}

			} else if tc.wantEngineError {
				require.ErrorContains(t, err, "HTTP 400")
				assert.Nil(t, result)
				assert.Equal(t, 3, requests)
				assert.Equal(t, 1, closes)
			} else {
				require.NoError(t, err)
				assert.Equal(t, []map[string]any{{"__value": int64(0)}}, result.Entries)
				assert.False(t, result.IsApproximate)
				assert.Equal(t, 3, requests)
				assert.Equal(t, 1, closes)
			}
		})
	}
}

func TestValidateExactDistinctQuery(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		category    string
		groups      []*interfaces.GroupByItem
		schema      []*interfaces.Property
		wantError   bool
	}{
		{name: "dotted field", field: "profile.name", schema: []*interfaces.Property{{Name: "profile.name", Type: interfaces.DataType_String, OriginalType: "keyword"}}},
		{name: "keyword subfield", field: "body", schema: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text, OriginalType: "text", Attributes: map[string]any{"analyzer": "standard"}, Features: []interfaces.PropertyFeature{{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword}}}}},
		{name: "lossy keyword subfield", field: "body", wantError: true, schema: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text, OriginalType: "text", Features: []interfaces.PropertyFeature{{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}}}}}},
		{name: "alias to nested field", field: "id", schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "alias", Attributes: map[string]any{"path": "profile.id"}}, {Name: "profile.id", Type: interfaces.DataType_Integer, OriginalType: "long"}}},
		{name: "alias to keyword subfield", field: "body_alias", schema: []*interfaces.Property{{Name: "body_alias", Type: interfaces.DataType_Text, OriginalType: "alias", Attributes: map[string]any{"path": "body"}}, {Name: "body", Type: interfaces.DataType_Text, OriginalType: "text", Features: []interfaces.PropertyFeature{{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword}}}}},
		{name: "lossy alias target", field: "id", wantError: true, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, OriginalType: "alias", Attributes: map[string]any{"path": "target"}}, {Name: "target", Type: interfaces.DataType_String, OriginalType: "keyword", Attributes: map[string]any{"ignore_above": 5}}}},
		{name: "missing alias target", field: "id", wantError: true, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, OriginalType: "alias", Attributes: map[string]any{"path": "missing"}}}},
		{name: "nested parent", field: "profile.id", wantError: true, schema: []*interfaces.Property{{Name: "profile", Type: interfaces.DataType_Json, OriginalType: "nested"}, {Name: "profile.id", Type: interfaces.DataType_Integer, OriginalType: "long"}}},
		{name: "disabled parent", field: "profile.id", wantError: true, schema: []*interfaces.Property{{Name: "profile", Type: interfaces.DataType_Json, OriginalType: "object", Attributes: map[string]any{"enabled": false}}, {Name: "profile.id", Type: interfaces.DataType_Integer, OriginalType: "long"}}},
		{name: "managed keyword config is checked without native attributes", field: "id", category: interfaces.ResourceCategoryDataset, wantError: true, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 5}}}}}},
		{name: "managed keyword feature is rejected without inspecting config", field: "id", category: interfaces.ResourceCategoryDataset, wantError: true, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}}}}},
		{name: "managed keyword empty config does not bypass the limit policy", field: "id", category: interfaces.ResourceCategoryDataset, wantError: true, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{}}}}}},
		{name: "source attributes are not index settings", field: "id", category: interfaces.ResourceCategoryTable, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, OriginalType: "VARCHAR", Attributes: map[string]any{"ignore_above": 5, "null_value": "source default"}}}},
		{name: "external keyword root ignores unused subfield config", field: "id", schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureName: "unused", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 5}}}}}},
		{name: "group field config is checked", field: "id", groups: []*interfaces.GroupByItem{{Property: "group"}}, wantError: true, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "group", Type: interfaces.DataType_String, Attributes: map[string]any{"ignore_above": 5}}}},
		{name: "non-index attributes are not mapping configuration", field: "id", category: interfaces.ResourceCategoryFileset, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Attributes: map[string]any{"ignore_above": 5, "null_value": "source default"}}}},
		{name: "non-index native type is not an index alias", field: "id", category: interfaces.ResourceCategoryTable, schema: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, OriginalType: "alias"}}},
		{name: "non-index parent metadata is not a nested mapping", field: "payload.id", category: interfaces.ResourceCategoryDataset, schema: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json, OriginalType: "nested", Attributes: map[string]any{"enabled": false}}, {Name: "payload.id", Type: interfaces.DataType_Integer}}},
		{name: "table native type does not describe index precision", field: "created", category: interfaces.ResourceCategoryTable, schema: []*interfaces.Property{{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"}}},
		{name: "dataset native type does not describe index precision", field: "created", category: interfaces.ResourceCategoryDataset, schema: []*interfaces.Property{{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"}}},
		{name: "missing field", field: "missing", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			category := tc.category
			if category == "" {
				category = interfaces.ResourceCategoryIndex
			}
			err := validateExactDistinctQuery(&interfaces.Resource{Category: category, SchemaDefinition: tc.schema}, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: tc.field, Aggr: "count_distinct", AllowApproximate: new(false)}, GroupBy: tc.groups})
			if tc.wantError {
				var condition *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &condition)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOpenSearchConnectorExactDistinctDateNanos(t *testing.T) {
	const complete = `"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0}`
	for _, tc := range []struct {
		name, measure, group, interval string
		allow                          *bool
		wantError                      bool
	}{
		{name: "exact nanos measure", measure: "created", allow: new(false), wantError: true},
		{name: "exact nanos measure alias", measure: "created_alias", allow: new(false), wantError: true},
		{name: "exact nanos terms group", measure: "id", group: "created", allow: new(false), wantError: true},
		{name: "exact nanos terms group alias", measure: "id", group: "created_alias", allow: new(false), wantError: true},
		{name: "exact day group", measure: "id", group: "created", interval: "day", allow: new(false)},
		{name: "exact month group alias", measure: "id", group: "created_alias", interval: "month", allow: new(false)},
		{name: "calendar group does not exempt the nanos measure", measure: "created", group: "created", interval: "day", allow: new(false), wantError: true},
		{name: "default approximation permits nanos measure", measure: "created"},
		{name: "explicit approximation permits nanos measure alias", measure: "created_alias", allow: new(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, searches, closes := 0, 0, 0
			client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
				Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					response := `{` + complete + `,"pit_id":"snapshot"}`
					switch {
					case request.Method == http.MethodDelete:
						closes++
						response = `{"pits":[{"pit_id":"snapshot","successful":true}]}`
					case request.URL.Path == "/_search":
						searches++
						response = `{` + complete + `,"aggregations":{"__distinct":{"buckets":[]}}}`
						if tc.group != "" && searches == 1 {
							var body map[string]any
							require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
							composite := body["aggs"].(map[string]any)["__distinct"].(map[string]any)["composite"].(map[string]any)
							group := composite["sources"].([]any)[0].(map[string]any)["0"].(map[string]any)
							if tc.interval != "" {
								assert.Equal(t, map[string]any{"field": "created", "calendar_interval": tc.interval, "missing_bucket": true}, group["date_histogram"])
							}
							response = `{` + complete + `,"aggregations":{"__distinct":{"buckets":[{"key":{"0":1767225600000,"value":1},"doc_count":1}],"after_key":{"0":1767225600000,"value":1}}}}`
						}
					case request.URL.Path == "/orders/_search":
						var body map[string]any
						require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
						assert.Equal(t, map[string]any{"field": "created", "precision_threshold": float64(40000)}, body["aggs"].(map[string]any)["__value"].(map[string]any)["cardinality"])
						response = `{` + complete + `,"aggregations":{"__value":{"value":2}}}`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})})
			require.NoError(t, err)
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: tc.measure, Aggr: "count_distinct", AllowApproximate: tc.allow}}
			if tc.group != "" {
				params.GroupBy = []*interfaces.GroupByItem{{Property: tc.group, CalendarInterval: tc.interval}}
			}
			result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(context.Background(), "orders", &interfaces.Resource{
				Category: interfaces.ResourceCategoryIndex, SchemaDefinition: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "long"},
					{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"},
					{Name: "created_alias", Type: interfaces.DataType_Datetime, OriginalType: "alias", Attributes: map[string]any{"path": "created"}},
				}}, params)
			if tc.wantError {
				var condition *interfaces.ConditionBuildError
				assert.ErrorAs(t, err, &condition)
				assert.ErrorContains(t, err, "date_nanos aggregation uses millisecond precision")
				assert.Nil(t, result)
				assert.Zero(t, requests, "reject unsupported precision before creating a PIT")
				assert.Zero(t, closes)
			} else {
				require.NoError(t, err)
				if tc.allow == nil || *tc.allow {
					assert.Equal(t, []map[string]any{{"__value": json.Number("2")}}, result.Entries)
					assert.True(t, result.IsApproximate)
					assert.Equal(t, 1, requests)
					assert.Zero(t, closes)
				} else {
					assert.Equal(t, []map[string]any{{tc.group: json.Number("1767225600000"), "__value": int64(1)}}, result.Entries)
					assert.False(t, result.IsApproximate)
					assert.Equal(t, 4, requests)
					assert.Equal(t, 1, closes)
				}
			}
		})
	}
}
