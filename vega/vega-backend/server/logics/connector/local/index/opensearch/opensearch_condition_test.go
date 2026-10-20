// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"context"
	"encoding/json"
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
				for _, size := range []int{1024, 1025, 5000} {
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
					if size <= 1024 {
						require.NoError(t, err)
						assert.Equal(t, size, countIndexRangeClauses(dsl))
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
			{"!=", first, map[string]any{"bool": map[string]any{"must_not": firstQuery}}},
			{"in", []any{first, second}, union},
			{"not_in", []any{first, second}, map[string]any{"bool": map[string]any{"must_not": union}}},
		} {
			cfg := &interfaces.FilterCondCfg{Name: "created", Operation: tc.op, ValueOptCfg: interfaces.ValueOptCfg{Value: tc.value}}
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
					cfg := &interfaces.FilterCondCfg{Name: "created", Operation: op, ValueOptCfg: interfaces.ValueOptCfg{Value: value}}
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
		cfg := &interfaces.FilterCondCfg{Name: "created", Operation: ">", ValueOptCfg: interfaces.ValueOptCfg{Value: "2026-10-19T00:00:00.123456789Z"}}
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
					cfg := &interfaces.FilterCondCfg{Name: "value", Operation: op, ValueOptCfg: interfaces.ValueOptCfg{Value: value}}
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
		alias := &interfaces.Property{Name: "title_alias", OriginalName: "title_alias", OriginalType: "alias",
			Type: interfaces.DataType_Text, Attributes: map[string]any{"path": "physical.title"}}
		schema := []*interfaces.Property{target, alias}
		fields := map[string]*interfaces.Property{target.Name: target, alias.Name: alias}
		for _, operation := range []string{
			filter_condition.OperationEqual, filter_condition.OperationNotEqual,
			filter_condition.OperationIn, filter_condition.OperationNotIn,
			filter_condition.OperationLike, filter_condition.OperationNotLike,
		} {
			t.Run(operation, func(t *testing.T) {
				var value any = "hello"
				if operation == filter_condition.OperationIn || operation == filter_condition.OperationNotIn {
					value = []any{"hello"}
				}
				cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: operation,
					ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: value}}
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
		for _, operation := range []string{filter_condition.OperationEqual, filter_condition.OperationNotEqual,
			filter_condition.OperationIn, filter_condition.OperationNotIn} {
			t.Run(operation+" enforces target ignore_above", func(t *testing.T) {
				var value any = "too-long-value"
				if operation == filter_condition.OperationIn || operation == filter_condition.OperationNotIn {
					value = []any{value}
				}
				cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: operation,
					ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: value}}
				condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
				require.NoError(t, err)
				dsl, err := conn.ConvertFilterCondition(condition, schema)
				require.ErrorContains(t, err, "ignore_above")
				assert.Nil(t, dsl)
			})
		}
		t.Run("field comparisons resolve aliases on both sides", func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: filter_condition.OperationEqual,
				ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Field, Value: alias.Name}}
			condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, fields)
			require.NoError(t, err)
			dsl, err := conn.ConvertFilterCondition(condition, schema)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"script": map[string]any{
				"source": "doc[params.left].value == doc[params.right].value",
				"params": map[string]any{"left": "physical.title.raw", "right": "physical.title.raw"},
			}}, dsl)
		})
		t.Run("missing keyword feature is still rejected", func(t *testing.T) {
			target.Features = nil
			cfg := &interfaces.FilterCondCfg{Name: alias.Name, Operation: filter_condition.OperationEqual,
				ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: "hello"}}
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
			name: "not equal wraps must_not",
			cfg:  osConstCfg("name", filter_condition.OperationNotEqual, "alice"),
			want: map[string]any{
				"bool": map[string]any{
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
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
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
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
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
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     []float32{0.1, 0.2},
			},
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
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     []float32{0.1, 0.2},
			},
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
				ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Field, Value: "right"},
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
				"script": map[string]any{
					"source": "doc[params.left].value " + operator + " doc[params.right].value",
					"params": map[string]any{
						"left":  "left.raw",
						"right": "right.exact",
					},
				},
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
			ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Field, Value: "right"},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
		require.NoError(t, err)

		got, err := conn.ConvertFilterCondition(cond, schema)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"script": map[string]any{
				"source": "doc[params.left].value == doc[params.right].value",
				"params": map[string]any{
					"left":  "left.ra'w",
					"right": "right.ex'act",
				},
			},
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
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Field,
					Value:     "right",
				},
			}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
			require.NoError(t, err)

			got, err := conn.ConvertFilterCondition(cond, schema)

			require.NoError(t, err)
			assert.Equal(t, map[string]any{
				"script": map[string]any{
					"source": "doc[params.left].value " + tt.operator + " doc[params.right].value",
					"params": map[string]any{
						"left":  "le'ft",
						"right": "ri'ght",
					},
				},
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
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     value,
		},
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
