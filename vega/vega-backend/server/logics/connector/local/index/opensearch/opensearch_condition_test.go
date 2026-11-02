// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestOpenSearchConnectorConvertFilterCondition(t *testing.T) {
	t.Run("time bounds include all legacy zero padding without adjacent instants", func(t *testing.T) {
		t.Run("large time sets retain keyword terms and enforce the expanded limit", func(t *testing.T) {
			field := &interfaces.Property{Name: "clock", OriginalName: "clock", Type: interfaces.DataType_Time}
			for _, size := range []int{5000, 10000} {
				values := make([]any, size)
				for i := range values {
					values[i] = time.UnixMilli(int64(i)).UTC().Format(common.TimeLayout)
				}
				cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("clock", "in", values), map[string]*interfaces.Property{"clock": field})
				require.NoError(t, err)
				query, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
				if size == 5000 {
					require.NoError(t, err)
					terms := query["terms"].(map[string]any)["clock"].([]any)
					assert.Greater(t, len(terms), 5000)
					assert.LessOrEqual(t, len(terms), 65536)
				} else {
					var buildErr *interfaces.ConditionBuildError
					require.ErrorAs(t, err, &buildErr)
					assert.Nil(t, query)
				}
			}
		})
		t.Run("sets include legacy whole second forms and exclude the next nanosecond", func(t *testing.T) {
			field := &interfaces.Property{Name: "clock", OriginalName: "clock", Type: interfaces.DataType_Time}
			for _, op := range []string{"in", "not_in", "!="} {
				var value any = []any{"12:00:00", "12:00:00.000", "12:00:00.000000000"}
				if op == "!=" {
					value = "12:00:00.000"
				}
				cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("clock", op, value), map[string]*interfaces.Property{"clock": field})
				require.NoError(t, err)
				query, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
				require.NoError(t, err)
				if op != "in" {
					assert.Equal(t, map[string]any{"exists": map[string]any{"field": "clock"}}, query["bool"].(map[string]any)["filter"])
					query = query["bool"].(map[string]any)["must_not"].(map[string]any)
				}
				terms := query["terms"].(map[string]any)["clock"].([]any)
				assert.Len(t, terms, 10)
				assert.Contains(t, terms, "12:00:00")
				assert.Contains(t, terms, "12:00:00.000000000")
				assert.NotContains(t, terms, "12:00:00.000000001")
			}
		})
		field := &interfaces.Property{Name: "clock", OriginalName: "clock", Type: interfaces.DataType_Time}
		for _, tc := range []struct{ op, bound, want string }{
			{">", "gt", "12:00:00.100000000"}, {">=", "gte", "12:00:00.1"},
			{"<", "lt", "12:00:00.1"}, {"<=", "lte", "12:00:00.100000000"},
		} {
			cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("clock", tc.op, "12:00:00.100"), map[string]*interfaces.Property{"clock": field})
			require.NoError(t, err)
			query, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"range": map[string]any{"clock": map[string]any{tc.bound: tc.want}}}, query)
			for _, source := range []string{"12:00:00.099999999", "12:00:00.1", "12:00:00.100", "12:00:00.100000000", "12:00:00.100000001"} {
				clock, err := time.Parse(time.TimeOnly, source)
				require.NoError(t, err)
				comparison := strings.Compare(source, tc.want)
				var got, want bool
				switch tc.op {
				case ">":
					got, want = comparison > 0, clock.Nanosecond() > 100000000
				case ">=":
					got, want = comparison >= 0, clock.Nanosecond() >= 100000000
				case "<":
					got, want = comparison < 0, clock.Nanosecond() < 100000000
				case "<=":
					got, want = comparison <= 0, clock.Nanosecond() <= 100000000
				}
				assert.Equal(t, want, got, "%s %s .1", source, tc.op)
			}
		}
		for _, literal := range []string{"12:00:00.1", "12:00:00.100", "12:00:00.100000000"} {
			cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("clock", "==", literal), map[string]*interfaces.Property{"clock": field})
			require.NoError(t, err)
			query, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"terms": map[string]any{"clock": []any{"12:00:00.1", "12:00:00.10", "12:00:00.100", "12:00:00.1000", "12:00:00.10000", "12:00:00.100000", "12:00:00.1000000", "12:00:00.10000000", "12:00:00.100000000"}}}, query)
		}
	})
	t.Run("date range budgets deduplicate and cover composite conditions", func(t *testing.T) {
		field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime, Attributes: map[string]any{"format": "epoch_millis"}}
		fields := map[string]*interfaces.Property{"created": field}
		duplicates := make([]any, 5000)
		for i := range duplicates {
			duplicates[i] = "2026-10-19T00:00:00Z"
		}
		cfg := osConstCfg("created", "in", duplicates)
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
		require.NoError(t, err)
		query, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
		require.NoError(t, err)
		assert.Len(t, query["bool"].(map[string]any)["should"], 1)
		values := make([]any, 600)
		for i := range values {
			values[i] = time.UnixMilli(1792339200000 + int64(i)).UTC().Format(time.RFC3339Nano)
		}
		composite := &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{osConstCfg("created", "in", values), osConstCfg("created", "not_in", values)}}
		cond, err = filter_condition.NewFilterCondition(context.Background(), composite, fields)
		require.NoError(t, err)
		query, err = (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
		var conditionErr *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &conditionErr)
		assert.Nil(t, query)
	})

	t.Run("whole seconds have explicit zero fractions", func(t *testing.T) {
		field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime}
		for _, op := range []string{"==", ">", ">=", "<", "<="} {
			cfg := osConstCfg("created", op, "2026-10-19T00:00:00Z")
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"created": field})
			require.NoError(t, err)
			dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.NoError(t, err)
			encoded, err := json.Marshal(dsl)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), "2026-10-19T00:00:00.000000000Z")
		}
	})
	t.Run("date sets enforce budgets for every mapping and precision", func(t *testing.T) {
		for _, format := range []string{"", "epoch_millis", "yyyy/MM/dd HH:mm:ss"} {
			for _, op := range []string{"in", "not_in"} {
				for _, size := range []int{1023, 1024, 1025, 5000} {
					field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime, Attributes: map[string]any{}}
					if format != "" {
						field.Attributes["format"] = format
					}
					values := make([]any, size)
					for i := range values {
						values[i] = time.UnixMilli(1792339200000 + int64(i)).UTC().Format(time.RFC3339Nano)
					}
					cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("created", op, values), map[string]*interfaces.Property{"created": field})
					require.NoError(t, err)
					dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
					clauses := size
					if op == "not_in" {
						clauses++ // 存在性约束也占用一条查询子句。
					}
					if clauses <= maxIndexTemporalRangeClauses {
						require.NoError(t, err)
						assert.Equal(t, size, countIndexRangeClauses(dsl))
						assert.Equal(t, clauses, countIndexQueryClauses(dsl))
					} else {
						var conditionErr *interfaces.ConditionBuildError
						require.ErrorAs(t, err, &conditionErr)
						assert.Nil(t, dsl)
					}
				}
			}
		}
	})
	t.Run("date nanos sets distinguish an exact millisecond from its next nanosecond", func(t *testing.T) {
		field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos", Attributes: map[string]any{"format": "epoch_millis"}}
		for _, op := range []string{"in", "not_in"} {
			for _, literal := range []any{"2026-10-19T00:00:00Z", json.Number("1792368000000"), "1792368000000"} {
				cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("created", op, []any{literal, "2026-10-19T00:00:00.000000001Z"}), map[string]*interfaces.Property{"created": field})
				require.NoError(t, err)
				dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
				require.NoError(t, err)
				if op == "not_in" {
					dsl = dsl["bool"].(map[string]any)["must_not"].(map[string]any)
				}
				queries := dsl["bool"].(map[string]any)["should"].([]map[string]any)
				require.Len(t, queries, 2)
				for i, text := range []string{"2026-10-19T00:00:00.000000000Z", "2026-10-19T00:00:00.000000001Z"} {
					assert.Equal(t, map[string]any{"range": map[string]any{"created": map[string]any{"gte": text, "lte": text, "format": "strict_date_optional_time_nanos"}}}, queries[i])
				}
			}
			// A set containing only an exact millisecond must also avoid the integer terms path.
			cond, err := filter_condition.NewFilterCondition(context.Background(), osConstCfg("created", op, []any{"2026-10-19T00:00:00Z"}), map[string]*interfaces.Property{"created": field})
			require.NoError(t, err)
			dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.NoError(t, err)
			assert.Equal(t, 1, countIndexRangeClauses(dsl))
		}
	})

	t.Run("datetime equality and sets retain boolean semantics", func(t *testing.T) {
		field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime, Attributes: map[string]any{"format": "epoch_millis"}}
		first, second := "2026-10-19T00:00:00.123456789Z", "2026-10-20T00:00:00.123456789Z"
		firstQuery := map[string]any{"range": map[string]any{"created": map[string]any{"gte": first, "lte": first, "format": "strict_date_optional_time_nanos"}}}
		secondQuery := map[string]any{"range": map[string]any{"created": map[string]any{"gte": second, "lte": second, "format": "strict_date_optional_time_nanos"}}}
		union := map[string]any{"bool": map[string]any{"should": []map[string]any{firstQuery, secondQuery}, "minimum_should_match": 1}}
		for _, tc := range []struct {
			op    string
			value any
			want  map[string]any
		}{
			{"==", first, firstQuery},
			{"!=", first, map[string]any{"bool": map[string]any{"filter": map[string]any{"exists": map[string]any{"field": "created"}}, "must_not": firstQuery}}},
			{"in", []any{first, second}, union},
			{"not_in", []any{first, second}, map[string]any{"bool": map[string]any{"filter": map[string]any{"exists": map[string]any{"field": "created"}}, "must_not": union}}},
		} {
			cfg := &interfaces.FilterCondCfg{Name: "created", Operation: tc.op, Value: tc.value}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"created": field})
			require.NoError(t, err)
			got, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.value, cfg.Value)
		}
	})

	t.Run("date queries declare their format independently of mapping", func(t *testing.T) {
		for _, format := range []string{"epoch_millis", "yyyy/MM/dd HH:mm:ss", "strict_date_optional_time_nanos"} {
			field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos", Attributes: map[string]any{"format": format}}
			for _, op := range []string{"==", "!=", ">", ">=", "<", "<=", "in", "not_in", "range", "out_range", "between"} {
				t.Run(format+"/"+op, func(t *testing.T) {
					literal := "2026-10-19T00:00:00.123456789Z"
					var value any = literal
					switch op {
					case "in", "not_in", "range", "out_range", "between":
						value = []any{literal, literal}
					}
					cfg := &interfaces.FilterCondCfg{Name: "created", Operation: op, Value: value}
					cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"created": field})
					require.NoError(t, err)
					dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
					require.NoError(t, err)
					encoded, err := json.Marshal(dsl)
					require.NoError(t, err)
					assert.Contains(t, string(encoded), `"format":"strict_date_optional_time_nanos"`)
					assert.Contains(t, string(encoded), literal)
					assert.NotContains(t, string(encoded), `"term":`)
					assert.NotContains(t, string(encoded), `"terms":`)
					assert.Equal(t, value, cfg.Value)
				})
			}
		}
	})

	t.Run("datetime keeps nanosecond precision", func(t *testing.T) {
		field := &interfaces.Property{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime}
		cfg := &interfaces.FilterCondCfg{Name: "created", Operation: ">", Value: "2026-10-19T00:00:00.123456789Z"}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"created": field})
		require.NoError(t, err)
		dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"range": map[string]any{"created": map[string]any{"gt": "2026-10-19T00:00:00.123456789Z", "format": "strict_date_optional_time_nanos"}}}, dsl)
	})
	t.Run("temporal input contract", func(t *testing.T) {
		t.Setenv("TZ", "Asia/Shanghai")
		for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Date, interfaces.DataType_Time} {
			field := &interfaces.Property{Name: "value", OriginalName: "value", Type: kind}
			literals := []any{"2026-10-19T00:00:00+08:00", "2026-10-19 00:00:00", json.Number("1792339200000"), "1792339200000"}
			if kind == interfaces.DataType_Date {
				literals = []any{"2026-10-19"}
			}
			if kind == interfaces.DataType_Time {
				literals = []any{"14:30:00.123", "14:30:00.123000", "14:30:00.123000000"}
			}
			for _, op := range []string{"==", "!=", ">", ">=", "<", "<=", "in", "not_in", "range", "out_range", "between"} {
				var expected map[string]any
				for _, literal := range literals {
					value := literal
					switch op {
					case "in", "not_in", "range", "out_range", "between":
						value = []any{literal, literal}
					}
					cfg := &interfaces.FilterCondCfg{Name: "value", Operation: op, Value: value}
					cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"value": field})
					require.NoError(t, err)
					dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, []*interfaces.Property{field})
					require.NoError(t, err)
					encoded, err := json.Marshal(dsl)
					require.NoError(t, err)
					switch kind {
					case interfaces.DataType_Datetime:
						assert.Contains(t, string(encoded), "2026-10-18T16:00:00.000000000Z")
					case interfaces.DataType_Time:
						assert.Contains(t, string(encoded), "14:30:00.123")
					default:
						assert.Contains(t, string(encoded), literal)
					}
					if expected == nil {
						expected = dsl
					} else {
						assert.Equal(t, expected, dsl, "equivalent temporal inputs must compile identically")
					}
					assert.Equal(t, value, cfg.Value)
				}
			}
		}
	})

	t.Run("text aliases use the target keyword field", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		target := &interfaces.Property{Name: "title", OriginalName: "physical.title", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": 8}}}}
		alias := &interfaces.Property{Name: "title_alias", OriginalName: "physical.alias", OriginalType: "alias",
			Type: interfaces.DataType_Text, Attributes: map[string]any{"path": "physical.title"}}
		schema := []*interfaces.Property{target, alias}
		fields := map[string]*interfaces.Property{target.Name: target, alias.Name: alias}
		for _, operation := range []string{
			filter_condition.OperationEqual, filter_condition.OperationNotEqual,
			filter_condition.OperationIn, filter_condition.OperationNotIn,
			filter_condition.OperationLike, filter_condition.OperationNotLike,
			filter_condition.OperationPrefix, filter_condition.OperationNotPrefix, filter_condition.OperationRegex,
			filter_condition.OperationContain, filter_condition.OperationNotContain,
		} {
			t.Run(operation, func(t *testing.T) {
				var value any = "hello"
				if operation == filter_condition.OperationIn || operation == filter_condition.OperationNotIn ||
					operation == filter_condition.OperationContain || operation == filter_condition.OperationNotContain {
					value = []any{"hello"}
				}
				cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: operation,
					ValueFrom: interfaces.ValueFrom_Const, Value: value}
				condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
				require.NoError(t, err)
				dsl, err := conn.ConvertFilterCondition(condition, schema)
				require.NoError(t, err)
				encoded, err := sonic.Marshal(dsl)
				require.NoError(t, err)
				assert.Contains(t, string(encoded), `"physical.title.raw"`)
				assert.NotContains(t, string(encoded), "title_alias")
			})
		}
		for _, field := range []string{target.Name, alias.Name} {
			for _, operation := range []string{filter_condition.OperationEqual, filter_condition.OperationNotEqual,
				filter_condition.OperationIn, filter_condition.OperationNotIn, filter_condition.OperationContain, filter_condition.OperationNotContain} {
				t.Run(field+"/"+operation+" preserves the request field on keyword overflow", func(t *testing.T) {
					var value any = "too-long-value"
					if operation == filter_condition.OperationIn || operation == filter_condition.OperationNotIn ||
						operation == filter_condition.OperationContain || operation == filter_condition.OperationNotContain {
						value = []any{value}
					}
					cfg := &interfaces.FilterCondCfg{Name: field, Operation: operation,
						ValueFrom: interfaces.ValueFrom_Const, Value: value}
					condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
					require.NoError(t, err)
					dsl, err := conn.ConvertFilterCondition(condition, schema)
					var limitErr *interfaces.KeywordLimitError
					require.ErrorAs(t, err, &limitErr)
					assert.Nil(t, dsl)
					assert.Equal(t, field, limitErr.Field)
					assert.Equal(t, 8, limitErr.Limit)
				})
			}
		}

		t.Run("field comparisons resolve aliases on both sides", func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: filter_condition.OperationEqual,
				ValueFrom: interfaces.ValueFrom_Field, Value: alias.Name}
			condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
			require.NoError(t, err)
			dsl, err := conn.ConvertFilterCondition(condition, schema)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"script": map[string]any{"script": map[string]any{
				"source": "doc.containsKey(params.left) && doc.containsKey(params.right) && doc[params.left].size() > 0 && doc[params.right].size() > 0 && doc[params.left].value == doc[params.right].value",
				"params": map[string]any{"left": "physical.title.raw", "right": "physical.title.raw"},
			}}}, dsl)
		})
		t.Run("missing keyword feature is still rejected", func(t *testing.T) {
			target.Features = nil
			cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: filter_condition.OperationEqual,
				ValueFrom: interfaces.ValueFrom_Const, Value: "hello"}
			condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
			require.NoError(t, err)
			dsl, err := conn.ConvertFilterCondition(condition, schema)
			require.ErrorContains(t, err, "no keyword feature")
			assert.Nil(t, dsl)
		})
	})
	conn := &OpenSearchConnector{}
	schema := opensearchConditionSchema()
	t.Run("rejects numeric boolean predicates", func(t *testing.T) {
		for _, operation := range []string{filter_condition.OperationTrue, filter_condition.OperationFalse} {
			condition := mustOSCondition(t, osConstCfg("age", operation, nil))
			got, err := conn.ConvertFilterCondition(condition, schema)
			require.ErrorContains(t, err, "requires BOOLEAN")
			assert.Nil(t, got)
		}
	})
	tests := []struct {
		name string
		cfg  *interfaces.FilterCondCfg
		want map[string]any
	}{
		{
			name: "equal string uses keyword field",
			cfg:  osConstCfg("name", filter_condition.OperationEqual, "alice"),
			want: map[string]any{"term": map[string]any{"name": "alice"}},
		},
		{
			name: "equal text uses keyword subfield",
			cfg:  osConstCfg("body", filter_condition.OperationEqual, "hello"),
			want: map[string]any{"term": map[string]any{"body.user-defined-keyword-name": "hello"}},
		},
		{
			name: "not equal excludes unindexed values",
			cfg:  osConstCfg("name", filter_condition.OperationNotEqual, "alice"),
			want: map[string]any{
				"bool": map[string]any{
					"filter": map[string]any{"exists": map[string]any{"field": "name"}},
					"must_not": map[string]any{
						"term": map[string]any{"name": "alice"},
					},
				},
			},
		},
		{
			name: "gt builds range",
			cfg:  osConstCfg("age", filter_condition.OperationGt, 18),
			want: map[string]any{"range": map[string]any{"age": map[string]any{"gt": 18}}},
		},
		{
			name: "in builds terms",
			cfg:  osConstCfg("name", filter_condition.OperationIn, []any{"alice", "bob"}),
			want: map[string]any{"terms": map[string]any{"name": []any{"alice", "bob"}}},
		},
		{
			name: "like matches the value as a literal substring",
			cfg:  osConstCfg("name", filter_condition.OperationLike, "ali"),
			want: map[string]any{"wildcard": map[string]any{"name": "*ali*"}},
		},
		{
			name: "like escapes wildcard metacharacters in the value",
			cfg:  osConstCfg("name", filter_condition.OperationLike, `a*b?c`),
			want: map[string]any{"wildcard": map[string]any{"name": `*a\*b\?c*`}},
		},
		{
			// Legacy spelling from a stored view definition: once marked, the index path still
			// renders it as a wildcard regexp, so the rows match what the query returned before
			name: "legacy like keeps the wildcard regexp",
			cfg:  osLegacyCfg("name", filter_condition.OperationLike, "%a_c%"),
			want: map[string]any{"regexp": map[string]any{"name": ".*a.c.*"}},
		},
		{
			name: "legacy not_like keeps the wildcard regexp",
			cfg:  osLegacyCfg("name", filter_condition.OperationNotLike, "a%"),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"regexp": map[string]any{"name": "a.*"}}}},
		},
		{
			name: "like treats an escaped percent as a literal character",
			cfg:  osConstCfg("name", filter_condition.OperationLike, `50\%`),
			want: map[string]any{"wildcard": map[string]any{"name": "*50%*"}},
		},
		{
			name: "range uses inclusive bounds",
			cfg:  osConstCfg("age", filter_condition.OperationRange, []any{18, 30}),
			want: map[string]any{"range": map[string]any{"age": map[string]any{"gte": 18, "lte": 30}}},
		},
		{
			name: "between uses inclusive bounds",
			cfg:  osConstCfg("created_at", filter_condition.OperationBetween, []any{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"}),
			want: map[string]any{"range": map[string]any{"created_at": map[string]any{"gte": "2026-01-01T00:00:00.000000000Z", "lte": "2026-01-02T00:00:00.000000000Z", "format": "strict_date_optional_time_nanos"}}},
		},
		{
			name: "not null builds exists",
			cfg:  osConstCfg("name", filter_condition.OperationNotNull, nil),
			want: map[string]any{"exists": map[string]any{"field": "name"}},
		},
		{
			name: "boolean equal true keeps term semantics",
			cfg:  osConstCfg("is_active", filter_condition.OperationEqual, true),
			want: map[string]any{"term": map[string]any{"is_active": true}},
		},
		{
			name: "boolean equal false keeps term semantics",
			cfg:  osConstCfg("is_active", filter_condition.OperationEqual, false),
			want: map[string]any{"term": map[string]any{"is_active": false}},
		},
		{
			name: "boolean null keeps missing value semantics",
			cfg:  osConstCfg("is_active", filter_condition.OperationNull, nil),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "is_active"}}}},
		},
		{
			name: "boolean not null keeps exists semantics",
			cfg:  osConstCfg("is_active", filter_condition.OperationNotNull, nil),
			want: map[string]any{"exists": map[string]any{"field": "is_active"}},
		},
		{
			name: "boolean exist keeps exists semantics",
			cfg:  osConstCfg("is_active", filter_condition.OperationExist, nil),
			want: map[string]any{"exists": map[string]any{"field": "is_active"}},
		},
		{
			name: "boolean not exist keeps missing value semantics",
			cfg:  osConstCfg("is_active", filter_condition.OperationNotExist, nil),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "is_active"}}}},
		},
		{
			name: "true builds boolean term",
			cfg:  osConstCfg("is_active", filter_condition.OperationTrue, nil),
			want: map[string]any{"term": map[string]any{"is_active": true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := mustOSCondition(t, tt.cfg)

			got, err := conn.ConvertFilterCondition(cond, schema)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("convert filter condition composite dsl", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		schema := opensearchConditionSchema()

		andCond := mustOSCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				osConstCfg("name", filter_condition.OperationEqual, "alice"),
				osConstCfg("age", filter_condition.OperationGte, 18),
			},
		})

		got, err := conn.ConvertFilterCondition(andCond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"name": "alice"}},
					{"range": map[string]any{"age": map[string]any{"gte": 18}}},
				},
			},
		}, got)

		orCond := mustOSCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationOr,
			SubConds: []*interfaces.FilterCondCfg{
				osConstCfg("name", filter_condition.OperationEqual, "alice"),
				osConstCfg("name", filter_condition.OperationEqual, "bob"),
			},
		})

		got, err = conn.ConvertFilterCondition(orCond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"name": "alice"}},
					{"term": map[string]any{"name": "bob"}},
				},
				"minimum_should_match": 1,
			},
		}, got)
	})

	t.Run("convert filter condition fulltext dsl", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		schema := opensearchConditionSchema()

		matchCond := mustOSCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationMatch,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
			RemainCfg: map[string]any{"fields": []any{"name", "body"}},
		})

		got, err := conn.ConvertFilterCondition(matchCond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"match": map[string]any{"name.user-defined-fulltext-name": "hello"}},
					{"match": map[string]any{"body": "hello"}},
				},
				"minimum_should_match": 1,
			},
		}, got)

		multiMatchCond := mustOSCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationMultiMatch,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
			RemainCfg: map[string]any{
				"fields":     []any{"name", "body"},
				"match_type": "best_fields",
			},
		})

		got, err = conn.ConvertFilterCondition(multiMatchCond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"multi_match": map[string]any{
				"query":  "hello",
				"fields": []string{"name.user-defined-fulltext-name", "body"},
				"type":   "best_fields",
			},
		}, got)
	})

	t.Run("convert filter condition knn vector dsl", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		schema := opensearchConditionSchema()
		cfg := &interfaces.FilterCondCfg{
			Name:      "embedding",
			Operation: filter_condition.OperationKnnVector,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     []float32{0.1, 0.2},
			RemainCfg: map[string]any{"limit_key": "k", "limit_value": 3},
			SubConds: []*interfaces.FilterCondCfg{
				osConstCfg("is_active", filter_condition.OperationTrue, nil),
			},
		}
		cond := mustOSCondition(t, cfg)

		got, err := conn.ConvertFilterCondition(cond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"knn": map[string]any{
				"embedding": map[string]any{
					"vector": []float32{0.1, 0.2},
					"k":      3,
					"filter": map[string]any{
						"bool": map[string]any{
							"must": []map[string]any{
								{"term": map[string]any{"is_active": true}},
							},
						},
					},
				},
			},
		}, got)
	})

	t.Run("uses direct k when limit key is absent", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Name:      "embedding",
			Operation: filter_condition.OperationKnnVector,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     []float32{0.1, 0.2},
			RemainCfg: map[string]any{"k": 50},
		}

		got, err := conn.ConvertFilterCondition(mustOSCondition(t, cfg), schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"knn": map[string]any{
				"embedding": map[string]any{
					"vector": []float32{0.1, 0.2},
					"k":      50,
				},
			},
		}, got)
	})
}

