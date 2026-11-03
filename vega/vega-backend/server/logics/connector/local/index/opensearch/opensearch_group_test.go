// Copyright openbkn.ai. All rights reserved.
// Licensed under the OpenBKN License. See LICENSE in the repository root for details.

package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	osclient "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// groupedQueryConnector 模拟同一 PIT 上的多页响应，同时检查续页和资源释放。
func groupedQueryConnector(t *testing.T, pages []string, inspect func(map[string]any)) (*OpenSearchConnector, *int, *bool) {
	t.Helper()
	searches, closed := 0, false
	client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true,
		Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
			var body map[string]any
			if request.Body != nil {
				decoder := json.NewDecoder(request.Body)
				decoder.UseNumber()
				require.NoError(t, decoder.Decode(&body))
			}
			response := `{"_shards":{"total":1,"successful":1,"failed":0},"pit_id":"group-pit"}`
			switch {
			case request.Method == http.MethodDelete:
				assert.Equal(t, "/_search/point_in_time", request.URL.Path)
				assert.Equal(t, []any{"group-pit"}, body["pit_id"])
				assert.NoError(t, request.Context().Err(), "PIT cleanup must survive cancellation")
				closed = true
				response = `{}`
			case strings.HasSuffix(request.URL.Path, "/_search/point_in_time"):
				assert.Equal(t, "false", request.URL.Query().Get("allow_partial_pit_creation"))
			default:
				require.Equal(t, "/_search", request.URL.Path)
				assert.Equal(t, "false", request.URL.Query().Get("allow_partial_search_results"))
				assert.Equal(t, map[string]any{"id": "group-pit", "keep_alive": "1m"}, body["pit"])
				require.Less(t, searches, len(pages), "unexpected extra page")
				if inspect != nil {
					inspect(body)
				}
				response = pages[searches]
				searches++
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
		})})
	require.NoError(t, err)
	return &OpenSearchConnector{client: client}, &searches, &closed
}

func groupedPage(buckets, after string) string {
	continuation := ""
	if after != "" {
		continuation = `,"after_key":` + after
	}
	return `{"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":6,"relation":"eq"}},"aggregations":{"__vega_groups":{"buckets":` + buckets + continuation + `}}}`
}

func TestOpenSearchConnectorExecuteGroupedQuery(t *testing.T) {
	t.Run("implicit count sorts NULL groups before applying offset and limit", func(t *testing.T) {
		for _, direction := range []string{interfaces.ASC_DIRECTION, interfaces.DESC_DIRECTION} {
			for _, offset := range []int{0, 1} {
				conn, _, closed := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":null},"doc_count":3},{"key":{"0":1},"doc_count":1},{"key":{"0":2},"doc_count":2}]`, `{"0":2}`), groupedPage(`[]`, "")}, nil)
				params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}, Sort: []*interfaces.SortField{{Field: "__value", Direction: direction}}, Paging: interfaces.PagingRequest{Offset: offset, Limit: 1}}
				result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}}}, params)
				require.NoError(t, err)
				want := map[string]any{"key": nil, "__value": int64(3)}
				if direction == interfaces.ASC_DIRECTION {
					want = map[string]any{"key": json.Number("1"), "__value": int64(1)}
				}
				if offset == 1 {
					want = map[string]any{"key": json.Number("2"), "__value": int64(2)}
				}
				assert.Equal(t, []map[string]any{want}, result.Entries)
				assert.True(t, *closed)
			}
		}
	})

	t.Run("decimal normalization precedes HAVING sorting and limit", func(t *testing.T) {
		for _, operation := range []string{"sum", "min", "max", "avg"} {
			for _, havingField := range []string{"", "__value", "count(*)"} {
				t.Run(operation+"/having="+havingField, func(t *testing.T) {
					conn, _, _ := groupedQueryConnector(t, []string{
						groupedPage(`[{"key":{"0":"A"},"doc_count":1,"__vega_metric":{"value":0.3},"__vega_nonnull":{"value":1}},{"key":{"0":"B"},"doc_count":2,"__vega_metric":{"value":0.30000000000000004},"__vega_nonnull":{"value":2}}]`, `{"0":"B"}`), groupedPage(`[]`, "")}, nil)
					params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: operation, Alias: "total"}, Sort: []*interfaces.SortField{{Field: "total", Direction: "desc"}, {Field: "key", Direction: "asc"}}, Paging: interfaces.PagingRequest{Limit: 1}}
					if havingField != "" {
						params.Having = &interfaces.HavingClause{Field: havingField, Operation: ">", Value: 0}
					}
					result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_String}, {Name: "amount", Type: interfaces.DataType_Decimal, Attributes: map[string]any{"precision": 12, "scale": 2}}}}, params)
					require.NoError(t, err)
					want := map[string]any{"key": "A", "total": json.Number("0.3")}
					if operation == "avg" {
						want = map[string]any{"key": "B", "total": json.Number("0.30000000000000004")}
					}
					assert.Equal(t, []map[string]any{want}, result.Entries)
				})
			}
		}
	})

	t.Run("six-row SQL fixture retains NULL and all-null measure groups", func(t *testing.T) {
		for _, operation := range []string{"count", "count_distinct", "sum", "avg", "min", "max"} {
			modes := []*bool{nil}
			if operation == "count_distinct" {
				allow := true
				modes = append(modes, &allow)
			}
			for _, allow := range modes {
				t.Run(fmt.Sprintf("%s/explicit=%v", operation, allow != nil), func(t *testing.T) {
					values := map[string][]string{"count": {"2", "2", "0"}, "count_distinct": {"2", "1", "0"}, "sum": {"30", "10", "0"}, "avg": {"15", "5", "null"}, "min": {"10", "5", "null"}, "max": {"20", "5", "null"}}[operation]
					pages := []string{
						groupedPage(fmt.Sprintf(`[{"key":{"0":null},"doc_count":3,"__vega_metric":{"value":%s},"__vega_nonnull":{"value":2}}]`, values[0]), `{"0":null}`),
						groupedPage(fmt.Sprintf(`[{"key":{"0":1},"doc_count":2,"__vega_metric":{"value":%s},"__vega_nonnull":{"value":2}},{"key":{"0":2},"doc_count":1,"__vega_metric":{"value":%s},"__vega_nonnull":{"value":0}}]`, values[1], values[2]), `{"0":2}`), groupedPage(`[]`, ""),
					}
					page := 0
					conn, searches, closed := groupedQueryConnector(t, pages, func(body map[string]any) {
						groups := body["aggs"].(map[string]any)["__vega_groups"].(map[string]any)
						composite := groups["composite"].(map[string]any)
						assert.Equal(t, json.Number("1000"), composite["size"])
						source := composite["sources"].([]any)[0].(map[string]any)["0"].(map[string]any)["terms"].(map[string]any)
						assert.Equal(t, map[string]any{"field": "group_key", "missing_bucket": true}, source)
						assert.NotContains(t, groups["aggs"].(map[string]any), "having_filter")
						children := groups["aggs"].(map[string]any)
						metricKind := operation
						if operation == "count" {
							metricKind = "value_count"
						}
						if operation == "count_distinct" {
							metricKind = "cardinality"
						}
						definition := map[string]any{"field": "measure_value"}
						if operation == "count_distinct" {
							definition["precision_threshold"] = json.Number("40000")
						}
						assert.Equal(t, map[string]any{metricKind: definition}, children["__vega_metric"])
						if operation == "sum" {
							assert.Equal(t, map[string]any{"value_count": map[string]any{"field": "measure_value"}}, children["__vega_nonnull"])
						}

						if page == 0 {
							assert.NotContains(t, composite, "after")
							assert.Equal(t, true, body["track_total_hits"])
						}
						if page == 1 {
							assert.Equal(t, map[string]any{"0": nil}, composite["after"])
						}
						page++
					})
					result, err := conn.ExecuteQuery(context.Background(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "group_key", Type: interfaces.DataType_Integer}, {Name: "measure_value", Type: interfaces.DataType_Integer}}}, &interfaces.ResourceDataQueryParams{NeedTotal: true, GroupBy: []*interfaces.GroupByItem{{Property: "group_key"}}, Aggregation: &interfaces.Aggregation{Property: "measure_value", Aggr: operation, Alias: "value", AllowApproximate: allow}, Paging: interfaces.PagingRequest{Limit: 100}})
					require.NoError(t, err)
					want := []map[string]any{{"group_key": nil, "value": json.Number(values[0])}, {"group_key": json.Number("1"), "value": json.Number(values[1])}, {"group_key": json.Number("2"), "value": json.Number(values[2])}}
					if operation != "count" && operation != "count_distinct" {
						want[2]["value"] = nil
					}
					assert.Equal(t, want, result.Entries)
					assert.Equal(t, int64(6), result.TotalCount)
					assert.Equal(t, operation == "count_distinct", result.IsApproximate)
					assert.Equal(t, 3, *searches)
					assert.True(t, *closed)
				})
			}
		}
	})
	t.Run("six-row fixture exact distinct retains the same groups", func(t *testing.T) {
		first := strings.ReplaceAll(groupedPage(`[
   {"key":{"0":null,"value":null},"doc_count":1},
   {"key":{"0":null,"value":10},"doc_count":1},
   {"key":{"0":null,"value":20},"doc_count":1},
   {"key":{"0":1,"value":5},"doc_count":2},
   {"key":{"0":2,"value":null},"doc_count":1}
  ]`, `{"0":2,"value":null}`), "__vega_groups", "__distinct")
		empty := strings.ReplaceAll(groupedPage(`[]`, ""), "__vega_groups", "__distinct")
		conn, _, closed := groupedQueryConnector(t, []string{first, empty}, func(body map[string]any) {
			sources := body["aggs"].(map[string]any)["__distinct"].(map[string]any)["composite"].(map[string]any)["sources"].([]any)
			assert.Equal(t, map[string]any{"terms": map[string]any{"field": "group_key", "missing_bucket": true}}, sources[0].(map[string]any)["0"])
		})
		result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{Category: interfaces.ResourceCategoryIndex, SchemaDefinition: []*interfaces.Property{{Name: "group_key", Type: interfaces.DataType_Integer, OriginalType: "long"}, {Name: "measure_value", Type: interfaces.DataType_Integer, OriginalType: "long"}}}, &interfaces.ResourceDataQueryParams{NeedTotal: true, GroupBy: []*interfaces.GroupByItem{{Property: "group_key"}}, Aggregation: &interfaces.Aggregation{Property: "measure_value", Aggr: "count_distinct", Alias: "value", AllowApproximate: new(false)}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"group_key": nil, "value": int64(2)}, {"group_key": json.Number("1"), "value": int64(1)}, {"group_key": json.Number("2"), "value": int64(0)}}, result.Entries)
		assert.False(t, result.IsApproximate)
		assert.Equal(t, int64(6), result.TotalCount)
		assert.True(t, *closed)
	})

	t.Run("multiple dimensions preserve NULL combinations and real values", func(t *testing.T) {
		conn, _, closed := groupedQueryConnector(t, []string{groupedPage(`[
   {"key":{"0":null,"1":null},"doc_count":2},
   {"key":{"0":null,"1":-1},"doc_count":1},
   {"key":{"0":null,"1":0},"doc_count":1},
   {"key":{"0":"","1":0},"doc_count":1},
   {"key":{"0":"NULL","1":null},"doc_count":1}
  ]`, `{"0":"NULL","1":null}`), groupedPage(`[]`, "")}, nil)
		result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "text", Type: interfaces.DataType_String}, {Name: "number", Type: interfaces.DataType_Integer}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "text"}, {Property: "number"}}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"text": nil, "number": nil}, {"text": nil, "number": json.Number("-1")}, {"text": nil, "number": json.Number("0")}, {"text": "", "number": json.Number("0")}, {"text": "NULL", "number": nil}}, result.Entries)
		assert.True(t, *closed)
	})
	t.Run("HAVING precedes global metric sorting and offset limit", func(t *testing.T) {
		for _, sortAlias := range []string{"total", "__value"} {
			conn, searches, _ := groupedQueryConnector(t, []string{
				groupedPage(`[{"key":{"0":null},"doc_count":3,"__vega_metric":{"value":0},"__vega_nonnull":{"value":0}},{"key":{"0":1},"doc_count":1,"__vega_metric":{"value":10},"__vega_nonnull":{"value":1}}]`, `{"0":1}`),
				groupedPage(`[{"key":{"0":2},"doc_count":1,"__vega_metric":{"value":30},"__vega_nonnull":{"value":1}},{"key":{"0":3},"doc_count":1,"__vega_metric":{"value":20},"__vega_nonnull":{"value":1}}]`, `{"0":3}`), groupedPage(`[]`, "")}, nil)
			params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum", Alias: "total"}, Having: &interfaces.HavingClause{Field: "total", Operation: ">", Value: 5}, Sort: []*interfaces.SortField{{Field: sortAlias, Direction: "desc"}}, Paging: interfaces.PagingRequest{Offset: 1, Limit: 1}}
			result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Integer}}}, params)
			require.NoError(t, err)
			assert.Equal(t, []map[string]any{{"key": json.Number("3"), "total": json.Number("20")}}, result.Entries)
			assert.Equal(t, 3, *searches)
		}
	})
	t.Run("all NULL key remains a group and count-star HAVING uses documents", func(t *testing.T) {
		for _, metric := range []bool{false, true} {
			conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":null},"doc_count":6,"__vega_metric":{"value":2}}]`, `{"0":null}`), groupedPage(`[]`, "")}, nil)
			params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 3}}
			if metric {
				params.Aggregation = &interfaces.Aggregation{Property: "amount", Aggr: "count", Alias: "n"}
			}
			result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Integer}}}, params)
			require.NoError(t, err)
			want := map[string]any{"key": nil, "__value": int64(6)}
			if metric {
				want = map[string]any{"key": nil, "n": json.Number("2")}
			}
			assert.Equal(t, []map[string]any{want}, result.Entries)
		}
	})
	t.Run("NULL metric fails comparisons and zero sum survives", func(t *testing.T) {
		for _, op := range []string{"==", "!=", ">", ">=", "<", "<=", "in", "not_in", "range", "out_range"} {
			threshold := any(0)
			if op == "in" || op == "not_in" {
				threshold = []any{0}
			}
			if op == "range" || op == "out_range" {
				threshold = []any{0, 0}
			}
			conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":null},"doc_count":1,"__vega_metric":{"value":0},"__vega_nonnull":{"value":0}},{"key":{"0":1},"doc_count":2,"__vega_metric":{"value":0},"__vega_nonnull":{"value":2}}]`, `{"0":1}`), groupedPage(`[]`, "")}, nil)
			result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Integer}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum"}, Having: &interfaces.HavingClause{Field: "__value", Operation: op, Value: threshold}})
			require.NoError(t, err)
			switch op {
			case "==", ">=", "<=", "in", "range":
				assert.Equal(t, []map[string]any{{"key": json.Number("1"), "__value": json.Number("0")}}, result.Entries)
			default:
				assert.Empty(t, result.Entries)
			}
		}
	})
	t.Run("empty result retains execution accuracy", func(t *testing.T) {
		conn, _, closed := groupedQueryConnector(t, []string{groupedPage(`[]`, "")}, nil)
		result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "key", Aggr: "count_distinct"}})
		require.NoError(t, err)
		require.NotNil(t, result.Entries)
		assert.Empty(t, result.Entries)
		assert.True(t, result.IsApproximate)
		assert.True(t, *closed)
	})
	t.Run("temporal HAVING preserves timestamp input semantics", func(t *testing.T) {
		t.Setenv("TZ", "Asia/Shanghai")
		for _, input := range []any{"2026-10-19T00:00:00+08:00", "2026-10-19 00:00:00", json.Number("1792339200000"), "1792339200000"} {
			conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":null},"doc_count":1,"__vega_metric":{"value":1792339200000}},{"key":{"0":"CN"},"doc_count":1,"__vega_metric":{"value":1792339200001}}]`, `{"0":"CN"}`), groupedPage(`[]`, "")}, nil)
			params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "country"}}, Aggregation: &interfaces.Aggregation{Property: "created", Aggr: "min", Alias: "min_created"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: input}}
			result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "country", Type: interfaces.DataType_String}, {Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "date"}}}, params)
			require.NoError(t, err)
			assert.Equal(t, []map[string]any{{"country": "CN", "min_created": json.Number("1792339200001")}}, result.Entries)
			assert.Equal(t, input, params.Having.Value)
		}
	})
	t.Run("decimal HAVING uses declared scale", func(t *testing.T) {
		for _, operation := range []string{"sum", "min", "max"} {
			conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":null},"doc_count":2,"__vega_metric":{"value":0.30000000000000004},"__vega_nonnull":{"value":2}}]`, `{"0":null}`), groupedPage(`[]`, "")}, nil)
			result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Decimal, Attributes: map[string]any{"scale": 2}}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: operation}, Having: &interfaces.HavingClause{Field: "__value", Operation: "==", Value: json.Number("0.30")}})
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			assert.Nil(t, result.Entries[0]["key"])
		}
	})
	t.Run("calendar sources include NULL without synthesizing empty dates", func(t *testing.T) {
		for _, operation := range []string{"count_distinct", "sum", ""} {
			conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":null,"1":null},"doc_count":1,"__vega_metric":{"value":0},"__vega_nonnull":{"value":1}},{"key":{"0":1767225600000,"1":1767398400000},"doc_count":2,"__vega_metric":{"value":0},"__vega_nonnull":{"value":2}}]`, `{"0":1767225600000,"1":1767398400000}`), groupedPage(`[]`, "")}, func(body map[string]any) {
				sources := body["aggs"].(map[string]any)["__vega_groups"].(map[string]any)["composite"].(map[string]any)["sources"].([]any)
				for i, name := range []string{"month", "day"} {
					assert.Equal(t, map[string]any{"date_histogram": map[string]any{"field": name, "calendar_interval": name, "missing_bucket": true}}, sources[i].(map[string]any)[fmt.Sprint(i)])
				}
			})
			params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "month", CalendarInterval: "month"}, {Property: "day", CalendarInterval: "day"}}}
			if operation != "" {
				params.Aggregation = &interfaces.Aggregation{Property: "amount", Aggr: operation}
			}
			result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Integer}, {Name: "month", Type: interfaces.DataType_Datetime}, {Name: "day", Type: interfaces.DataType_Datetime}}}, params)
			require.NoError(t, err)
			require.Len(t, result.Entries, 2)
			assert.Nil(t, result.Entries[0]["day"])
			assert.Equal(t, json.Number("1767398400000"), result.Entries[1]["day"])
			if operation != "" {
				assert.Equal(t, json.Number("0"), result.Entries[1]["__value"])
			}
			assert.Equal(t, operation == "count_distinct", result.IsApproximate)
		}
	})
	t.Run("text keyword alias resolution applies to composite sources", func(t *testing.T) {
		for _, name := range []string{"body", "body_alias"} {
			conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[]`, "")}, func(body map[string]any) {
				source := body["aggs"].(map[string]any)["__vega_groups"].(map[string]any)["composite"].(map[string]any)["sources"].([]any)[0].(map[string]any)["0"].(map[string]any)
				assert.Equal(t, map[string]any{"terms": map[string]any{"field": "body.raw", "missing_bucket": true}}, source)
			})
			_, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
				{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword}}},
				{Name: "body_alias", OriginalType: "alias", Type: interfaces.DataType_Text, Attributes: map[string]any{"path": "body"}},
			}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: name}}})
			require.NoError(t, err)
		}
	})

	t.Run("wide integer continuation and NULL descending order", func(t *testing.T) {
		page := 0
		conn, _, _ := groupedQueryConnector(t, []string{
			groupedPage(`[{"key":{"0":null},"doc_count":1},{"key":{"0":9007199254740992},"doc_count":1}]`, `{"0":9007199254740992}`),
			groupedPage(`[{"key":{"0":9007199254740993},"doc_count":1}]`, `{"0":9007199254740993}`), groupedPage(`[]`, "")}, func(body map[string]any) {
			if page == 1 {
				after := body["aggs"].(map[string]any)["__vega_groups"].(map[string]any)["composite"].(map[string]any)["after"]
				assert.Equal(t, map[string]any{"0": json.Number("9007199254740992")}, after)
			}
			page++
		})
		result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Sort: []*interfaces.SortField{{Field: "key", Direction: "desc"}}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"key": json.Number("9007199254740993")}, {"key": json.Number("9007199254740992")}, {"key": nil}}, result.Entries)
	})
}

// 失败场景独立组织，避免与成功矩阵混在同一个过长的测试函数中。
func TestOpenSearchConnectorExecuteGroupedQueryFailures(t *testing.T) {
	t.Run("implicit count alias cannot overwrite a group key", func(t *testing.T) {
		conn, _, _ := groupedQueryConnector(t, []string{groupedPage(`[{"key":{"0":"value"},"doc_count":3}]`, `{"0":"value"}`), groupedPage(`[]`, "")}, nil)
		result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "__value", Type: interfaces.DataType_String}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "__value"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}})
		var conflict *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &conflict)
		assert.Nil(t, result)
	})

	valid := groupedPage(`[{"key":{"0":null},"doc_count":1,"__vega_metric":{"value":0},"__vega_nonnull":{"value":0}}]`, `{"0":null}`)
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Integer}}}
	for _, tc := range []struct {
		name      string
		pages     []string
		needTotal bool
	}{
		{name: "timed out", pages: []string{strings.Replace(valid, `"timed_out":false`, `"timed_out":true`, 1)}},
		{name: "failed shard", pages: []string{strings.Replace(valid, `"successful":1,"failed":0`, `"successful":0,"failed":1`, 1)}},
		{name: "missing shard metadata", pages: []string{`{"aggregations":{"__vega_groups":{"buckets":[]}}}`}},
		{name: "missing buckets", pages: []string{strings.Replace(valid, `"buckets":`, `"invalid":`, 1)}},
		{name: "invalid group key", pages: []string{strings.Replace(valid, `"key":{"0":null}`, `"key":{"1":null}`, 1)}},
		{name: "missing metric", pages: []string{strings.Replace(valid, `"__vega_metric":`, `"invalid":`, 1)}},
		{name: "missing nonnull count", pages: []string{strings.Replace(valid, `"__vega_nonnull":`, `"invalid":`, 1)}},
		{name: "missing continuation", pages: []string{strings.Replace(valid, `,"after_key":{"0":null}`, ``, 1)}},
		{name: "duplicate page", pages: []string{valid, valid}},
		{name: "continuation did not advance", pages: []string{valid, groupedPage(`[{"key":{"0":1},"doc_count":1,"__vega_metric":{"value":1},"__vega_nonnull":{"value":1}}]`, `{"0":null}`)}},
		{name: "later page failure discards prior rows", pages: []string{valid, `{"timed_out":false,"_shards":{"total":1,"successful":0,"failed":1}}`}},
		{name: "inexact total", pages: []string{strings.Replace(valid, `"relation":"eq"`, `"relation":"gte"`, 1)}, needTotal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, _, closed := groupedQueryConnector(t, tc.pages, nil)
			result, err := conn.ExecuteQuery(t.Context(), "orders", resource, &interfaces.ResourceDataQueryParams{NeedTotal: tc.needTotal, GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum"}})
			require.Error(t, err)
			assert.Nil(t, result)
			assert.True(t, *closed)
			assert.Empty(t, aggregationSlots)
		})
	}
	t.Run("PIT and search failures keep their error classifications", func(t *testing.T) {
		for _, pitFailure := range []bool{false, true} {
			for _, status := range []int{400, 404, 401, 403, 500} {
				closed := false
				client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, DisableRetry: true, Transport: countRowsTransport(func(request *http.Request) (*http.Response, error) {
					code := status
					body := `{"error":{"reason":"private backend detail"}}`
					switch {
					case request.Method == http.MethodDelete:
						closed = true
						code = 200
						body = `{}`
					case strings.HasSuffix(request.URL.Path, "/_search/point_in_time") && !pitFailure:
						code = 200
						body = `{"pit_id":"snapshot","_shards":{"total":1,"successful":1,"failed":0}}`
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})})
				require.NoError(t, err)
				result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(t.Context(), "orders", resource, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}})
				require.Error(t, err)
				assert.Nil(t, result)
				assert.NotContains(t, err.Error(), "private backend detail")
				var invalid *interfaces.ConditionBuildError
				assert.Equal(t, pitFailure && (status == 400 || status == 404), errors.As(err, &invalid))
				var forbidden *interfaces.SourceReadForbiddenError
				assert.Equal(t, pitFailure && (status == 401 || status == 403), errors.As(err, &forbidden))
				assert.Equal(t, !pitFailure, closed)
				assert.Empty(t, aggregationSlots)
			}
		}
	})

	t.Run("response memory budget", func(t *testing.T) {
		conn, _, closed := groupedQueryConnector(t, []string{strings.Repeat(" ", aggregationResponseBytes+1)}, nil)
		result, err := conn.ExecuteQuery(t.Context(), "orders", resource, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}})
		var budget *interfaces.QueryExecutionBudgetError
		require.ErrorAs(t, err, &budget)
		assert.Equal(t, "group_aggregation_response_memory", budget.Budget)
		assert.Nil(t, result)
		assert.True(t, *closed)
	})
	t.Run("group memory budget", func(t *testing.T) {
		var pages []string
		for i := 0; i < 3; i++ {
			key := fmt.Sprintf("%d%s", i, strings.Repeat("x", 3<<20))
			pages = append(pages, groupedPage(fmt.Sprintf(`[{"key":{"0":%q},"doc_count":1}]`, key), fmt.Sprintf(`{"0":%q}`, key)))
		}
		conn, _, closed := groupedQueryConnector(t, pages, nil)
		result, err := conn.ExecuteQuery(t.Context(), "orders", &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_String}}}, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}})
		var budget *interfaces.QueryExecutionBudgetError
		require.ErrorAs(t, err, &budget)
		assert.Equal(t, "group_aggregation_group_memory", budget.Budget)
		assert.Nil(t, result)
		assert.True(t, *closed)
	})
	t.Run("cancellation releases PIT", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		conn, _, closed := groupedQueryConnector(t, []string{valid}, func(map[string]any) { cancel() })
		result, err := conn.ExecuteQuery(ctx, "orders", resource, &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum"}})
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, result)
		assert.True(t, *closed)
	})
	t.Run("cancel while shared slots are occupied", func(t *testing.T) {
		for i := 0; i < cap(aggregationSlots); i++ {
			aggregationSlots <- struct{}{}
		}
		defer func() {
			for i := 0; i < cap(aggregationSlots); i++ {
				<-aggregationSlots
			}
		}()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result, err := (&OpenSearchConnector{}).executeGroupedQuery(ctx, "orders", resource, &interfaces.ResourceDataQueryParams{}, nil, nil)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, result)
	})
	t.Run("invalid output plans fail before PIT creation", func(t *testing.T) {
		for _, params := range []*interfaces.ResourceDataQueryParams{
			{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "count", Alias: "key"}},
			{GroupBy: []*interfaces.GroupByItem{{Property: "key"}, {Property: "key"}}},
			{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Sort: []*interfaces.SortField{{Field: "amount", Direction: "asc"}}},
			{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum"}, Having: &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{1}}},
		} {
			client, err := osclient.NewClient(osclient.Config{Addresses: []string{"http://opensearch.test"}, Transport: countRowsTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid plan must not create a PIT")
				return nil, errors.New("unexpected request")
			})})
			require.NoError(t, err)
			result, err := (&OpenSearchConnector{client: client}).ExecuteQuery(t.Context(), "orders", resource, params)
			var invalid *interfaces.ConditionBuildError
			require.ErrorAs(t, err, &invalid)
			assert.Nil(t, result)
			assert.Empty(t, aggregationSlots)
		}
	})
}