func TestOpenSearchConnectorConvertFilterConditionNotEqual(t *testing.T) {
	conn := &OpenSearchConnector{}
	t.Run("constant comparisons exclude unindexed values", func(t *testing.T) {
		tests := []struct {
			name       string
			field      *interfaces.Property
			value      any
			exactField string
			match      map[string]any
		}{
			{"boolean true", &interfaces.Property{Name: "flag", Type: interfaces.DataType_Boolean}, true, "flag", map[string]any{"term": map[string]any{"flag": true}}},
			{"boolean false", &interfaces.Property{Name: "flag", Type: interfaces.DataType_Boolean}, false, "flag", map[string]any{"term": map[string]any{"flag": false}}},
			{"physical boolean name", &interfaces.Property{Name: "flag", OriginalName: "physical_flag", Type: interfaces.DataType_Boolean}, true, "physical_flag", map[string]any{"term": map[string]any{"physical_flag": true}}},
			{"integer", &interfaces.Property{Name: "age", Type: interfaces.DataType_Integer}, 18, "age", map[string]any{"term": map[string]any{"age": 18}}},
			{"string with keyword limit", &interfaces.Property{Name: "name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}}}}, "alice", "name", map[string]any{"term": map[string]any{"name": "alice"}}},
			{
				name: "text keyword subfield with length limit",
				field: &interfaces.Property{Name: "body", OriginalName: "physical_body", Type: interfaces.DataType_Text,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, FeatureName: "raw", Config: map[string]any{"ignore_above": 256}}}},
				value: "hello", exactField: "physical_body.raw", match: map[string]any{"term": map[string]any{"physical_body.raw": "hello"}},
			},
			{"datetime", &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}, "2026-10-19T00:00:00Z", "created",
				map[string]any{"range": map[string]any{"created": map[string]any{"gte": "2026-10-19T00:00:00.000000000Z", "lte": "2026-10-19T00:00:00.000000000Z", "format": "strict_date_optional_time_nanos"}}}},
			{"time", &interfaces.Property{Name: "clock", Type: interfaces.DataType_Time}, "12:00:00", "clock",
				map[string]any{"terms": map[string]any{"clock": []any{"12:00:00", "12:00:00.0", "12:00:00.00", "12:00:00.000", "12:00:00.0000", "12:00:00.00000", "12:00:00.000000", "12:00:00.0000000", "12:00:00.00000000", "12:00:00.000000000"}}}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				for _, operation := range []string{"!=", "not_eq"} {
					cfg := osConstCfg(tc.field.Name, operation, tc.value)
					cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{tc.field.Name: tc.field})
					require.NoError(t, err)
					got, err := conn.ConvertFilterCondition(cond, []*interfaces.Property{tc.field})
					require.NoError(t, err)
					// NULL 和缺失字段都没有索引值，必须在同一个子条件内排除。
					assert.Equal(t, map[string]any{"bool": map[string]any{
						"filter":   map[string]any{"exists": map[string]any{"field": tc.exactField}},
						"must_not": tc.match,
					}}, got, operation)
					assert.Equal(t, tc.value, cfg.Value)
				}
			})
		}
	})

	t.Run("nested and or keep the existence guard on the inequality branch", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{
			osConstCfg("age", ">=", 18),
			{Operation: "or", SubConds: []*interfaces.FilterCondCfg{
				osConstCfg("is_active", "!=", true),
				osConstCfg("is_active", "null", nil),
			}},
		}}
		cond := mustOSCondition(t, cfg)
		got, err := conn.ConvertFilterCondition(cond, opensearchConditionSchema())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"bool": map[string]any{"must": []map[string]any{
			{"range": map[string]any{"age": map[string]any{"gte": 18}}},
			{"bool": map[string]any{"minimum_should_match": 1, "should": []map[string]any{
				{"bool": map[string]any{
					"filter":   map[string]any{"exists": map[string]any{"field": "is_active"}},
					"must_not": map[string]any{"term": map[string]any{"is_active": true}},
				}},
				{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "is_active"}}}},
			}}},
		}}}, got)
	})
}

func TestOpenSearchConnectorConvertFilterConditionNotIn(t *testing.T) {
	conn := &OpenSearchConnector{}
	for _, tc := range []struct {
		name       string
		field      *interfaces.Property
		values     []any
		exactField string
	}{
		{"boolean true", &interfaces.Property{Name: "flag", Type: interfaces.DataType_Boolean}, []any{true}, "flag"},
		{"boolean false", &interfaces.Property{Name: "flag", Type: interfaces.DataType_Boolean}, []any{false}, "flag"},
		{"both boolean values", &interfaces.Property{Name: "flag", Type: interfaces.DataType_Boolean}, []any{true, false}, "flag"},
		{"physical integer name", &interfaces.Property{Name: "age", OriginalName: "physical_age", Type: interfaces.DataType_Integer}, []any{18, 30}, "physical_age"},
		{"string with keyword limit", &interfaces.Property{Name: "name", Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}}}}, []any{"alice", "bob"}, "name"},
		{"text keyword subfield with length limit", &interfaces.Property{Name: "body", OriginalName: "physical_body", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, FeatureName: "raw", Config: map[string]any{"ignore_above": 256}}}}, []any{"hello", "world"}, "physical_body.raw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := osConstCfg(tc.field.Name, "not_in", tc.values)
			condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{tc.field.Name: tc.field})
			require.NoError(t, err)
			got, err := conn.ConvertFilterCondition(condition, []*interfaces.Property{tc.field})
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"bool": map[string]any{
				"filter":   map[string]any{"exists": map[string]any{"field": tc.exactField}},
				"must_not": map[string]any{"terms": map[string]any{tc.exactField: tc.values}},
			}}, got)
			assert.Equal(t, tc.values, cfg.Value)
		})
	}

	t.Run("nested and or keep explicit null alternatives", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{
			osConstCfg("age", ">=", 18),
			{Operation: "or", SubConds: []*interfaces.FilterCondCfg{
				osConstCfg("name", "not_in", []any{"alice", "bob"}),
				osConstCfg("name", "null", nil),
			}},
		}}
		got, err := conn.ConvertFilterCondition(mustOSCondition(t, cfg), opensearchConditionSchema())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"bool": map[string]any{"must": []map[string]any{
			{"range": map[string]any{"age": map[string]any{"gte": 18}}},
			{"bool": map[string]any{"minimum_should_match": 1, "should": []map[string]any{
				{"bool": map[string]any{
					"filter":   map[string]any{"exists": map[string]any{"field": "name"}},
					"must_not": map[string]any{"terms": map[string]any{"name": []any{"alice", "bob"}}},
				}},
				{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "name"}}}},
			}}},
		}}}, got)
	})
}

func TestOpenSearchConnectorConvertFilterConditionEqual(t *testing.T) {
	t.Run("uses each field's own keyword name for field comparison", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{Name: "left", OriginalName: "left", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
				FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword,
			}}},
			{Name: "right", OriginalName: "right", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
				FeatureName: "exact", FeatureType: interfaces.PropertyFeatureType_Keyword,
			}}},
		}
		fields := map[string]*interfaces.Property{"left": schema[0], "right": schema[1]}
		for _, operation := range []string{filter_condition.OperationEqual, filter_condition.OperationNotEqual} {
			cfg := &interfaces.FilterCondCfg{
				Name: "left", Operation: operation,
				ValueFrom: interfaces.ValueFrom_Field, Value: "right",
			}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
			require.NoError(t, err)

			got, err := conn.ConvertFilterCondition(cond, schema)

			require.NoError(t, err)
			operator := "=="
			if operation == filter_condition.OperationNotEqual {
				operator = "!="
			}
			assert.Equal(t, map[string]any{
				"script": map[string]any{"script": map[string]any{
					"source": "doc.containsKey(params.left) && doc.containsKey(params.right) && doc[params.left].size() > 0 && doc[params.right].size() > 0 && doc[params.left].value " + operator + " doc[params.right].value",
					"params": map[string]any{
						"left":  "left.raw",
						"right": "right.exact",
					},
				}},
			}, got)
		}
	})

	t.Run("passes custom keyword names as script parameters", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{Name: "left", OriginalName: "left", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
				FeatureName: "ra'w", FeatureType: interfaces.PropertyFeatureType_Keyword,
			}}},
			{Name: "right", OriginalName: "right", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
				FeatureName: "ex'act", FeatureType: interfaces.PropertyFeatureType_Keyword,
			}}},
		}
		fields := map[string]*interfaces.Property{"left": schema[0], "right": schema[1]}
		cfg := &interfaces.FilterCondCfg{
			Name: "left", Operation: filter_condition.OperationEqual,
			ValueFrom: interfaces.ValueFrom_Field, Value: "right",
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
		require.NoError(t, err)

		got, err := conn.ConvertFilterCondition(cond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"script": map[string]any{"script": map[string]any{
				"source": "doc.containsKey(params.left) && doc.containsKey(params.right) && doc[params.left].size() > 0 && doc[params.right].size() > 0 && doc[params.left].value == doc[params.right].value",
				"params": map[string]any{
					"left":  "left.ra'w",
					"right": "right.ex'act",
				},
			}},
		}, got)
	})

	t.Run("uses default keyword name when feature name is empty", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, "hello"))
		schema := opensearchConditionSchema()
		schema[1].Features[0].FeatureName = ""

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"term": map[string]any{"body.keyword": "hello"}}, got)
	})

	t.Run("uses discovered keyword physical name", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, "hello"))
		schema := opensearchConditionSchema()
		schema[1].Features[0] = interfaces.PropertyFeature{
			FeatureName: "body.raw",
			FeatureType: interfaces.PropertyFeatureType_Keyword,
			IsNative:    true,
		}

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"term": map[string]any{"body.raw": "hello"}}, got)
	})

	t.Run("treats keyword feature matching property name as a subfield", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, "hello"))
		schema := opensearchConditionSchema()
		schema[1].Features[0].FeatureName = "body"

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"term": map[string]any{"body.body": "hello"}}, got)
	})

	t.Run("rejects text field without keyword feature", func(t *testing.T) {
		conn := &OpenSearchConnector{}

		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, "hello"))
		got, err := conn.ConvertFilterConditionEqual(cond, []*interfaces.Property{
			{
				Name:         "body",
				OriginalName: "body",
				Type:         interfaces.DataType_Text,
			},
		})
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "no keyword feature")
		var buildErr *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &buildErr)
	})

	t.Run("rejects exact text values above keyword ignore_above", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, strings.Repeat("字", 257)))
		schema := opensearchConditionSchema()
		schema[1].Features[0].Config = map[string]any{"ignore_above": 256}

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "exceeds keyword ignore_above 256")
		var limitErr *interfaces.KeywordLimitError
		require.ErrorAs(t, err, &limitErr)
		assert.Equal(t, "body", limitErr.Field)
		assert.Equal(t, 256, limitErr.Limit)
		var buildErr *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &buildErr)
	})

	t.Run("rejects exact string values above keyword ignore_above", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("name", filter_condition.OperationEqual, "abcdef"))
		schema := opensearchConditionSchema()
		schema[0].Features = append(schema[0].Features, interfaces.PropertyFeature{
			FeatureName: interfaces.LocalIndexKeywordSubfieldName,
			FeatureType: interfaces.PropertyFeatureType_Keyword,
			Config:      map[string]any{"ignore_above": 5},
		})

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "exceeds keyword ignore_above 5")
		var buildErr *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &buildErr)
	})

	t.Run("rejects oversized exact text value when original name is empty", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, strings.Repeat("字", 257)))
		schema := opensearchConditionSchema()
		schema[1].OriginalName = ""
		schema[1].Features[0].Config = map[string]any{"ignore_above": 256}

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "exceeds keyword ignore_above 256")
		var limitErr *interfaces.KeywordLimitError
		require.ErrorAs(t, err, &limitErr)
		assert.Equal(t, "body", limitErr.Field)
		assert.Equal(t, 256, limitErr.Limit)
		var buildErr *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &buildErr)
	})

	t.Run("counts supplementary characters as UTF-16 code units", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, "😀"))
		schema := opensearchConditionSchema()
		schema[1].Features[0].Config = map[string]any{"ignore_above": int32(1)}

		got, err := conn.ConvertFilterConditionEqual(cond, schema)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "exceeds keyword ignore_above 1")
	})
}

func TestOpenSearchConnectorConvertFilterConditionOrderedFieldComparison(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := []*interfaces.Property{
		{Name: "left", OriginalName: "le'ft", Type: interfaces.DataType_Integer},
		{Name: "right", OriginalName: "ri'ght", Type: interfaces.DataType_Integer},
	}
	fields := map[string]*interfaces.Property{"left": schema[0], "right": schema[1]}
	tests := []struct {
		operation string
		operator  string
	}{
		{operation: filter_condition.OperationGt, operator: ">"},
		{operation: filter_condition.OperationGte, operator: ">="},
		{operation: filter_condition.OperationLt, operator: "<"},
		{operation: filter_condition.OperationLte, operator: "<="},
	}

	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{
				Name:      "left",
				Operation: tt.operation,
				ValueFrom: interfaces.ValueFrom_Field,
				Value:     "right",
			}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
			require.NoError(t, err)

			got, err := conn.ConvertFilterCondition(cond, schema)

			require.NoError(t, err)
			assert.Equal(t, map[string]any{
				"script": map[string]any{"script": map[string]any{
					"source": "doc.containsKey(params.left) && doc.containsKey(params.right) && doc[params.left].size() > 0 && doc[params.right].size() > 0 && doc[params.left].value " + tt.operator + " doc[params.right].value",
					"params": map[string]any{
						"left":  "le'ft",
						"right": "ri'ght",
					},
				}},
			}, got)
		})
	}
}

func TestOpenSearchConnectorConvertFilterConditionAnd(t *testing.T) {
	t.Run("rejects non and condition", func(t *testing.T) {
		conn := &OpenSearchConnector{}
		schema := opensearchConditionSchema()

		got, err := conn.ConvertFilterConditionAnd(&filter_condition.EqualCond{}, schema)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "condition is not")
	})
}

func mustOSCondition(t *testing.T, cfg *interfaces.FilterCondCfg) interfaces.FilterCondition {
	t.Helper()

	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, opensearchConditionFieldsMap())
	require.NoError(t, err)
	require.NotNil(t, cond)
	return cond
}

func osConstCfg(name string, op string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueFrom: interfaces.ValueFrom_Const,
		Value:     value,
	}
}

func opensearchConditionFieldsMap() map[string]*interfaces.Property {
	fields := map[string]*interfaces.Property{}
	for _, prop := range opensearchConditionSchema() {
		cp := *prop
		fields[prop.Name] = &cp
	}
	return fields
}

func opensearchConditionSchema() []*interfaces.Property {
	return []*interfaces.Property{
		{
			Name:         "name",
			OriginalName: "name",
			Type:         interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "user-defined-fulltext-name", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			},
		},
		{
			Name:         "body",
			OriginalName: "body",
			Type:         interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "user-defined-keyword-name", FeatureType: interfaces.PropertyFeatureType_Keyword},
			},
		},
		{Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		{Name: "is_active", OriginalName: "is_active", Type: interfaces.DataType_Boolean},
		{Name: "embedding", OriginalName: "embedding", Type: interfaces.DataType_Vector},
	}
}

func TestFulltextFieldName(t *testing.T) {
	t.Run("fulltext field name string uses subfield", func(t *testing.T) {
		prop := &interfaces.Property{
			Name: "team_name",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "user-defined-name", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			},
		}

		assert.Equal(t, "team_name.user-defined-name", fulltextFieldName(prop))
	})

	t.Run("fulltext field name accepts discovered physical name", func(t *testing.T) {
		prop := &interfaces.Property{
			Name:         "teamName",
			OriginalName: "team_name",
			Type:         interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "team_name.analyzed", FeatureType: interfaces.PropertyFeatureType_Fulltext, IsNative: true},
			},
		}

		assert.Equal(t, "team_name.analyzed", fulltextFieldName(prop))
	})

	t.Run("fulltext feature matching property name remains a subfield", func(t *testing.T) {
		prop := &interfaces.Property{
			Name: "team_name",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "team_name", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			},
		}

		assert.Equal(t, "team_name.team_name", fulltextFieldName(prop))
	})

	t.Run("fulltext field name text uses bare name", func(t *testing.T) {
		prop := &interfaces.Property{Name: "body", Type: interfaces.DataType_Text}

		assert.Equal(t, "body", fulltextFieldName(prop))
	})

	t.Run("fulltext field name uses default when feature name is empty", func(t *testing.T) {
		prop := &interfaces.Property{
			Name: "team_name",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureType: interfaces.PropertyFeatureType_Fulltext},
			},
		}

		assert.Equal(t, "team_name.fulltext", fulltextFieldName(prop))
	})

	t.Run("fulltext field name string no fulltext bare name", func(t *testing.T) {
		prop := &interfaces.Property{Name: "code", Type: interfaces.DataType_String}

		assert.Equal(t, "code", fulltextFieldName(prop))
	})
}

// osLegacyCfg builds a like/not_like marked as legacy, i.e. one using % as a wildcard
func osLegacyCfg(name string, op string, value any) *interfaces.FilterCondCfg {
	cfg := osConstCfg(name, op, value)
	cfg.LegacyLikeWildcards = true
	return cfg
}

func TestOpenSearchConnectorExactMatchFieldName(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := []*interfaces.Property{{Name: "id", OriginalName: "physical_id", Type: interfaces.DataType_Integer}, {Name: "order.items", Type: interfaces.DataType_String}}
	for _, name := range []string{"missing", "payload.id", "physical_id.child"} {
		field, err := conn.exactMatchFieldName(name, schema)
		require.Error(t, err)
		assert.Empty(t, field)
		assert.Contains(t, err.Error(), "not found")
	}
	for _, name := range []string{"physical_id", "order.items"} {
		field, err := conn.exactMatchFieldName(name, schema)
		require.NoError(t, err)
		assert.Equal(t, name, field)
	}
}

func TestValidateKeywordValues(t *testing.T) {
	for _, typ := range []string{interfaces.DataType_String, interfaces.DataType_Text} {
		for _, tc := range []struct {
			name     string
			value    any
			rejected bool
		}{
			{"at ASCII limit", strings.Repeat("X", 256), false},
			{"above ASCII limit", strings.Repeat("X", 257), true},
			{"at Unicode limit", strings.Repeat("字", 256), false},
			{"at supplementary limit", strings.Repeat("😀", 128), false},
			{"above supplementary limit", strings.Repeat("😀", 129), true},
			{"set with oversized candidate", []any{"CLOSED", strings.Repeat("X", 257)}, true},
			{"valid set", []any{"CLOSED", "OPEN"}, false},
		} {
			t.Run(typ+"/"+tc.name, func(t *testing.T) {
				schema := []*interfaces.Property{{Name: "status", Type: typ, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}}}}}
				err := validateKeywordValues("status", "status", tc.value, schema)
				if !tc.rejected {
					require.NoError(t, err)
					return
				}
				var limit *interfaces.KeywordLimitError
				require.ErrorAs(t, err, &limit)
				assert.Equal(t, "status", limit.Field)
				assert.Equal(t, 256, limit.Limit)
				var condition *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &condition)
			})
		}
	}
}

func TestOpenSearchConnectorConvertFilterConditionUnsignedFieldComparison(t *testing.T) {
	for _, types := range [][2]string{
		{interfaces.DataType_UnsignedInteger, interfaces.DataType_UnsignedInteger},
		{interfaces.DataType_UnsignedInteger, interfaces.DataType_Integer},
		{interfaces.DataType_Integer, interfaces.DataType_UnsignedInteger},
		{interfaces.DataType_UnsignedInteger, interfaces.DataType_Float},
		{interfaces.DataType_Float, interfaces.DataType_UnsignedInteger},
	} {
		for _, operation := range []string{"==", "!=", ">", ">=", "<", "<="} {
			t.Run(types[0]+"/"+operation+"/"+types[1], func(t *testing.T) {
				schema := []*interfaces.Property{
					{Name: "left", OriginalName: "physical.left", Type: types[0]},
					{Name: "right", OriginalName: "physical.right", Type: types[1]},
				}
				cfg := &interfaces.FilterCondCfg{Name: "left", Operation: operation,
					ValueFrom: interfaces.ValueFrom_Field, Value: "right"}
				condition, err := filter_condition.NewFilterCondition(t.Context(), cfg,
					map[string]*interfaces.Property{"left": schema[0], "right": schema[1]})
				require.NoError(t, err)
				dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(condition, schema)
				require.NoError(t, err)
				script := dsl["script"].(map[string]any)["script"].(map[string]any)
				assert.Equal(t, map[string]any{"left": "physical.left", "right": "physical.right"}, script["params"])
				comparison := "new BigInteger(doc[params.left].value.toString()).compareTo(new BigInteger(doc[params.right].value.toString())) " + operation + " 0"
				if types[0] == interfaces.DataType_Float || types[1] == interfaces.DataType_Float {
					comparison = "doc[params.left].value " + operation + " doc[params.right].value"
				}
				assert.Equal(t,
					"doc.containsKey(params.left) && doc.containsKey(params.right) && doc[params.left].size() > 0 && doc[params.right].size() > 0 && "+comparison,
					script["source"])
			})
		}
	}
}

func TestOpenSearchConnectorConvertFilterConditionTemporalFieldComparison(t *testing.T) {
	for _, leftType := range []string{interfaces.DataType_Date, interfaces.DataType_Datetime, interfaces.DataType_Timestamp} {
		for _, rightType := range []string{interfaces.DataType_Date, interfaces.DataType_Datetime, interfaces.DataType_Timestamp} {
			schema := []*interfaces.Property{
				{Name: "start", OriginalName: "physical.start", Type: leftType},
				{Name: "end", OriginalName: "physical.end", Type: rightType},
			}
			fields := map[string]*interfaces.Property{"start": schema[0], "end": schema[1]}
			for _, operation := range []string{"==", "!=", ">", ">=", "<", "<="} {
				t.Run(leftType+"/"+operation+"/"+rightType, func(t *testing.T) {
					cfg := &interfaces.FilterCondCfg{Name: "start", Operation: operation,
						ValueFrom: interfaces.ValueFrom_Field, Value: "end"}
					condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
					require.NoError(t, err)
					dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(condition, schema)
					require.NoError(t, err)
					script := dsl["script"].(map[string]any)["script"].(map[string]any)
					assert.Equal(t, map[string]any{"left": "physical.start", "right": "physical.end"}, script["params"])
					assert.Equal(t,
						"doc.containsKey(params.left) && doc.containsKey(params.right) && doc[params.left].size() > 0 && doc[params.right].size() > 0 && "+
							"doc[params.left].value.toInstant().compareTo(doc[params.right].value.toInstant()) "+operation+" 0",
						script["source"])
					assert.NotContains(t, script["source"], "toEpochMilli")
				})
			}
		}
	}
}

func TestIndexBeforeQuery(t *testing.T) {
	now := time.Date(2024, 3, 31, 0, 30, 0, 123456789, time.UTC)
	t.Run("range errors use normalized units", func(t *testing.T) {
		t.Setenv("TZ", "UTC")
		for _, tc := range []struct {
			originalType, unit string
			inputs             []string
		}{
			{originalType: "date_nanos", unit: "day", inputs: []string{"day", "DAY", " Day ", "\tDaY\n"}},
			{originalType: "date", unit: "year", inputs: []string{"year", "YEAR", " Year ", "\tYeAr\n"}},
		} {
			for _, input := range tc.inputs {
				t.Run(tc.originalType+"/"+input, func(t *testing.T) {
					cond := &filter_condition.BeforeCond{
						Lfield: &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: tc.originalType},
						Value:  []any{int64(2147483647), input},
					}
					dsl, err := indexBeforeQuery(cond, now)
					var rangeErr *interfaces.TemporalRangeError
					require.ErrorAs(t, err, &rangeErr)
					assert.Nil(t, dsl)
					assert.Equal(t, tc.unit, rangeErr.Unit)
					assert.Equal(t, input, cond.Value[1])
				})
			}
		}
	})
	t.Run("date nanos interval must produce a supported boundary", func(t *testing.T) {
		t.Setenv("TZ", "UTC")
		for _, unit := range []string{"year", "month", "week", "day", "hour", "minute", "second"} {
			for _, originalType := range []string{"date", "date_nanos"} {
				t.Run(originalType+"/"+unit, func(t *testing.T) {
					// A large interval can be valid for date while exceeding date_nanos.
					interval := int64(2147483647)
					cond := &filter_condition.BeforeCond{Lfield: &interfaces.Property{Name: "created", OriginalName: "physical.created", Type: interfaces.DataType_Datetime, OriginalType: originalType}, Value: []any{interval, unit}}
					dsl, err := indexBeforeQuery(cond, now)
					if originalType == "date_nanos" || unit == "year" {
						var buildErr *interfaces.ConditionBuildError
						require.ErrorAs(t, err, &buildErr)
						assert.Contains(t, err.Error(), "created")
						assert.Nil(t, dsl)
					} else {
						require.NoError(t, err)
						assert.Contains(t, dsl, "range")
					}
				})
			}
		}
	})
	cutoffs := map[string]string{
		"year":   "2021-03-31T00:30:00.123456789Z",
		"month":  "2023-12-31T00:30:00.123456789Z",
		"week":   "2024-03-10T00:30:00.123456789Z",
		"day":    "2024-03-28T00:30:00.123456789Z",
		"hour":   "2024-03-30T21:30:00.123456789Z",
		"minute": "2024-03-31T00:27:00.123456789Z",
		"second": "2024-03-31T00:29:57.123456789Z",
	}
	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
		for _, typ := range []string{interfaces.DataType_Date, interfaces.DataType_Datetime} {
			for _, unit := range []string{"year", "month", "week", "day", "hour", "minute", "second"} {
				t.Run(zone+"/"+typ+"/"+unit, func(t *testing.T) {
					t.Setenv("TZ", zone)
					cfg := &interfaces.FilterCondCfg{Name: "at", Operation: "before", ValueFrom: interfaces.ValueFrom_Const, Value: []any{json.Number("3"), unit}}
					condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{"at": {Name: "at", OriginalName: "physical.at", Type: typ}})
					require.NoError(t, err)
					got, err := indexBeforeQuery(condition.(*filter_condition.BeforeCond), now)
					require.NoError(t, err)
					want, err := time.Parse(time.RFC3339Nano, cutoffs[unit])
					require.NoError(t, err)
					if typ == interfaces.DataType_Date {
						switch zone {
						case "Asia/Shanghai":
							want = want.Add(8 * time.Hour)
						case "America/New_York":
							want = want.Add(-4 * time.Hour)
						}
					} else if zone == "America/New_York" && (unit == "month" || unit == "week") {
						want = want.Add(time.Hour)
					}
					assert.Equal(t, map[string]any{"range": map[string]any{"physical.at": map[string]any{
						"lt": want.Format(common.RFC3339FixedNanosLayout), "format": "strict_date_optional_time_nanos",
					}}}, got)
				})
			}
		}
	}
	t.Run("emitted boundary follows standard library calendar arithmetic", func(t *testing.T) {
		t.Setenv("TZ", "America/New_York")
		location, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)
		for _, value := range []string{"2024-03-11T06:30:00.123456789Z", "2024-11-04T06:30:00.123456789Z"} {
			t.Run(value, func(t *testing.T) {
				anchor, err := time.Parse(time.RFC3339Nano, value)
				require.NoError(t, err)
				want := anchor.In(location).AddDate(0, 0, -1)
				cond := &filter_condition.BeforeCond{Lfield: &interfaces.Property{Name: "at", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"}, Value: []any{int64(1), "day"}}
				dsl, err := indexBeforeQuery(cond, anchor)
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"range": map[string]any{"at": map[string]any{
					"lt": want.UTC().Format(common.RFC3339FixedNanosLayout), "format": "strict_date_optional_time_nanos",
				}}}, dsl)
			})
		}
	})
	t.Run("legacy reference time retains its meaning", func(t *testing.T) {
		cond := &filter_condition.BeforeCond{Lfield: &interfaces.Property{Name: "at", OriginalName: "physical.at", Type: interfaces.DataType_Datetime}, Value: []any{float64(3), "2026-10-30T00:00:00Z"}}
		got, err := indexBeforeQuery(cond, now)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"range": map[string]any{"physical.at": map[string]any{"lt": "2026-10-29T21:00:00Z"}}}, got)
	})
	t.Run("legacy nanos validates the emitted second precision boundary", func(t *testing.T) {
		for _, tc := range []struct {
			name, reference, want string
			rejected              bool
		}{
			{name: "epoch second", reference: "1970-01-01T01:00:00.9Z", want: "1970-01-01T00:00:00Z"},
			{name: "last supported second", reference: "2262-04-12T00:47:16.9Z", want: "2262-04-11T23:47:16Z"},
			{name: "before epoch", reference: "1970-01-01T00:59:59.999999999Z", rejected: true},
			{name: "after last supported second", reference: "2262-04-12T00:47:17Z", rejected: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cond := &filter_condition.BeforeCond{Lfield: &interfaces.Property{Name: "at", OriginalName: "physical.at", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"}, Value: []any{float64(1), tc.reference}}
				got, err := indexBeforeQuery(cond, now)
				if tc.rejected {
					var temporal *interfaces.TemporalRangeError
					require.ErrorAs(t, err, &temporal)
					assert.Nil(t, got)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"range": map[string]any{"physical.at": map[string]any{"lt": tc.want}}}, got)
			})
		}
	})
	t.Run("invalid zone retains server configuration classification", func(t *testing.T) {
		t.Setenv("TZ", "Invalid/Zone")
		_, err := indexBeforeQuery(&filter_condition.BeforeCond{Lfield: &interfaces.Property{Name: "at", Type: interfaces.DataType_Datetime}, Value: []any{float64(3), "day"}}, now)
		var zoneErr *interfaces.ServerTimeZoneError
		require.ErrorAs(t, err, &zoneErr)
	})
	for _, values := range [][]any{{float64(0), "day"}, {float64(-1), "month"}, {float64(1.5), "day"}, {float64(2147483648), "day"}, {float64(3), "unknown"}} {
		t.Run(fmt.Sprint(values), func(t *testing.T) {
			_, err := indexBeforeQuery(&filter_condition.BeforeCond{Lfield: &interfaces.Property{Name: "at", Type: interfaces.DataType_Datetime}, Value: values}, now)
			var buildErr *interfaces.ConditionBuildError
			require.ErrorAs(t, err, &buildErr)
		})
	}
}

func TestValidateBeforeConditions(t *testing.T) {
	t.Setenv("TZ", "UTC")
	now := time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)
	field := &interfaces.Property{Name: "nano_time", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"}
	bad := &filter_condition.BeforeCond{Lfield: field, Value: []any{int64(2147483647), "day"}}
	valid := &filter_condition.BeforeCond{Lfield: field, Value: []any{int64(1), "day"}}
	for name, condition := range map[string]interfaces.FilterCondition{
		"leaf":             bad,
		"and":              &filter_condition.AndCond{SubConds: []interfaces.FilterCondition{valid, bad}},
		"or":               &filter_condition.OrCond{SubConds: []interfaces.FilterCondition{valid, bad}},
		"vector subfilter": &filter_condition.KnnVectorCond{SubConds: []interfaces.FilterCondition{bad}},
	} {
		t.Run(name, func(t *testing.T) {
			var rangeErr *interfaces.TemporalRangeError
			require.ErrorAs(t, ValidateBeforeConditions(condition, now), &rangeErr)
			assert.Equal(t, "nano_time", rangeErr.Field)
			assert.Equal(t, "date_nanos", rangeErr.FieldType)
			assert.Equal(t, int64(2147483647), rangeErr.Interval)
			assert.Equal(t, "day", rangeErr.Unit)
		})
	}
	require.NoError(t, ValidateBeforeConditions(nil, now))
	require.NoError(t, ValidateBeforeConditions(valid, now))
}

func TestValidateIndexTimeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, value, typ string
		bad              bool
	}{
		{name: "epoch", value: "1970-01-01T00:00:00Z", typ: "date_nanos"},
		{name: "below epoch", value: "1969-12-31T23:59:59.999999999Z", typ: "date_nanos", bad: true},
		{name: "same instant in offset", value: "1970-01-01T08:00:00+08:00", typ: "date_nanos"},
		{name: "maximum nanos", value: "2262-04-11T23:47:16.854775807Z", typ: "date_nanos"},
		{name: "above maximum nanos", value: "2262-04-11T23:47:16.854775808Z", typ: "date_nanos", bad: true},
		{name: "ordinary date before epoch", value: "1900-01-01T00:00:00Z", typ: "date"},
		{name: "ordinary date after nanos maximum", value: "3000-01-01T00:00:00Z", typ: "date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cutoff, err := time.Parse(time.RFC3339Nano, tc.value)
			require.NoError(t, err)
			cfg := &interfaces.FilterCondCfg{Name: "at", Operation: "before", Value: []any{1, "day"}}
			cond := &filter_condition.BeforeCond{Cfg: cfg, Lfield: &interfaces.Property{Name: "at", OriginalType: tc.typ}}
			err = validateIndexTimeBoundary(cond, cutoff, 1, "day")
			if tc.bad {
				var rangeErr *interfaces.TemporalRangeError
				require.ErrorAs(t, err, &rangeErr)
				assert.Equal(t, tc.typ, rangeErr.FieldType)
				assert.Equal(t, "at", rangeErr.Field)
			} else {
				require.NoError(t, err)
			}
		})
	}
	t.Run("captures the actual field without retaining the condition", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Name: "published_at"}
		field := &interfaces.Property{Name: "source_at", OriginalType: "date_nanos"}
		cond := &filter_condition.BeforeCond{Cfg: cfg, Lfield: field}
		err := validateIndexTimeBoundary(cond, time.Unix(0, -1), 1, "day")
		var temporal *interfaces.TemporalRangeError
		require.ErrorAs(t, err, &temporal)
		assert.Equal(t, "source_at", temporal.Field)
		cfg.Name = "renamed_request"
		field.Name = "renamed_source"
		assert.Equal(t, "source_at", temporal.Field)
	})
}

func TestIndexBeforeCutoff(t *testing.T) {
	for _, tc := range []struct {
		name, now, zone, unit, want string
		interval                    int64
	}{
		{name: "month end clamps", now: "2024-03-31T12:30:00.123456789Z", zone: "UTC", unit: "month", interval: 1, want: "2024-02-29T12:30:00.123456789Z"},
		{name: "leap year clamps", now: "2024-02-29T12:30:00Z", zone: "UTC", unit: "year", interval: 1, want: "2023-02-28T12:30:00Z"},
		{name: "calendar week", now: "2024-03-31T12:30:00Z", zone: "UTC", unit: "week", interval: 1, want: "2024-03-24T12:30:00Z"},
		{name: "day follows the location calendar", now: "2024-03-10T16:00:00Z", zone: "America/New_York", unit: "day", interval: 1, want: "2024-03-09T17:00:00Z"},
		{name: "month uses the current local date", now: "2024-03-30T16:30:00.123456789Z", zone: "Asia/Shanghai", unit: "month", interval: 1, want: "2024-02-28T16:30:00.123456789Z"},
		{name: "hours use elapsed time", now: "2024-03-10T07:30:00Z", zone: "America/New_York", unit: "hour", interval: 1, want: "2024-03-10T06:30:00Z"},
		{name: "large hours avoid duration overflow", now: "2024-03-31T00:00:00Z", zone: "UTC", unit: "hour", interval: 2147483647},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor, err := time.Parse(time.RFC3339Nano, tc.now)
			require.NoError(t, err)
			location, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			got := indexBeforeCutoff(anchor, tc.interval, tc.unit, location)
			assert.Equal(t, location, got.Location())
			if tc.want != "" {
				want, err := time.Parse(time.RFC3339Nano, tc.want)
				require.NoError(t, err)
				assert.True(t, got.Equal(want), "got %s, want %s", got, want)
			} else {
				assert.Equal(t, anchor.Unix()-tc.interval*3600, got.Unix())
			}
		})
	}
}

func TestOpenSearchConnectorTextKeywordOperations(t *testing.T) {
	conn := &OpenSearchConnector{}
	for _, op := range []string{"prefix", "not_prefix", "regex", "contain", "not_contain"} {
		t.Run(op, func(t *testing.T) {
			field := &interfaces.Property{Name: "body", OriginalName: "physical.body", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 8}}}}
			var value any = "hello"
			if op == "contain" || op == "not_contain" {
				value = []any{"hello", "world"}
			}
			cond, err := filter_condition.NewFilterCondition(t.Context(), &interfaces.FilterCondCfg{Name: "body", Operation: op, ValueFrom: interfaces.ValueFrom_Const, Value: value}, map[string]*interfaces.Property{"body": field})
			require.NoError(t, err)
			got, err := conn.ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.NoError(t, err)
			switch op {
			case "prefix":
				assert.Equal(t, map[string]any{"prefix": map[string]any{"physical.body.raw": "hello"}}, got)
			case "not_prefix":
				assert.Equal(t, map[string]any{"bool": map[string]any{"must_not": map[string]any{"prefix": map[string]any{"physical.body.raw": "hello"}}}}, got)
			case "regex":
				assert.Equal(t, map[string]any{"regexp": map[string]any{"physical.body.raw": "hello"}}, got)
			case "contain":
				assert.Equal(t, map[string]any{"bool": map[string]any{"should": []map[string]any{{"term": map[string]any{"physical.body.raw": "hello"}}, {"term": map[string]any{"physical.body.raw": "world"}}}, "minimum_should_match": 1}}, got)
			case "not_contain":
				assert.Equal(t, map[string]any{"bool": map[string]any{"must_not": []map[string]any{{"term": map[string]any{"physical.body.raw": "hello"}}, {"term": map[string]any{"physical.body.raw": "world"}}}}}, got)
			}
			field.Features = nil
			got, err = conn.ConvertFilterCondition(cond, []*interfaces.Property{field})
			require.Error(t, err)
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), "no keyword feature")
		})
	}
}

func TestOpenSearchConnectorConvertFilterConditionMixedTemporalFields(t *testing.T) {
	for _, temporal := range []string{interfaces.DataType_Date, interfaces.DataType_Datetime, interfaces.DataType_Timestamp} {
		for _, other := range []string{interfaces.DataType_Integer, interfaces.DataType_String} {
			for _, types := range [][2]string{{temporal, other}, {other, temporal}} {
				for _, op := range []string{"==", "!=", ">", ">=", "<", "<="} {
					t.Run(types[0]+"/"+op+"/"+types[1], func(t *testing.T) {
						schema := []*interfaces.Property{{Name: "left", OriginalName: "physical.left", Type: types[0]}, {Name: "right", OriginalName: "physical.right", Type: types[1]}}
						cond, err := filter_condition.NewFilterCondition(t.Context(), &interfaces.FilterCondCfg{Name: "left", Operation: op, ValueFrom: interfaces.ValueFrom_Field, Value: "right"}, map[string]*interfaces.Property{"left": schema[0], "right": schema[1]})
						require.NoError(t, err)
						dsl, err := (&OpenSearchConnector{}).ConvertFilterCondition(cond, schema)
						var buildErr *interfaces.ConditionBuildError
						require.ErrorAs(t, err, &buildErr)
						assert.Nil(t, dsl)
					})
				}
			}
		}
	}
}
